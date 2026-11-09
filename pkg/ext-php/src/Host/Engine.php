<?php

declare(strict_types=1);

namespace ExtIO\Host;

use ExtIO\Plugin\Json;

/**
 * The C engine (lib/plugin), loaded through PHP FFI from a path, EXT_HOST_LIBRARY, or
 * libext_host on the loader path.
 *
 *     $engine = new ExtIO\Host\Engine();
 *     $host = $engine->process($descriptor, '/opt/plugins/echo/1.0.0/echo');
 *     $host->start();
 *     $host->call(['name' => 'ext.echo', 'version' => 'v1'], 'echo', ['value' => 7]);
 *     $host->destroy();
 *
 * The engine does the work a host must get right: admission, the handshake check against the
 * descriptor, request and response validation, deadlines and the session's lifecycle. This class
 * only moves bytes and policy decisions across FFI, so it needs the FFI extension (enabled for
 * the CLI by default; ffi.enable=true elsewhere) and the engine's shared library. Autoloading
 * ExtIO\Plugin (the guest) never loads it. An engine is never unloaded.
 */
final class Engine
{
    public const ABI_VERSION = 2;
    public const STATUS_NAMES = [
        'ok', 'invalid', 'denied', 'mismatch', 'unsupported', 'closed', 'timeout', 'io', 'nomem',
        'draining', 'not_found', 'ambiguous', 'canceled', 'updating', 'capacity', 'sequence',
        'protocol', 'expired', 'exists', 'missing',
    ];
    public const STATES = ['created', 'ready', 'draining', 'closed', 'failed'];

    private const BACKEND_EXTENSION_CONTEXT = 3;
    private const BACKEND_PROCESS_LAUNCH = 5;

    /**
     * The declarations of ext_host.h this class uses. FFI parses no preprocessor directives, so
     * they are restated here. Byte inputs are declared const char * (the same ABI as
     * const uint8_t *) so PHP strings pass directly; a policy's JSON stays const uint8_t *, since
     * FFI would turn a const char * callback argument into a string cut at its first NUL. The ABI version check below and the shared
     * host cases hold them to the header.
     */
    private const DECLARATIONS = <<<'C'
        typedef int32_t ext_status;
        typedef struct ext_host ext_host;
        typedef struct { uint8_t *data; size_t len; } ext_buffer;
        typedef int32_t (*ext_policy)(void *user, const uint8_t *json, size_t len);
        typedef struct {
          uint32_t abi_version; uint32_t struct_size; const char *descriptor; size_t descriptor_len;
          ext_policy verify; ext_policy authorize; void *user;
        } ext_host_options;
        typedef struct { uint32_t struct_size; uint32_t kind; const void *config; size_t config_size; } ext_backend_options;
        typedef struct {
          uint32_t struct_size; const char *executable; const char *const *arguments; size_t argument_count;
          const char *const *environment; size_t environment_count; const char *working_directory;
          uint32_t stderr_bytes;
        } ext_jsonline_process_launch;
        typedef struct { uint32_t struct_size; uint32_t timeout_ms; const void *cancel; void *value; } ext_call_options;
        typedef struct {
          uint32_t struct_size; uint32_t flags; void *user; void *connect; void *invoke; void *close;
          void (*release)(void *);
        } ext_backend_extension_context;
        typedef struct { uint32_t struct_size; const char *path; } ext_shared_library_options;
        uint32_t ext_host_abi_version(void);
        ext_status ext_host_create(const ext_host_options *, const ext_backend_options *, ext_host **out);
        ext_status ext_host_start_with_options(ext_host *, const ext_call_options *);
        ext_status ext_host_call(ext_host *, const char *, size_t, const ext_call_options *, ext_buffer *);
        ext_status ext_host_invoke_with_options(ext_host *, const char *, size_t, const ext_call_options *, ext_buffer *);
        ext_status ext_host_drain_with_options(ext_host *, const ext_call_options *);
        void ext_host_close(ext_host *);
        void ext_host_destroy(ext_host *);
        int ext_host_get_state(ext_host *);
        ext_status ext_host_process_stderr(ext_host *, uint8_t *buffer, size_t capacity, size_t *length);
        void ext_buffer_free(ext_buffer *);
        ext_status ext_shared_library_backend_create(const ext_shared_library_options *, ext_backend_extension_context *);
        C;

    /** @internal */
    public readonly \FFI $ffi;

    /** Loads the engine; throws \FFI\Exception when no library loads, or its ABI is not 2. */
    public function __construct(?string $path = null)
    {
        if (!\extension_loaded('ffi')) {
            throw new \FFI\Exception('the FFI extension is not loaded');
        }
        $path ??= (string) getenv('EXT_HOST_LIBRARY');
        $candidates = $path !== '' ? [$path] : match (PHP_OS_FAMILY) {
            'Windows' => ['ext_host.dll'],
            'Darwin' => ['libext_host.dylib'],
            default => ['libext_host.so'],
        };
        $last = null;
        foreach ($candidates as $candidate) {
            try {
                $this->ffi = \FFI::cdef(self::DECLARATIONS, $candidate);
                $last = null;
                break;
            } catch (\FFI\Exception $e) {
                $last = $e;
            }
        }
        if ($last !== null) {
            throw new \FFI\Exception('the ext engine library was not found: set EXT_HOST_LIBRARY to libext_host, or pass its path (' . $last->getMessage() . ')', 0, $last);
        }
        $abi = $this->ffi->ext_host_abi_version();
        if ($abi !== self::ABI_VERSION) {
            throw new \FFI\Exception("ext engine ABI $abi, this class needs " . self::ABI_VERSION);
        }
    }

    /**
     * A host for a JSON-line guest started as $executable (an absolute path, no shell).
     * $environment entries are NAME=value, added to a scrubbed base of PATH; the child's stderr
     * is kept up to $stderrBytes (Host::stderr()). $verify sees the descriptor before the child
     * starts and $authorize each validated request: each receives the decoded JSON and returns
     * true to allow; null allows, and a throw denies. Neither may block or use this host.
     *
     * @param array<mixed>|\stdClass|string $descriptor
     * @param list<string> $arguments
     * @param list<string> $environment
     */
    public function process(
        array|\stdClass|string $descriptor,
        string $executable,
        array $arguments = [],
        array $environment = [],
        ?string $workingDirectory = null,
        int $stderrBytes = 4096,
        ?callable $verify = null,
        ?callable $authorize = null,
    ): Host {
        if ($stderrBytes < 0 || $stderrBytes > 0xFFFFFFFF) {
            throw new \InvalidArgumentException('stderrBytes is out of range');
        }
        $keep = [];
        $launch = $this->ffi->new('ext_jsonline_process_launch');
        $launch->struct_size = \FFI::sizeof($launch);
        $launch->executable = $this->cString($executable, $keep);
        [$launch->arguments, $launch->argument_count] = $this->cStrings($arguments, $keep);
        [$launch->environment, $launch->environment_count] = $this->cStrings($environment, $keep);
        $launch->working_directory = $workingDirectory === null ? null : $this->cString($workingDirectory, $keep);
        $launch->stderr_bytes = $stderrBytes;
        $backend = $this->backend(self::BACKEND_PROCESS_LAUNCH, $launch);
        return new Host($this, self::encode($descriptor), $backend, $verify, $authorize, null);
    }

    /**
     * A host for a guest library that exports the C ABI of ext_plugin.h. $path is an absolute
     * path to a verified, revision-specific artifact; it is loaded when the host starts, after
     * $verify, and is never unloaded. Its native code runs in this process with its full access.
     *
     * @param array<mixed>|\stdClass|string $descriptor
     */
    public function sharedLibrary(array|\stdClass|string $descriptor, string $path, ?callable $verify = null, ?callable $authorize = null): Host
    {
        $keep = [];
        $options = $this->ffi->new('ext_shared_library_options');
        $options->struct_size = \FFI::sizeof($options);
        $options->path = $this->cString($path, $keep);
        $ext = $this->ffi->new('ext_backend_extension_context');
        $status = $this->ffi->ext_shared_library_backend_create(\FFI::addr($options), \FFI::addr($ext));
        if ($status !== 0) {
            throw HostError::of($status, 'shared library');
        }
        $backend = $this->backend(self::BACKEND_EXTENSION_CONTEXT, $ext);
        return new Host($this, self::encode($descriptor), $backend, $verify, $authorize, $ext);
    }

    private function backend(int $kind, \FFI\CData $config): \FFI\CData
    {
        $backend = $this->ffi->new('ext_backend_options');
        $backend->struct_size = \FFI::sizeof($backend);
        $backend->kind = $kind;
        $backend->config = \FFI::addr($config);
        $backend->config_size = \FFI::sizeof($config);
        return $backend; // $config stays alive in the caller until the host has copied it
    }

    /** A NUL-terminated copy of $s, kept alive by $keep. */
    private function cString(string $s, array &$keep): \FFI\CData
    {
        if (str_contains($s, "\0")) {
            throw new \InvalidArgumentException('a string passed to the engine may not contain NUL');
        }
        $c = $this->ffi->new('char[' . (\strlen($s) + 1) . ']');
        \FFI::memcpy($c, $s, \strlen($s));
        $keep[] = $c;
        return $this->ffi->cast('char *', $c);
    }

    /**
     * @param list<string> $items
     * @return array{0: ?\FFI\CData, 1: int}
     */
    private function cStrings(array $items, array &$keep): array
    {
        $items = array_values($items);
        if ($items === []) {
            return [null, 0];
        }
        $array = $this->ffi->new('const char *[' . \count($items) . ']');
        foreach ($items as $i => $item) {
            $array[$i] = $this->cString((string) $item, $keep);
        }
        $keep[] = $array;
        return [$this->ffi->cast('const char *const *', $array), \count($items)];
    }

    /** @param array<mixed>|\stdClass|string $value */
    private static function encode(array|\stdClass|string $value): string
    {
        return \is_string($value) ? $value : Json::encode($value);
    }
}
