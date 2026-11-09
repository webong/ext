<?php

declare(strict_types=1);

namespace ExtIO\Host;

/**
 * A host failure: the engine's status by name ($status: "denied", "closed" ...), its number
 * (getCode()) and what was being done ($action).
 */
final class HostError extends \RuntimeException
{
    public function __construct(int $code, public readonly string $status, public readonly string $action = '')
    {
        parent::__construct('ext host: ' . ($action !== '' ? "$action: " : '') . $status, $code);
    }

    public static function of(int $code, string $action = ''): self
    {
        return new self($code, Engine::STATUS_NAMES[$code] ?? 'unknown', $action);
    }
}
