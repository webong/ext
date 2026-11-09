<?php

declare(strict_types=1);

namespace ExtIO\Host;

use ExtIO\Plugin\Json;
use ExtIO\Plugin\RemoteError;

/**
 * One session with one plugin. Use Engine::process() or Engine::sharedLibrary().
 *
 * The engine runs the policies on the thread making the call, which is PHP's, inside the FFI call
 * to start() or call(); a policy that uses the host it decides for is refused with HostError
 * "invalid". close() aborts; destroy() (or the object going away) frees the host.
 */
final class Host
{
    private const STDERR_CAPACITY = 65536;

    private readonly \FFI $ffi;
    private ?\FFI\CData $handle = null;
    private bool $inPolicy = false;
    /** @var list<\Closure> the FFI callbacks, alive as long as the host */
    private array $callbacks = [];

    /** @internal Use Engine::process() or Engine::sharedLibrary(). */
    public function __construct(
        private readonly Engine $engine,
        string $descriptor,
        \FFI\CData $backend,
        ?callable $verify,
        ?callable $authorize,
        ?\FFI\CData $extension,
    ) {
        $this->ffi = $engine->ffi;
        $this->callbacks = [$this->policy($verify), $this->policy($authorize)];
        $options = $this->ffi->new('ext_host_options');
        $options->abi_version = Engine::ABI_VERSION;
        $options->struct_size = \FFI::sizeof($options);
        $copy = $this->ffi->new('char[' . max(\strlen($descriptor), 1) . ']');
        \FFI::memcpy($copy, $descriptor, \strlen($descriptor));
        $options->descriptor = $this->ffi->cast('char *', $copy);
        $options->descriptor_len = \strlen($descriptor);
        $options->verify = $this->callbacks[0];
        $options->authorize = $this->callbacks[1];
        $options->user = null;
        $handle = $this->ffi->new('ext_host *');
        $status = $this->ffi->ext_host_create(\FFI::addr($options), \FFI::addr($backend), \FFI::addr($handle));
        if ($status !== 0) {
            if ($extension !== null) {
                ($extension->release)($extension->user); // ownership stays with the caller on a failed create
            }
            throw HostError::of($status, 'create');
        }
        $this->handle = $handle;
    }

    /** A C policy around $decide(decoded JSON): a truthy result allows; a throw or anything else denies. */
    private function policy(?callable $decide): \Closure
    {
        if ($decide === null) {
            return static fn ($user, $json, $len): int => 0;
        }
        return function ($user, $json, $len) use ($decide): int {
            $this->inPolicy = true;
            try {
                return $decide(Json::decode(\FFI::string($json, $len))) ? 0 : 1;
            } catch (\Throwable) {
                return 1; // never unwind into the engine
            } finally {
                $this->inPolicy = false;
            }
        };
    }

    private function live(string $action): \FFI\CData
    {
        if ($this->handle === null) {
            throw HostError::of(5, 'destroyed');
        }
        if ($this->inPolicy) {
            throw HostError::of(1, $action); // a policy may not reenter its host
        }
        return $this->handle;
    }

    private function options(float $timeout): \FFI\CData
    {
        $ms = $timeout * 1000.0;
        if (!($ms >= 1.0)) {
            throw new \InvalidArgumentException('timeout must be at least 1 ms');
        }
        $o = $this->ffi->new('ext_call_options');
        $o->struct_size = \FFI::sizeof($o);
        $o->timeout_ms = $ms >= 4294967295.0 ? 4294967295 : (int) $ms;
        return $o;
    }

    /** Starts the guest and checks its handshake against the descriptor. Timeouts are seconds. */
    public function start(float $timeout = 3.0): void
    {
        $handle = $this->live('start');
        $o = $this->options($timeout);
        $status = $this->ffi->ext_host_start_with_options($handle, \FFI::addr($o));
        if ($status !== 0) {
            throw HostError::of($status, 'start');
        }
    }

    private function exchange(string $function, string $data, float $timeout, string $action): string
    {
        $handle = $this->live($action);
        $o = $this->options($timeout);
        $out = $this->ffi->new('ext_buffer');
        $status = $this->ffi->{$function}($handle, $data, \strlen($data), \FFI::addr($o), \FFI::addr($out));
        try {
            if ($status !== 0) {
                throw HostError::of($status, $action);
            }
            return $out->data === null ? '' : \FFI::string($out->data, $out->len);
        } finally {
            if ($out->data !== null) {
                $this->ffi->ext_buffer_free(\FFI::addr($out));
            }
        }
    }

    /**
     * Calls an operation and returns its payload as the guest SDK decodes JSON (objects are
     * \stdClass; null when it has none). A domain error throws RemoteError; a host failure throws
     * HostError.
     *
     * @param array{name: string, version: string}|\stdClass $contract
     */
    public function call(array|\stdClass $contract, string $operation, mixed $payload = null, float $timeout = 3.0): mixed
    {
        $contract = (array) $contract;
        $request = ['contract' => ['name' => $contract['name'], 'version' => $contract['version']], 'operation' => $operation];
        if ($payload !== null) {
            $request['payload'] = $payload;
        }
        $response = Json::decode($this->exchange('ext_host_call', Json::encode($request), $timeout, $operation));
        $error = Json::get($response, 'error');
        if ($error !== null) {
            $retry = Json::get($error, 'retryAfterMilliseconds');
            throw new RemoteError(Json::get($error, 'code'), Json::get($error, 'message'), $retry === null ? null : (int) $retry);
        }
        return Json::get($response, 'payload');
    }

    /** Sends a complete request envelope (JSON) and returns the response envelope (JSON). */
    public function invoke(string $request, float $timeout = 3.0): string
    {
        return $this->exchange('ext_host_invoke_with_options', $request, $timeout, 'invoke');
    }

    /** Stops admission and waits for the calls in flight; a timeout reopens admission. */
    public function drain(float $timeout = 3.0): void
    {
        $handle = $this->live('drain');
        $o = $this->options($timeout);
        $status = $this->ffi->ext_host_drain_with_options($handle, \FFI::addr($o));
        if ($status !== 0) {
            throw HostError::of($status, 'drain');
        }
    }

    /** Aborts the session; calls in flight end with "closed". Harmless after destroy(). */
    public function close(): void
    {
        if ($this->inPolicy) {
            throw HostError::of(1, 'close');
        }
        if ($this->handle !== null) {
            $this->ffi->ext_host_close($this->handle);
        }
    }

    /** "created", "ready", "draining", "closed" or "failed". */
    public function state(): string
    {
        return Engine::STATES[$this->ffi->ext_host_get_state($this->live('state'))] ?? 'unknown';
    }

    /** What a process guest wrote to stderr, bounded and untrusted. */
    public function stderr(): string
    {
        $handle = $this->live('stderr');
        $buffer = $this->ffi->new('uint8_t[' . self::STDERR_CAPACITY . ']');
        $length = $this->ffi->new('size_t');
        $status = $this->ffi->ext_host_process_stderr($handle, $buffer, self::STDERR_CAPACITY, \FFI::addr($length));
        if ($status !== 0) {
            throw HostError::of($status, 'stderr');
        }
        return \FFI::string($buffer, min($length->cdata, self::STDERR_CAPACITY));
    }

    /** Frees the host. Once is enough; again is harmless. */
    public function destroy(): void
    {
        if ($this->inPolicy) {
            throw HostError::of(1, 'destroy');
        }
        if ($this->handle !== null) {
            $this->ffi->ext_host_close($this->handle);
            $this->ffi->ext_host_destroy($this->handle);
            $this->handle = null;
        }
    }

    public function destroyed(): bool
    {
        return $this->handle === null;
    }

    public function __destruct()
    {
        if (!$this->inPolicy) {
            $this->destroy();
        }
    }
}
