<?php

declare(strict_types=1);

namespace ExtIO\Plugin;

/** A contract by name and exact version, as ReverseCaller::call() names the host's. */
final class ContractRef
{
    public function __construct(
        public readonly string $name,
        public readonly string $version,
    ) {
    }
}
