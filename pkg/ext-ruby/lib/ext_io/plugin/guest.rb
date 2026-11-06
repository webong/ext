# frozen_string_literal: true

require_relative "core"
require_relative "frames"

module ExtIO
  module Plugin
    DEFAULT_MAX_CALL_SECONDS = 30.0

    # Details of the call being handled, for handlers that need them.
    #
    # On a transport that carries frames (a guest served by Plugin.serve) the call can also
    # report progress and call its host: see #progress, #progress_limits and #reverse. This
    # guest reads inline, so #session_reverse is always nil and a handler makes one reverse call
    # at a time, from its own thread.
    class CallInfo
      attr_reader :request_id, :contract_name, :contract_version, :operation, :surface, :deadline
      # The rate the host set for this call's events (a ProgressLimits), or nil when the
      # operation reports none.
      attr_accessor :progress_limits
      # A ReverseCaller for host operations, bound by the call's deadline, or nil on a transport
      # with no frames.
      attr_accessor :reverse
      # Always nil here: nobody reads the connection between calls.
      attr_reader :session_reverse
      attr_writer :reporter # :nodoc:

      def initialize(request_id, contract_name, contract_version, operation, surface, deadline)
        @request_id = request_id
        @contract_name = contract_name
        @contract_version = contract_version
        @operation = operation
        @surface = surface
        @deadline = deadline # a Time
        @progress_limits = nil
        @reverse = nil
        @session_reverse = nil
        @reporter = nil
      end

      # Seconds left before the deadline (negative once it has passed).
      def remaining
        @deadline - Time.now
      end

      # Reports a Progress event to the host while the call runs. Events reach the host in
      # order, before the result. Returns Plugin::SENT, or Plugin::DROPPED when the rate limit
      # dropped it (routine: the next event that passes carries the count). Raises FrameError:
      # +unsupported+ when the operation did not declare progress, the host asked for no events
      # or there is no frame channel (stop reporting), +invalid+ for an event that breaks the
      # rules (nothing is sent), +closed+ once the call is over.
      def progress(event)
        raise FrameError.new("unsupported", "no progress channel on this call") if @reporter.nil?

        @reporter.report(event)
      end
    end

    @current = nil

    class << self
      # The CallInfo of the in-flight call, or nil outside a handler.
      def current_call
        @current
      end

      attr_writer :current # :nodoc:
    end

    module_function

    # --- descriptor -------------------------------------------------------------

    # Raises ArgumentError unless +d+ is a valid ext.plugin/v1 descriptor (string keys, as
    # decoded from JSON).
    def validate_descriptor(d)
      unless d.is_a?(Hash) && d.each_key.all? { |k| %w[apiVersion identity contracts reverse].include?(k) }
        raise ArgumentError, "descriptor must be an object with apiVersion, identity, contracts"
      end
      raise ArgumentError, "unsupported apiVersion" if d["apiVersion"] != API_VERSION

      ident = d["identity"]
      unless ident.is_a?(Hash) && ident.each_key.all? { |k| %w[id revision version].include?(k) }
        raise ArgumentError, "invalid identity"
      end
      unless match?(IDENTIFIER, ident["id"]) && match?(REVISION, ident["revision"])
        raise ArgumentError, "identity requires a bounded id and immutable revision"
      end
      if ident.key?("version") && ident["version"] != "" && !match?(REVISION, ident["version"])
        raise ArgumentError, "invalid identity version"
      end

      contracts = d["contracts"]
      raise ArgumentError, "declare 1..64 contracts" unless contracts.is_a?(Array) && contracts.length.between?(1, 64)

      validate_contracts(contracts, false)
      # The host operations the plugin may call; none is a null or empty list.
      reverse = d["reverse"]
      unless reverse.nil?
        raise ArgumentError, "declare at most 64 reverse contracts" unless reverse.is_a?(Array) && reverse.length <= 64

        validate_contracts(reverse, true)
      end
      nil
    end

    def validate_contracts(contracts, reverse)
      seen = {}
      contracts.each do |c|
        raise ArgumentError, "invalid contract" unless c.is_a?(Hash) && c.each_key.all? { |k| %w[name version operations].include?(k) }
        raise ArgumentError, "contract requires a name and exact version" unless match?(IDENTIFIER, c["name"]) && match?(REVISION, c["version"])

        key = [c["name"], c["version"]]
        ops = c["operations"]
        if seen.key?(key) || !ops.is_a?(Array) || !ops.length.between?(1, 256)
          raise ArgumentError, "duplicate contract or invalid operation count"
        end

        seen[key] = true
        names = {}
        ops.each do |op|
          raise ArgumentError, "invalid operation" unless op.is_a?(Hash) && op.each_key.all? { |k| %w[name surface progress].include?(k) }

          name = op["name"]
          surface = op["surface"]
          progress = op["progress"]
          # Go leaves a member that is null alone, as if absent.
          surface = "" if surface.nil?
          if !match?(IDENTIFIER, name) ||
             name == "plugin.hello" ||
             names.key?(name) ||
             !surface.is_a?(String) ||
             (surface != "" && !match?(IDENTIFIER, surface)) ||
             !(progress.nil? || progress == true || progress == false) ||
             (progress == true && reverse) # a host operation does not report progress
            raise ArgumentError, "invalid, reserved, or duplicate operation"
          end

          names[name] = true
        end
      end
    end
    private_class_method :validate_contracts

    # Reads a descriptor from a plugin.json (ext.package/v1) or a bare descriptor file.
    def load_descriptor(path)
      raw = File.open(path, "rb") { |f| f.read((16 << 20) + 1) } || +""
      raise ArgumentError, "descriptor file exceeds 16 MiB" if raw.bytesize > 16 << 20

      begin
        data = decode_frame(raw)
      rescue ProtocolError => e
        raise ArgumentError, e.message
      end
      if data.is_a?(Hash) && data.key?("descriptor")
        raise ArgumentError, "unsupported package apiVersion" if data["apiVersion"] != "ext.package/v1"

        data = data["descriptor"]
      end
      validate_descriptor(data)
      data
    end

    # --- guest ------------------------------------------------------------------

    HELLO_FIELDS = %w[apiVersion id plugin contract operation surface deadline payload].freeze
    REQUEST_FIELDS = (HELLO_FIELDS + %w[progress parent]).freeze
    INT64 = (-(1 << 63))...(1 << 63)

    # The rate a request sets for a call's progress events: nil when absent, else
    # [events_per_second, bytes_per_second] with 0 for a member left out. Go decodes the members
    # into int64s, so a number that is not a whole one that fits is not a request at all.
    def rate_of(value)
      return nil if value.nil?
      unless value.is_a?(Hash) && value.each_key.all? { |k| %w[eventsPerSecond bytesPerSecond].include?(k) }
        raise ProtocolError, "invalid progress rate"
      end

      %w[eventsPerSecond bytesPerSecond].map do |key|
        n = value[key]
        n = 0 if n.nil?
        raise ProtocolError, "invalid progress rate" unless n.is_a?(Integer) && INT64.cover?(n)

        n
      end
    end

    def identity_of(obj, fields)
      return fields.to_h { |f| [f, ""] } if obj.nil?
      raise ProtocolError, "invalid envelope object" unless obj.is_a?(Hash) && obj.each_key.all? { |k| fields.include?(k) }

      fields.to_h do |f|
        v = obj.fetch(f, "")
        raise ProtocolError, "invalid envelope field" unless v.is_a?(String)

        [f, v]
      end
    end

    # An immutable descriptor plus a domain handler.
    #
    # The handler is anything that responds to +call(contract_name, operation, payload)+ (a
    # block, a lambda, a method, a Capability::Router). It returns a JSON-serializable result
    # (Hash, Array, String, Symbol, Integer, finite Float, true, false or nil), or raises
    # RemoteError for a deliberate public failure. Any other error becomes
    # +RemoteError("provider_error", message cut to 4096 bytes)+; pass +redact_errors: true+ to
    # send a generic +operation_failed+ instead, so internal text never reaches the host. Calls
    # are sequential.
    class Guest
      # What a handler failure is: everything a program means to raise. Signals, exit and
      # running out of memory still end the guest.
      HANDLER_FAILURES = [StandardError, ScriptError, SystemStackError].freeze

      def initialize(descriptor, handler = nil, max_call_seconds: DEFAULT_MAX_CALL_SECONDS, redact_errors: false, &block)
        descriptor = if descriptor.is_a?(Hash)
                       begin
                         Plugin.json_copy(descriptor)
                       rescue EncodeError => e
                         raise ArgumentError, "descriptor is not JSON: #{e.message}"
                       end.tap { |d| Plugin.validate_descriptor(d) }
                     else
                       Plugin.load_descriptor(descriptor)
                     end
        handler ||= block
        raise TypeError, "handler is required" unless handler.respond_to?(:call)
        raise ArgumentError, "max_call_seconds must be positive" unless max_call_seconds.is_a?(Numeric) && max_call_seconds.positive?

        @descriptor = descriptor
        @handler = handler
        @max_call = max_call_seconds.to_f
        @redact = redact_errors ? true : false
        @ops = {} # [contract, version, operation] => [surface, reports progress]
        @descriptor["contracts"].each do |c|
          c["operations"].each do |op|
            @ops[[c["name"], c["version"], op["name"]]] = [op["surface"] || "", op["progress"] == true]
          end
        end
        @reverse = {} # [contract, version, operation] the plugin may call on its host
        (@descriptor["reverse"] || []).each do |c|
          c["operations"].each { |op| @reverse[[c["name"], c["version"], op["name"]]] = true }
        end
        ident = @descriptor["identity"]
        @identity = { "id" => ident["id"], "revision" => ident["revision"], "version" => ident.fetch("version", "") }
      end

      def handshake
        Plugin.json_copy(@descriptor)
      end

      # Validates one decoded request and returns the response object.
      #
      # Raises ProtocolError for envelopes the Go host would not even decode (unknown fields,
      # wrong types); those end the stream. +link+ is the connection the request came in on
      # (Plugin.serve passes it): without one the call can neither report progress nor call its
      # host.
      def invoke(request, link = nil)
        raise ProtocolError, "invalid request envelope" unless request.is_a?(Hash) && request.each_key.all? { |k| REQUEST_FIELDS.include?(k) }

        request_id = request["id"]
        raise ProtocolError, "invalid request id" unless request_id.is_a?(String)

        plugin = Plugin.identity_of(request["plugin"], %w[id revision version])
        contract = Plugin.identity_of(request["contract"], %w[name version])
        %w[apiVersion operation surface].each do |k|
          raise ProtocolError, "invalid request field" if request.key?(k) && !request[k].is_a?(String)
        end
        raise ProtocolError, "invalid request deadline" if request.key?("deadline") && !request["deadline"].is_a?(String)
        raise ProtocolError, "invalid request parent" if !request["parent"].nil? && !request["parent"].is_a?(String)

        rate = Plugin.rate_of(request["progress"])
        response = { "apiVersion" => API_VERSION, "id" => request_id }
        deadline = Plugin.parse_deadline(request["deadline"])
        operation = request.fetch("operation", "")
        surface = request.fetch("surface", "")
        key = [contract["name"], contract["version"], operation]
        if request["apiVersion"] != API_VERSION ||
           !Plugin.match?(REVISION, request_id) ||
           deadline.nil? ||
           plugin != @identity ||
           !@ops.key?(key) ||
           @ops[key][0] != surface
          response["error"] = { "code" => "invalid_request", "message" => "request does not match selected contract" }
          return response
        end

        limit = [deadline, Time.now + @max_call].min
        error = nil
        result = nil
        if Time.now >= limit
          error = failure("deadline exceeded")
        else
          info = CallInfo.new(request_id, contract["name"], contract["version"], operation, surface, limit)
          Plugin.current = info
          reporter = reverse_caller = nil
          unless link.nil?
            # A call reports progress when its operation declared it and the host asked for
            # events; it can call the host on the connection it came in on.
            if !rate.nil? && @ops[key][1]
              info.progress_limits = ProgressLimits.new(
                rate[0].positive? ? rate[0] : DEFAULT_PROGRESS_EVENTS,
                rate[1].positive? ? rate[1] : DEFAULT_PROGRESS_BYTES
              )
              reporter = Frames::Reporter.new(link, request_id, info.progress_limits)
              info.reporter = reporter
            end
            reverse_caller = info.reverse = Frames::ReverseCaller.new(link, @reverse, request_id, limit)
          end
          begin
            result = @handler.call(contract["name"], operation, request["payload"])
            Plugin.encode_frame(result) # surface unserializable results as provider errors
            error = failure("deadline exceeded") if Time.now >= limit
          rescue RemoteError => e
            error = e.to_wire
          rescue *HANDLER_FAILURES => e
            error = failure(e.message)
          ensure
            Plugin.current = nil
            reverse_caller.over = true unless reverse_caller.nil? # a request names its call, so a caller cannot outlive it
            reporter&.finish # a marker for events never reported, before the response
          end
        end
        if error.nil?
          response["payload"] = result
        else
          response["error"] = error
        end
        response
      end

      private

      def failure(message)
        return { "code" => "operation_failed", "message" => "plugin operation failed" } if @redact

        text = message.to_s
        text = Plugin.utf8(text) || text.dup.force_encoding(Encoding::UTF_8).scrub("?")
        { "code" => "provider_error", "message" => text.byteslice(0, MAX_MESSAGE_BYTES).scrub("") }
      end
    end

    # Serves +guest+ over binary streams until EOF. Raises ProtocolError on violations.
    #
    # A handler can report progress and call its host (see CallInfo). The host sends nothing
    # during a call but the answer to a reverse call: anything else ends the session with
    # ProtocolError, and the call in flight is not answered. When the input ends during a call,
    # the call is answered and the session ends.
    def serve(guest, input, output)
      link = Frames::Link.new(input, output)
      hello = false
      loop do
        raw = read_frame(input)
        return nil if raw.nil?

        request = decode_frame(raw)
        if hello
          response = guest.invoke(request, link)
          raise link.failure unless link.failure.nil? # the host broke the rules while the handler waited
        else
          response = answer_hello(guest, request)
          hello = true
        end
        frame = encode_frame(response)
        if frame.bytesize > MAX_FRAME_BYTES
          response = {
            "apiVersion" => API_VERSION,
            "id" => response["id"],
            "error" => { "code" => "response_too_large", "message" => "response exceeds the frame limit" }
          }
          frame = encode_frame(response)
        end
        link.write(frame)
      end
    end

    def answer_hello(guest, request)
      raise ProtocolError, "invalid hello" unless request.is_a?(Hash) && request.each_key.all? { |k| HELLO_FIELDS.include?(k) }

      plugin = identity_of(request["plugin"], %w[id revision version])
      contract = identity_of(request["contract"], %w[name version])
      deadline = parse_deadline(request["deadline"])
      if request["apiVersion"] != API_VERSION ||
         request["id"] != "hello" ||
         request["operation"] != "plugin.hello" ||
         plugin.values.any? { |v| !v.empty? } ||
         contract.values.any? { |v| !v.empty? } ||
         request.fetch("surface", "") != "" ||
         request.key?("payload") ||
         deadline.nil?
        raise ProtocolError, "invalid hello"
      end
      raise ProtocolError, "hello deadline exceeded" if Time.now >= deadline

      { "apiVersion" => API_VERSION, "id" => "hello", "payload" => guest.handshake }
    end
    private_class_method :answer_hello

    # Serves on this process's standard input and output. Returns at EOF.
    #
    # +descriptor+ is a Hash or the path of a plugin.json / descriptor file; the handler is the
    # second argument or the block. Protocol frames use a private duplicate of the original
    # standard output; file descriptor 1 (and so $stdout and STDOUT) is redirected to standard
    # error, so stray output cannot corrupt the stream. Call once, from the guest's main.
    def serve_stdio(descriptor, handler = nil, max_call_seconds: DEFAULT_MAX_CALL_SECONDS, redact_errors: false, &block)
      guest = Guest.new(descriptor, handler, max_call_seconds: max_call_seconds, redact_errors: redact_errors, &block)
      $stdout.flush
      out = STDOUT.dup
      out.binmode
      out.sync = true
      STDOUT.reopen(STDERR)
      $stdout = STDOUT
      input = STDIN
      input.binmode
      begin
        serve(guest, input, out)
      ensure
        out.close
      end
    end
  end
end
