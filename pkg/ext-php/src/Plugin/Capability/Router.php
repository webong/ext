<?php

declare(strict_types=1);

namespace ExtIO\Plugin\Capability;

use ExtIO\Plugin\RemoteError;

/**
 * A handler for serveStdio() that dispatches (contract, operation) to registered functions,
 * validating the payload before the call and the result after it. A payload that fails validation
 * is an "invalid_input" remote error; a result that fails is an internal error, because it is the
 * plugin's own bug.
 *
 *     $router = (new Router())->health(fn () => ['status' => 'healthy', 'ready' => true]);
 *     ExtIO\Plugin\serveStdio('plugin.json', $router);
 */
final class Router
{
    /** @var array<string, array{\Closure, ?\Closure, ?\Closure}> */
    private array $routes = [];

    public function on(string $contract, string $version, string $operation, callable $function, ?callable $checkInput = null, ?callable $checkOutput = null): self
    {
        $key = $contract . "\0" . $version . "\0" . $operation;
        if (isset($this->routes[$key])) {
            throw new \InvalidArgumentException('operation already registered');
        }
        $this->routes[$key] = [
            \Closure::fromCallable($function),
            $checkInput === null ? null : \Closure::fromCallable($checkInput),
            $checkOutput === null ? null : \Closure::fromCallable($checkOutput),
        ];
        return $this;
    }

    /** $check() returns ['status' => ..., 'ready' => ...]. */
    public function health(callable $check): self
    {
        return $this->on('ext.health', 'v1', 'check', static fn ($_) => $check(), Shape::healthRequest(...), Shape::health(...));
    }

    /** $update($instance, $revision, $values) returns nothing; the router answers. */
    public function configuration(callable $update): self
    {
        $run = static function (\stdClass $p) use ($update): array {
            $update($p->instance, $p->revision, $p->values);
            return ['instance' => $p->instance, 'revision' => $p->revision];
        };
        return $this->on('ext.configuration', 'v1', 'update', $run, Shape::configuration(...), Shape::configured(...));
    }

    /** $open($parameters) returns an id; $read($id, $sequence, $limit) returns ['items' => ..., 'done' => ...]; $close($id). */
    public function stream(callable $open, callable $read, callable $close): self
    {
        $this->on('ext.stream', 'v1', 'open', static fn (\stdClass $p) => ['id' => $open($p->parameters)], Shape::streamOpen(...), Shape::streamOpened(...));
        $this->on('ext.stream', 'v1', 'read', static fn (\stdClass $p) => $read($p->id, $p->sequence, $p->limit), Shape::streamRead(...), Shape::streamBatch(...));
        return $this->on('ext.stream', 'v1', 'close', static function (\stdClass $p) use ($close) {
            $close($p->id);
            return new \stdClass();
        }, Shape::streamClose(...), static fn ($v) => Shape::object($v, []));
    }

    /**
     * @param string|array{string, string} $contract a name (version v1) or [name, version]
     */
    public function __invoke(string|array $contract, string $operation, mixed $payload): mixed
    {
        [$name, $version] = \is_array($contract) ? array_values($contract) : [$contract, 'v1'];
        $entry = $this->routes[$name . "\0" . $version . "\0" . $operation] ?? null;
        if ($entry === null) {
            throw new RemoteError('unsupported', 'operation is not implemented');
        }
        [$function, $checkInput, $checkOutput] = $entry;
        if ($checkInput !== null) {
            try {
                $payload = $checkInput($payload);
            } catch (\InvalidArgumentException) {
                throw new RemoteError('invalid_input', 'request payload does not satisfy operation contract');
            }
        }
        $result = $function($payload);
        if ($checkOutput !== null) {
            // An InvalidArgumentException here is the plugin's bug and surfaces as an internal error.
            $result = $checkOutput($result);
        }
        return $result;
    }
}
