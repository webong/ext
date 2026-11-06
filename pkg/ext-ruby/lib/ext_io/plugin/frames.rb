# frozen_string_literal: true

require_relative "core"

module ExtIO
  module Plugin
    MAX_PROGRESS_TEXT = 2048
    DEFAULT_PROGRESS_EVENTS = 50
    DEFAULT_PROGRESS_BYTES = 64 * 1024

    # What CallInfo#progress returns: the event was written, or the rate limit dropped it
    # (routine).
    SENT = :sent
    DROPPED = :dropped

    # The rate the host set for a call's progress events.
    ProgressLimits = Struct.new(:events_per_second, :bytes_per_second)
    # A contract by name and exact version, as ReverseCaller#call takes it (a two-element Array
    # works too).
    ContractRef = Struct.new(:name, :version)

    # A progress or reverse-call operation failed locally (a host's refusal is a RemoteError).
    #
    # +code+ is +unsupported+ (no reporter or caller on this call), +invalid+ (the event or
    # request breaks the rules; nothing was sent), +closed+ (the call is over or the connection
    # ended), or +protocol+ (the host broke the framing rules; the session ends).
    class FrameError < StandardError
      attr_reader :code

      def initialize(code, message = nil)
        super(message || code)
        @code = code
      end
    end

    # One progress event: a +progress+ count of +completed+ out of +total+ (zero when not known),
    # or a +log+ line, optionally on a +stream+ (+out+ or +err+). Text is at most 2048 bytes of
    # UTF-8; use Plugin.split_progress_text to cut a longer line.
    class Progress
      attr_reader :kind, :stream, :text, :completed, :total

      def initialize(kind, stream = "", text = "", completed = 0, total = 0)
        @kind = kind
        @stream = stream
        @text = text
        @completed = completed
        @total = total
      end

      def self.log(text)
        new("log", "", text)
      end

      # A log line of a program's standard output.
      def self.out(text)
        new("log", "out", text)
      end

      # A log line of a program's standard error.
      def self.err(text)
        new("log", "err", text)
      end

      # +completed+ of +total+; zero when the total is not known.
      def self.fraction(completed, total)
        new("progress", "", "", completed, total)
      end
    end

    module_function

    # Cuts text into pieces that each fit one event: at most 2048 bytes of UTF-8, never ending
    # inside a character. Bytes that are not UTF-8 become U+FFFD.
    def split_progress_text(text)
      text = text.to_s.dup.force_encoding(Encoding::UTF_8) unless text.is_a?(String) && text.encoding == Encoding::UTF_8
      text = text.scrub("�")
      pieces = []
      piece = +""
      text.each_char do |ch|
        if piece.bytesize + ch.bytesize > MAX_PROGRESS_TEXT
          pieces << piece
          piece = +""
        end
        piece << ch
      end
      pieces << piece unless piece.empty?
      pieces
    end

    # Progress events and reverse calls for a guest (docs/plugin-guest-frames.md).
    #
    # A guest on a transport that carries frames can report progress while a call runs and call
    # its host. The rules are the Go contract's, pinned by
    # lib/plugin/testdata/v1/{frame-cases,progress-limiter-cases}.json. This guest runs *inline*:
    # it has no reader of its own, so the thread that waits for the host's answer to a reverse
    # call reads the connection itself, which suffices because the host sends nothing but
    # answers during a call. A call therefore has one reverse call in flight, from the handler's
    # thread, and no session-scoped caller exists.
    module Frames
      # A token bucket counted in events and bytes of text, with a burst of twice the rate, and a
      # count of events dropped since the last one that passed. Time is a monotonic clock in
      # nanoseconds, given by the caller.
      class Limiter
        def initialize(events_per_second = 0, bytes_per_second = 0, now_ns = 0)
          @event_rate = (events_per_second.positive? ? events_per_second : DEFAULT_PROGRESS_EVENTS).to_f
          @byte_rate = (bytes_per_second.positive? ? bytes_per_second : DEFAULT_PROGRESS_BYTES).to_f
          @events = 2 * @event_rate
          @bytes = 2 * @byte_rate
          @last = now_ns
          @dropped = 0
        end

        # The number of events dropped since the previous one that passed (which the caller
        # attaches to this event), or nil when the rate limit drops it.
        def admit(text_bytes, now_ns)
          delta = now_ns - @last
          # Seconds as Go's Duration.Seconds computes them.
          whole = delta >= 0 ? delta / 1_000_000_000 : -(-delta / 1_000_000_000)
          elapsed = whole.to_f + ((delta - (whole * 1_000_000_000)).to_f / 1e9)
          if elapsed.positive?
            @events = [@events + (elapsed * @event_rate), 2 * @event_rate].min
            @bytes = [@bytes + (elapsed * @byte_rate), 2 * @byte_rate].min
            @last = now_ns
          end
          if @events < 1 || @bytes < text_bytes
            @dropped += 1
            return nil
          end
          @events -= 1
          @bytes -= text_bytes
          reported = @dropped
          @dropped = 0
          reported
        end

        # The drops not yet reported, reset to zero.
        def pending
          reported = @dropped
          @dropped = 0
          reported
        end
      end

      # --- judging frames ---------------------------------------------------------

      PROGRESS_FIELDS = %w[frame apiVersion parent kind stream text completed total dropped].freeze
      REVERSE_REQUEST_FIELDS = %w[frame apiVersion id parent contract operation deadline payload].freeze
      REVERSE_RESPONSE_FIELDS = %w[frame apiVersion id payload error].freeze

      module_function

      def violation
        raise ProtocolError, "plugin protocol violation"
      end

      def strict(value, fields)
        violation unless value.is_a?(Hash) && value.each_key.all? { |k| fields.include?(k) }
        value
      end

      # A member Go decodes into a string: absent and null are empty.
      def text(m, key)
        v = m[key]
        return "" if v.nil?

        violation unless v.is_a?(String)
        v
      end

      # A member Go decodes into a uint64: absent and null are zero; anything but a whole number
      # that fits is refused (1.0 and 1e2 are not whole numbers to Go).
      def count(m, key)
        v = m[key]
        return 0 if v.nil?

        violation unless v.is_a?(Integer) && v >= 0 && v < (1 << 64)
        v
      end

      def check_event(kind, stream, text, completed, total)
        size = Plugin.utf8_length(text)
        if !%w[progress log].include?(kind) ||
           !["", "out", "err"].include?(stream) ||
           (stream != "" && kind != "log") ||
           size.negative? || size > MAX_PROGRESS_TEXT ||
           (total != 0 && completed > total)
          violation
        end
      end

      # Judges a progress frame (a decoded value): returns its parent (the call it belongs to)
      # and the event as [kind, stream, text, completed, total, dropped]; raises ProtocolError.
      def validate_progress_frame(v)
        strict(v, PROGRESS_FIELDS)
        parent = text(v, "parent")
        event = [text(v, "kind"), text(v, "stream"), text(v, "text"), count(v, "completed"), count(v, "total"), count(v, "dropped")]
        violation if text(v, "frame") != "progress" || text(v, "apiVersion") != API_VERSION || !Plugin.match?(REVISION, parent)
        check_event(*event[0, 5])
        [parent, event]
      end

      # Judges a reverse request frame, as the host reads it; raises ProtocolError.
      def validate_reverse_request(v)
        strict(v, REVERSE_REQUEST_FIELDS)
        ref = strict(v["contract"], %w[name version])
        parent = text(v, "parent")
        if text(v, "frame") != "reverse_request" ||
           text(v, "apiVersion") != API_VERSION ||
           !Plugin.match?(REVISION, text(v, "id")) ||
           (parent != "" && !Plugin.match?(REVISION, parent)) ||
           !Plugin.match?(IDENTIFIER, text(ref, "name")) ||
           !Plugin.match?(REVISION, text(ref, "version")) ||
           !Plugin.match?(IDENTIFIER, text(v, "operation")) ||
           Plugin.parse_deadline(v["deadline"]).nil?
          violation
        end
        nil
      end

      # Judges a reverse response, which a guest accepts only for the request it is waiting on.
      # Returns [payload, nil] or [nil, RemoteError]; raises ProtocolError.
      def validate_reverse_response(v, request_id)
        strict(v, REVERSE_RESPONSE_FIELDS)
        violation if text(v, "frame") != "reverse_response" || text(v, "apiVersion") != API_VERSION || text(v, "id") != request_id
        failed = !v["error"].nil?
        # A response carries a payload (null counts) or an error, never both or neither.
        violation if failed == v.key?("payload")
        return [v["payload"], nil] unless failed

        e = strict(v["error"], %w[code message retryAfterMilliseconds])
        retry_after = count(e, "retryAfterMilliseconds")
        message = text(e, "message")
        size = Plugin.utf8_length(message)
        if !Plugin.match?(IDENTIFIER, text(e, "code")) || size.negative? || size > MAX_MESSAGE_BYTES || retry_after > MAX_RETRY_AFTER_MS
          violation
        end
        [nil, RemoteError.new(e["code"], message, retry_after_ms: retry_after)]
      end

      # --- the connection -----------------------------------------------------------

      # One connection with the host: frames written one at a time, and the read side that the
      # thread waiting for an answer drives. +failure+ is set when the host broke the rules.
      class Link
        attr_reader :owner, :failure
        attr_accessor :ended

        def initialize(input, output)
          @in = input
          @out = output
          @write = Mutex.new
          @owner = Thread.current
          @issued = 0
          @ended = false
          @failure = nil
        end

        # Writes one encoded frame and its line feed; frames never interleave.
        def write(frame)
          @write.synchronize do
            @out.write(frame.b + "\n")
            @out.flush
          end
        end

        # Calls a host operation and waits for the answer. +parent+ is the id of the call it
        # belongs to; +deadline+ is a Time. Returns the host's payload.
        def reverse(parent, name, version, operation, payload, deadline)
          raise FrameError.new("closed", "plugin connection ended") if @ended

          @issued += 1
          request_id = "g#{@issued}"
          frame = { "frame" => "reverse_request", "apiVersion" => API_VERSION, "id" => request_id }
          frame["parent"] = parent if parent && !parent.empty?
          frame["contract"] = { "name" => name, "version" => version }
          frame["operation"] = operation
          frame["deadline"] = Plugin.format_time(deadline)
          frame["payload"] = payload
          begin
            Frames.validate_reverse_request(frame)
            data = Plugin.encode_frame(frame)
            raise ArgumentError, "frame too large" if data.bytesize > MAX_FRAME_BYTES

            Plugin.decode_frame(data) # the payload must read back as the host reads it
          rescue ProtocolError, TypeError, ArgumentError, SystemStackError
            raise FrameError.new("invalid", "reverse request breaks the rules")
          end
          begin
            write(data)
          rescue IOError, SystemCallError
            @ended = true
            raise FrameError.new("closed", "plugin connection ended")
          end
          # The host sends nothing but this answer during a call.
          raw = begin
            Plugin.read_frame(@in)
          rescue ProtocolError
            raise violated
          rescue IOError, SystemCallError
            nil
          end
          if raw.nil?
            @ended = true
            raise FrameError.new("closed", "plugin connection ended")
          end
          begin
            answer = Plugin.decode_frame(raw)
            Frames.violation if !answer.is_a?(Hash) || answer["frame"] != "reverse_response" || answer["id"] != request_id
            result, error = Frames.validate_reverse_response(answer, request_id)
          rescue ProtocolError
            raise violated
          end
          raise error if error

          result
        end

        private

        def violated
          @failure = ProtocolError.new("plugin protocol violation")
          @ended = true
          FrameError.new("protocol", "plugin protocol violation")
        end
      end

      # Calls an operation the host offers (declared in the descriptor's +reverse+ list) and
      # waits for the answer. A guest that reads inline makes one call at a time, from the
      # thread that runs the handler.
      class ReverseCaller
        attr_accessor :over

        def initialize(link, declared, parent, deadline)
          @link = link
          @declared = declared
          @parent = parent
          @deadline = deadline
          @over = false
        end

        # Several reverse calls cannot be outstanding at once.
        def concurrent
          false
        end
        alias concurrent? concurrent

        # Calls +operation+ of +contract+ (a [name, version] pair or a ContractRef) with a JSON
        # payload and returns the host's payload. The host's refusal or error raises RemoteError
        # with its code (+reverse_undeclared+ when the descriptor does not list it, +denied+,
        # +busy+, +too_large+, +expired+, ...); a local failure raises FrameError. +timeout+
        # seconds bound the deadline the host is given, never past the call's own; the wait
        # itself ends with the host's answer or the end of the connection, since standard input
        # cannot be read with a timeout.
        def call(contract, operation, payload = nil, timeout: nil)
          name, version = contract.to_a
          raise RemoteError.new("reverse_undeclared", "reverse call refused") unless @declared.key?([name, version, operation])
          raise FrameError.new("closed", "the call is over") if @over
          raise FrameError.new("unsupported", "reverse calls are made from the handler's thread") unless Thread.current == @link.owner

          deadline = @deadline
          deadline = [deadline, Time.now + [0.0, timeout.to_f].max].min unless timeout.nil?
          @link.reverse(@parent, name, version, operation, payload, deadline)
        end
      end

      # Sends the progress events of one call, within the rate limit the host set.
      class Reporter
        def initialize(link, parent, limits)
          @link = link
          @parent = parent
          @limiter = Limiter.new(limits.events_per_second, limits.bytes_per_second, Frames.monotonic_ns)
          @lock = Mutex.new
          @finished = false
        end

        # Events reach the host in the order they are reported, and none after the call is over.
        def report(event)
          begin
            fields = [event.kind, event.stream, event.text, event.completed, event.total]
            unless fields[0].is_a?(String) && fields[1].is_a?(String) && fields[2].is_a?(String) &&
                   fields[3, 2].all? { |n| n.is_a?(Integer) && n >= 0 }
              Frames.violation
            end
            Frames.check_event(*fields)
          rescue ProtocolError, NoMethodError
            raise FrameError.new("invalid", "progress event breaks the rules")
          end
          @lock.synchronize do
            dropped = @limiter.admit(Plugin.utf8_length(event.text), Frames.monotonic_ns)
            return DROPPED if dropped.nil?
            raise FrameError.new("closed", "the call is over") if @finished

            send_event(event.kind, event.stream, Plugin.utf8(event.text), event.completed, event.total, dropped)
          end
          SENT
        end

        # Ends reporting after a marker for events never reported: nothing may follow the
        # response.
        def finish
          @lock.synchronize do
            return if @finished

            @finished = true
            pending = @limiter.pending
            if pending.positive?
              begin
                send_event("log", "", "", 0, 0, pending)
              rescue FrameError
                nil # the connection is gone; there is nobody to tell
              end
            end
          end
        end

        private

        def send_event(kind, stream, text, completed, total, dropped)
          frame = { "frame" => "progress", "apiVersion" => API_VERSION, "parent" => @parent, "kind" => kind }
          { "stream" => stream, "text" => text, "completed" => completed, "total" => total, "dropped" => dropped }.each do |key, value|
            frame[key] = value unless value == "" || value == 0 # rubocop:disable Style/NumericPredicate
          end
          begin
            @link.write(Plugin.encode_frame(frame))
          rescue IOError, SystemCallError
            raise FrameError.new("closed", "plugin connection ended")
          end
        end
      end

      def monotonic_ns
        Process.clock_gettime(Process::CLOCK_MONOTONIC, :nanosecond)
      end
    end

    ReverseCaller = Frames::ReverseCaller
  end
end
