<?php

declare(strict_types=1);

namespace ExtIO\Plugin;

/** @internal The identifier and revision syntax of ext.plugin/v1. */
final class Pattern
{
    private const IDENTIFIER = '/\A[a-z0-9][a-z0-9._\/-]{0,255}\z/';
    private const REVISION = '/\A[A-Za-z0-9][A-Za-z0-9._:+\/-]{0,255}\z/';

    public static function identifier(mixed $value): bool
    {
        return \is_string($value) && preg_match(self::IDENTIFIER, $value) === 1;
    }

    public static function revision(mixed $value): bool
    {
        return \is_string($value) && preg_match(self::REVISION, $value) === 1;
    }
}
