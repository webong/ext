<?php

declare(strict_types=1);

namespace ExtIO\Plugin;

/** @internal RFC 3339 deadlines as seconds since the epoch. */
final class Time
{
    private const PATTERN = '/\A([0-9]{4})-([0-9]{2})-([0-9]{2})[Tt]([0-9]{2}):([0-9]{2}):([0-9]{2})(\.[0-9]+)?([Zz]|[+-][0-9]{2}:[0-9]{2})\z/';

    // Go's zero time, which means "no deadline given".
    private const ZERO = -62135596800.0;

    /** Seconds since the epoch, or null when the value is not a deadline or is the zero time. */
    public static function parse(mixed $value): ?float
    {
        if (!\is_string($value) || preg_match(self::PATTERN, $value, $m) !== 1) {
            return null;
        }
        [$year, $month, $day, $hour, $minute, $second] = array_map('intval', \array_slice($m, 1, 6));
        $micro = $m[7] !== '' ? (int) substr(substr($m[7], 1) . '000000', 0, 6) : 0;
        if ($year < 1 || $month < 1 || $month > 12 || $day < 1 || $day > self::daysIn($year, $month)
            || $hour > 23 || $minute > 59 || $second > 59) {
            return null;
        }
        $offset = 0;
        $tz = $m[8];
        if ($tz !== 'Z' && $tz !== 'z') {
            $offset = ((int) substr($tz, 1, 2)) * 3600 + ((int) substr($tz, 4, 2)) * 60;
            if ($offset >= 86400) {
                return null;
            }
            if ($tz[0] === '-') {
                $offset = -$offset;
            }
        }
        $seconds = self::days($year, $month, $day) * 86400 + $hour * 3600 + $minute * 60 + $second - $offset;
        // Whole microseconds, then seconds, as Python's datetime.timestamp() computes them.
        $stamp = ($seconds * 1000000 + $micro) / 1e6;
        if ($stamp === self::ZERO) {
            return null;
        }
        return $stamp;
    }

    /** Milliseconds, rounded down, so the instant is never after the call's own. */
    public static function format(float $seconds): string
    {
        $ms = (int) floor($seconds * 1000);
        $whole = (int) floor($ms / 1000);
        return gmdate('Y-m-d\TH:i:s', $whole) . sprintf('.%03dZ', $ms - $whole * 1000);
    }

    private static function daysIn(int $year, int $month): int
    {
        if ($month === 2) {
            return ($year % 4 === 0 && $year % 100 !== 0) || $year % 400 === 0 ? 29 : 28;
        }
        return \in_array($month, [4, 6, 9, 11], true) ? 30 : 31;
    }

    // Days since 1970-01-01 of a proleptic Gregorian date (Howard Hinnant's days_from_civil).
    private static function days(int $y, int $m, int $d): int
    {
        $y -= $m <= 2 ? 1 : 0;
        $era = intdiv($y >= 0 ? $y : $y - 399, 400);
        $yoe = $y - $era * 400;
        $doy = intdiv(153 * ($m + ($m > 2 ? -3 : 9)) + 2, 5) + $d - 1;
        $doe = $yoe * 365 + intdiv($yoe, 4) - intdiv($yoe, 100) + $doy;
        return $era * 146097 + $doe - 719468;
    }
}
