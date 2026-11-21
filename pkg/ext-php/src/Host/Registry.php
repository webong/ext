<?php

declare(strict_types=1);

namespace ExtIO\Host;

/**
 * A guest registry (ext_registry_* in ext_host.h): a reverse call one guest makes is served by a
 * forward call to another guest, along an edge declared with route(). An undeclared edge is
 * refused with reverse_undeclared, an unregistered target with reverse_unavailable, and a call that
 * would re-enter a host already on its call chain with denied; the target's answer passes through
 * unchanged. A host is a source once Host::attach() names the registry, and a target once
 * register() names a contract it serves; it leaves the registry when it is destroyed. A host keeps
 * the registries it uses alive, and the native registry is freed only once neither this object nor
 * any host can call into it. Failures throw HostError.
 */
final class Registry
{
    /** @internal */
    public readonly \FFI $ffi;
    /** @internal */
    public ?\FFI\CData $handle;
    /** @internal the hosts that use it, besides this object */
    public int $users = 0;
    private bool $dropped = false;
    /** @var array<string, \WeakReference<Host>> the host that serves each "name\0version" */
    private array $targets = [];

    public function __construct(Engine $engine)
    {
        $this->ffi = $engine->ffi;
        $handle = $this->ffi->new('ext_registry *');
        $status = $this->ffi->ext_registry_create(\FFI::addr($handle));
        if ($status !== 0) {
            throw HostError::of($status, 'registry');
        }
        $this->handle = $handle;
    }

    /**
     * Names $host (started, and listing the contract) as the one serving $contract.
     *
     * @param array{name: string, version: string}|\stdClass $contract
     */
    public function register(array|\stdClass $contract, Host $host): void
    {
        $contract = (array) $contract;
        $host->registerIn($this, (string) $contract['name'], (string) $contract['version']);
        $this->targets[$contract['name'] . "\0" . $contract['version']] = \WeakReference::create($host);
    }

    /**
     * Removes the contract and waits for the forward calls routed to its host.
     *
     * @param array{name: string, version: string}|\stdClass $contract
     */
    public function unregister(array|\stdClass $contract): void
    {
        $contract = (array) $contract;
        $name = (string) $contract['name'];
        $version = (string) $contract['version'];
        $status = $this->ffi->ext_registry_unregister($this->handle, $name, $version);
        if ($status !== 0) {
            throw HostError::of($status, 'unregister');
        }
        $host = ($this->targets[$name . "\0" . $version] ?? null)?->get();
        unset($this->targets[$name . "\0" . $version]);
        $host?->forget($this, $name, $version);
    }

    /**
     * A reverse call of $from ({name, version, operation}) is served by a call of $to.
     *
     * @param array{name: string, version: string, operation: string}|\stdClass $from
     * @param array{name: string, version: string, operation: string}|\stdClass $to
     */
    public function route(array|\stdClass $from, array|\stdClass $to): void
    {
        $from = (array) $from;
        $to = (array) $to;
        $keep = [];
        $edge = $this->ffi->new('ext_registry_edge');
        $edge->struct_size = \FFI::sizeof($edge);
        $fields = ['from_name' => $from['name'], 'from_version' => $from['version'], 'from_operation' => $from['operation'],
            'to_name' => $to['name'], 'to_version' => $to['version'], 'to_operation' => $to['operation']];
        foreach ($fields as $field => $value) {
            $c = $this->ffi->new('char[' . (\strlen((string) $value) + 1) . ']');
            \FFI::memcpy($c, (string) $value, \strlen((string) $value));
            $keep[] = $c;
            $edge->{$field} = $this->ffi->cast('char *', $c);
        }
        $status = $this->ffi->ext_registry_route($this->handle, \FFI::addr($edge));
        if ($status !== 0) {
            throw HostError::of($status, 'route');
        }
    }

    /** @internal a host stopped using it */
    public function release(): void
    {
        $this->users--;
        $this->freeIfUnused();
    }

    /** @internal a host left the contract; it is no longer its target */
    public function forgetTarget(string $name, string $version): void
    {
        unset($this->targets[$name . "\0" . $version]);
    }

    private function freeIfUnused(): void
    {
        if ($this->dropped && $this->users === 0 && $this->handle !== null) {
            $this->ffi->ext_registry_destroy($this->handle);
            $this->handle = null;
        }
    }

    public function __destruct()
    {
        $this->dropped = true;
        $this->freeIfUnused();
    }
}
