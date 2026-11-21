<?php

declare(strict_types=1);

namespace ExtIO\Host;

/**
 * One reverse request a plugin made, for the hook that serves it: $request is the Request as the
 * guest SDK decodes JSON (apiVersion, id, plugin, contract, operation, surface, deadline, payload
 * and, for a call-scoped request, parent), $remaining the seconds left to it.
 */
final class ReverseCall
{
    public function __construct(public readonly mixed $request, public readonly float $remaining)
    {
    }
}
