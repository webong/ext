<?php

declare(strict_types=1);

namespace ExtIO\Plugin;

/**
 * Calls an operation the host offers (declared in the descriptor's "reverse" list) and waits for
 * the answer. A guest that reads inline makes one call at a time, from the handler.
 */
final class ReverseCaller
{
    /** Several reverse calls cannot be outstanding at once. */
    public readonly bool $concurrent;

    /** @internal set when the call it belongs to is over */
    public bool $over = false;

    /**
     * @internal
     * @param array<string, true> $declared
     */
    public function __construct(
        private readonly Link $link,
        private readonly array $declared,
        private readonly string $parent,
        private readonly float $deadline,
    ) {
        $this->concurrent = false;
    }

    /**
     * Calls $operation of $contract (a ContractRef or a [name, version] pair) with a JSON payload
     * and returns the host's payload. The host's refusal or error throws RemoteError with its code
     * ("reverse_undeclared" when the descriptor does not list it, "denied", "busy", "too_large",
     * "expired", ...); a local failure throws FrameError. $timeout seconds bound the deadline the
     * host is given, never past the call's own; the wait itself ends with the host's answer or the
     * end of the connection, since standard input cannot be read with a timeout.
     *
     * @param ContractRef|array{string, string} $contract
     */
    public function call(ContractRef|array $contract, string $operation, mixed $payload = null, ?float $timeout = null): mixed
    {
        [$name, $version] = $contract instanceof ContractRef ? [$contract->name, $contract->version] : array_values($contract);
        if (!isset($this->declared[Guest::key((string) $name, (string) $version, $operation)])) {
            throw new RemoteError('reverse_undeclared', 'reverse call refused');
        }
        if ($this->over) {
            throw new FrameError('closed', 'the call is over');
        }
        $deadline = $this->deadline;
        if ($timeout !== null) {
            $deadline = min($deadline, microtime(true) + max(0.0, $timeout));
        }
        return $this->link->reverse($this->parent, (string) $name, (string) $version, $operation, $payload, $deadline);
    }
}
