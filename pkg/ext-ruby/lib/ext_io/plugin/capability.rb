# frozen_string_literal: true

require_relative "guest"

module ExtIO
  module Plugin
    # Typed definitions of the SDK's optional capability contracts, with the same wire shapes
    # and limits as the Go library: +ext.health+, +ext.configuration+ and +ext.stream+. Router is
    # a handler for Plugin.serve_stdio that checks each payload and result against them before
    # and after your code runs.
    #
    #   router = ExtIO::Plugin::Capability::Router.new
    #   router.health { { "status" => "healthy", "ready" => true } }
    #   ExtIO::Plugin.serve_stdio("plugin.json", router)
    module Capability
      MAX_STREAM_ITEMS = 256
      MAX_STREAM_BATCH_BYTES = 1 << 20
      MAX_ID = 64
      MAX_NAME = 256
      STATUSES = %w[healthy degraded unhealthy].freeze

      module_function

      # +value+ must be a Hash with exactly these keys.
      def object(value, fields)
        unless value.is_a?(Hash) && value.keys.sort == fields.sort
          raise ArgumentError, "object does not have exactly the fields #{fields.inspect}"
        end

        value
      end

      def text(value, limit)
        return if value.is_a?(String) && !value.empty? && value.length <= limit

        raise ArgumentError, "expected a non-empty string of at most #{limit} characters"
      end

      def integer(value)
        raise ArgumentError, "expected an integer" unless value.is_a?(Integer)
      end

      def health_request(value)
        object(value, [])
      end

      def health(value)
        object(value, %w[status ready])
        raise ArgumentError, "invalid health" unless STATUSES.include?(value["status"]) && [true, false].include?(value["ready"])

        value
      end

      def configuration(value)
        object(value, %w[instance revision values])
        text(value["instance"], MAX_NAME)
        text(value["revision"], MAX_NAME)
        value
      end

      def configured(value)
        object(value, %w[instance revision])
        raise ArgumentError, "invalid configured" unless value["instance"].is_a?(String) && value["revision"].is_a?(String)

        value
      end

      def stream_open(value)
        object(value, %w[parameters])
      end

      def stream_opened(value)
        object(value, %w[id])
        text(value["id"], MAX_ID)
        value
      end

      def stream_read(value)
        object(value, %w[id sequence limit])
        text(value["id"], MAX_ID)
        integer(value["sequence"])
        integer(value["limit"])
        raise ArgumentError, "invalid read" if value["sequence"] < 1 || !value["limit"].between?(1, MAX_STREAM_ITEMS)

        value
      end

      def stream_close(value)
        object(value, %w[id])
        text(value["id"], MAX_ID)
        value
      end

      def stream_closed(value)
        object(value, [])
      end

      # At most MAX_STREAM_ITEMS items and MAX_STREAM_BATCH_BYTES of JSON.
      def stream_batch(value)
        object(value, %w[items done])
        raise ArgumentError, "invalid batch" unless value["items"].is_a?(Array) && [true, false].include?(value["done"])
        raise ArgumentError, "too many items" if value["items"].length > MAX_STREAM_ITEMS

        size = 0
        value["items"].each do |item|
          out = +""
          Encoder.write(item, out, 0, false)
          size += out.bytesize
          raise ArgumentError, "batch too large" if size > MAX_STREAM_BATCH_BYTES
        end
        value
      end

      # Dispatches (contract, operation) to registered functions, validating the payload before
      # the call and the result after it. A payload that fails validation is an +invalid_input+
      # remote error; a result that fails is an internal error, because it is the plugin's own
      # bug. Results are read as the host reads them, so symbol keys work.
      class Router
        def initialize
          @routes = {}
        end

        # Registers +function+ (or the block), called with the payload, for +operation+ of
        # +contract+ at +version+. +check_input+ and +check_output+ take a value and raise
        # ArgumentError when it does not fit.
        def on(contract, version, operation, function = nil, check_input: nil, check_output: nil, &block)
          function ||= block
          raise TypeError, "function is required" unless function.respond_to?(:call)

          key = [contract, version, operation]
          raise ArgumentError, "operation already registered" if @routes.key?(key)

          @routes[key] = [function, check_input, check_output]
          self
        end

        # +check+ (or the block) returns {"status" => ..., "ready" => ...}.
        def health(check = nil, &block)
          check ||= block
          on("ext.health", "v1", "check", ->(_p) { check.call },
             check_input: Capability.method(:health_request), check_output: Capability.method(:health))
        end

        # +update+ (or the block) takes (instance, revision, values); the router answers.
        def configuration(update = nil, &block)
          update ||= block
          run = lambda do |payload|
            update.call(payload["instance"], payload["revision"], payload["values"])
            { "instance" => payload["instance"], "revision" => payload["revision"] }
          end
          on("ext.configuration", "v1", "update", run,
             check_input: Capability.method(:configuration), check_output: Capability.method(:configured))
        end

        # +open+ takes the parameters and returns an id; +read+ takes (id, sequence, limit) and
        # returns {"items" => [...], "done" => ...}; +close+ takes the id.
        def stream(open:, read:, close:)
          on("ext.stream", "v1", "open", ->(p) { { "id" => open.call(p["parameters"]) } },
             check_input: Capability.method(:stream_open), check_output: Capability.method(:stream_opened))
          on("ext.stream", "v1", "read", ->(p) { read.call(p["id"], p["sequence"], p["limit"]) },
             check_input: Capability.method(:stream_read), check_output: Capability.method(:stream_batch))
          on("ext.stream", "v1", "close", ->(p) { close.call(p["id"]) || {} },
             check_input: Capability.method(:stream_close), check_output: Capability.method(:stream_closed))
        end

        # The handler: +contract+ is a name (version v1) or a [name, version] pair.
        def call(contract, operation, payload)
          name, version = contract.is_a?(Array) ? contract : [contract, "v1"]
          function, check_input, check_output = @routes[[name, version, operation]]
          raise RemoteError.new("unsupported", "operation is not implemented") if function.nil?

          begin
            check_input&.call(payload)
          rescue ArgumentError
            raise RemoteError.new("invalid_input", "request payload does not satisfy operation contract")
          end
          result = function.call(payload)
          return result if check_output.nil?

          # A failure here is the plugin's bug and surfaces as an internal error.
          result = Plugin.json_copy(result)
          check_output.call(result)
          result
        end

        def to_proc
          method(:call).to_proc
        end
      end
    end
  end
end
