<?php

declare(strict_types=1);

namespace ExtIO\Host;

use ExtIO\Plugin\Json;

/** A cooperating writer backed by the C package_writer object. Close explicitly
 * in finally, or use Engine::withPackageWriter(). Store/trust APIs do not acquire
 * it implicitly. A stale lock is never stolen; another owner's replacement is
 * never removed. The engine library must provide the described object. */
final class PackageWriter
{
    private readonly \FFI $ffi;
    private ?\FFI\CData $handle = null;

    public function __construct(Engine $engine, string $root)
    {
        $this->ffi = $engine->ffi;
        $config = Json::encode(['object' => 'package_writer', 'root' => $root]);
        $handle = $this->ffi->new('uint64_t');
        $status = $this->ffi->ext_plugin_open($config, strlen($config), \FFI::addr($handle));
        if ($status !== 0) {
            throw HostError::of($status, 'package writer acquire');
        }
        $this->handle = $handle;
    }

    /** Idempotent. A failed release still consumes the native handle. */
    public function close(): void
    {
        if ($this->handle === null) {
            return;
        }
        $handle = $this->handle;
        $this->handle = null;
        $status = $this->ffi->ext_plugin_close($handle->cdata);
        if ($status !== 0) {
            throw HostError::of($status, 'package writer release');
        }
    }

    private function __clone()
    {
    }

    public function __destruct()
    {
        try {
            $this->close();
        } catch (\Throwable) {
            // Destructors cannot report failures reliably. Explicit close does.
        }
    }
}
