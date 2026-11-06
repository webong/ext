# frozen_string_literal: true

# Host SDK for ext.plugin/v1: runs plugins through the C engine (lib/plugin).
#
#   require "ext_io/host"
#
#   engine = ExtIO::Host::Engine.new        # EXT_HOST_LIBRARY, or libext_host on the loader path
#   engine.process(descriptor, "/opt/plugins/echo/1.0.0/echo") do |host|
#     host.start
#     host.call({ "name" => "ext.echo", "version" => "v1" }, "echo", { "value" => 7 })
#   end
#
# The engine does the work a host must get right: admission, the handshake check against the
# descriptor, request and response validation, deadlines and the session's lifecycle. This file
# only moves bytes and policy decisions across a native extension, ExtIO::Host::Native, which
# loads the engine's shared library (libext_host) at run time and releases the global VM lock
# around every call that can block. Requiring "ext_io/plugin" (the guest) never loads either.
#
# Guests may be JSON-line child processes (Engine#process) or libraries that export the C ABI of
# ext_plugin.h (Engine#shared_library). Native code in a shared library runs inside this process
# with its full access.

require "json"
require "ext_io/plugin" # the ext-io gem, or lib/ beside this file in a checkout

module ExtIO
  module Host
    RemoteError = Plugin::RemoteError

    # A host failure: the engine's status by name (+status+: "denied", "closed" ...), its number
    # (+code+) and what was being done (+action+).
    class HostError < StandardError
      attr_reader :code, :status, :action

      def initialize(code, status, action = "")
        @code = code
        @status = status
        @action = action
        super("ext host: #{action.empty? ? "" : "#{action}: "}#{status}")
      end
    end

    require "ext_io/host/native"

    ABI_VERSION = Native::ABI_VERSION
    STATUS_NAMES = Native::STATUS_NAMES

    LIBRARY_NAMES = if Gem.win_platform?
                      ["ext_host.dll"]
                    elsif RUBY_PLATFORM.include?("darwin")
                      ["libext_host.dylib"]
                    else
                      ["libext_host.so"]
                    end.freeze

    # A policy for the extension: +decide+ gets the decoded JSON and returns truthy to allow.
    def self.policy(decide)
      return nil if decide.nil?
      raise ArgumentError, "a policy must respond to call" unless decide.respond_to?(:call)

      ->(json) { decide.call(JSON.parse(json)) ? true : false }
    end

    def self.encode(value)
      value.is_a?(String) ? value : JSON.generate(value)
    end

    # The C engine, loaded from +path+, +EXT_HOST_LIBRARY+, or libext_host on the loader path.
    # Raises LoadError when no library loads or its ABI is not this extension's. An engine is
    # never unloaded.
    class Engine
      def initialize(path = nil)
        path ||= ENV.fetch("EXT_HOST_LIBRARY", nil)
        if path && !path.empty?
          @engine = Native::Engine.open(path)
          return
        end
        error = nil
        LIBRARY_NAMES.each do |name|
          @engine = Native::Engine.open(name)
          return
        rescue LoadError => e
          error = e
        end
        raise LoadError, "the ext engine library was not found: set EXT_HOST_LIBRARY to libext_host, or pass its path (#{error&.message})"
      end

      # A host for a JSON-line guest started as +executable+ (an absolute path, no shell).
      # +environment+ is a list of NAME=value entries added to a scrubbed base of PATH; the
      # child's stderr is kept up to +stderr_bytes+ (Host#stderr). +verify+ sees the descriptor
      # before the child starts and +authorize+ each validated request; each returns truthy to
      # allow, and an exception denies. Neither may block or use this host. With a block, yields
      # the host and destroys it afterwards.
      def process(descriptor, executable, arguments: [], environment: [], working_directory: nil,
                  stderr_bytes: 4096, verify: nil, authorize: nil, &block)
        native = @engine.process(Host.encode(descriptor), executable.to_s, arguments.map(&:to_s),
                                 environment.map(&:to_s), working_directory&.to_s, stderr_bytes,
                                 Host.policy(verify), Host.policy(authorize))
        Host.open(native, &block)
      end

      # A host for a guest library that exports the C ABI of ext_plugin.h. +path+ is an absolute
      # path to a verified, revision-specific artifact; it is loaded when the host starts, after
      # +verify+, and is never unloaded.
      def shared_library(descriptor, path, verify: nil, authorize: nil, &block)
        native = @engine.shared_library(Host.encode(descriptor), path.to_s, Host.policy(verify), Host.policy(authorize))
        Host.open(native, &block)
      end
    end

    def self.open(native, &block)
      host = Session.new(native)
      return host unless block

      begin
        yield host
      ensure
        host.close
        host.destroy
      end
    end

    # One session with one plugin. Use Engine#process or Engine#shared_library.
    #
    # Calls may come from several threads, and run without the global VM lock; the engine
    # serializes them for a process or a shared library. An interrupt (Thread#raise, Thread#kill)
    # cancels the call in progress. +close+ aborts; +destroy+ frees the host once no call is
    # running, and garbage collection frees an unreachable one.
    class Session
      def initialize(native)
        @native = native
      end

      # Starts the guest and checks its handshake against the descriptor. Timeouts are seconds.
      def start(timeout = 3.0)
        @native.start(timeout)
        nil
      end

      # Calls an operation and returns its payload (nil when it has none). A domain error raises
      # RemoteError; a host failure raises HostError.
      def call(contract, operation, payload = nil, timeout: 3.0)
        request = { "contract" => { "name" => fetch(contract, "name"), "version" => fetch(contract, "version") },
                    "operation" => operation }
        request["payload"] = payload unless payload.nil?
        response = JSON.parse(@native.call_json(JSON.generate(request), timeout, operation.to_s))
        error = response["error"]
        if error
          raise RemoteError.new(error["code"].to_s, error["message"].to_s,
                                retry_after_ms: error["retryAfterMilliseconds"])
        end
        response["payload"]
      end

      # Sends a complete request envelope (a JSON String) and returns the response envelope.
      def invoke(request, timeout: 3.0)
        @native.invoke(request, timeout)
      end

      # Stops admission and waits for the calls in flight; a timeout reopens admission.
      def drain(timeout = 3.0)
        @native.drain(timeout)
        nil
      end

      # Aborts the session; calls in flight end with "closed". Harmless after destroy.
      def close
        @native.close
      end

      # "created", "ready", "draining", "closed" or "failed".
      def state
        @native.state
      end

      # What a process guest wrote to stderr, bounded and untrusted (a binary String).
      def stderr
        @native.stderr
      end

      # Frees the host. Once is enough; again is harmless.
      def destroy
        @native.destroy
      end

      def destroyed?
        @native.destroyed?
      end

      private

      def fetch(contract, key)
        contract.fetch(key) { contract.fetch(key.to_sym) }
      end
    end
  end
end
