<?php

declare(strict_types=1);

namespace ExtIO\Plugin;

/**
 * An integer literal outside the int64 range, kept exactly as its digits.
 *
 * A decoded payload holds one where json_decode would have lost precision to a float. It encodes
 * back to the same digits, so a guest that echoes a payload returns it unchanged.
 */
final class JsonInteger implements \JsonSerializable, \Stringable
{
    private const MARK = "\u{F8FF}ext-io-int:";

    /** @internal set when an encoding wrote a marker that Json::encode replaces. */
    public static bool $encoded = false;

    public function __construct(public readonly string $digits)
    {
        if (preg_match('/\A-?(?:0|[1-9][0-9]*)\z/', $digits) !== 1) {
            throw new \InvalidArgumentException('not an integer literal');
        }
    }

    public function negative(): bool
    {
        return $this->digits[0] === '-';
    }

    public function __toString(): string
    {
        return $this->digits;
    }

    public function jsonSerialize(): string
    {
        self::$encoded = true;
        return self::MARK . $this->digits . "\u{F8FF}";
    }

    /** @internal */
    public static function restore(string $json): string
    {
        return preg_replace('/"\x{F8FF}ext-io-int:(-?[0-9]+)\x{F8FF}"/u', '$1', $json);
    }

    /**
     * Compares two non-negative integers, either of which may be a JsonInteger: -1, 0 or 1.
     */
    public static function compare(int|self $a, int|self $b): int
    {
        $x = (string) $a;
        $y = (string) $b;
        return \strlen($x) <=> \strlen($y) ?: strcmp($x, $y) <=> 0;
    }
}
