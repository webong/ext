<?php

declare(strict_types=1);

namespace ExtIO\Plugin;

/** @internal UTF-8 helpers that need neither mbstring nor iconv. */
final class Text
{
    // One well-formed UTF-8 character, or (group 1) a byte that does not start one.
    private const CHARACTER = '/[\x00-\x7F]|[\xC2-\xDF][\x80-\xBF]|\xE0[\xA0-\xBF][\x80-\xBF]'
        . '|[\xE1-\xEC\xEE\xEF][\x80-\xBF]{2}|\xED[\x80-\x9F][\x80-\xBF]|\xF0[\x90-\xBF][\x80-\xBF]{2}'
        . '|[\xF1-\xF3][\x80-\xBF]{3}|\xF4[\x80-\x8F][\x80-\xBF]{2}|([\x80-\xFF])/';

    public static function isUtf8(string $s): bool
    {
        return preg_match('//u', $s) === 1;
    }

    /** The length in bytes of UTF-8, or -1 when the string is not UTF-8. */
    public static function utf8Length(mixed $s): int
    {
        return \is_string($s) && self::isUtf8($s) ? \strlen($s) : -1;
    }

    /** The number of characters of a UTF-8 string. */
    public static function characters(string $s): int
    {
        return \strlen($s) - preg_match_all('/[\x80-\xBF]/', $s);
    }

    /** Replaces each byte that is not part of a UTF-8 character with U+FFFD. */
    public static function scrub(string $s): string
    {
        if (self::isUtf8($s)) {
            return $s;
        }
        return preg_replace_callback(self::CHARACTER, static fn (array $m): string => isset($m[1]) ? "\u{FFFD}" : $m[0], $s);
    }

    /** At most $limit bytes of $s, never ending inside a character. */
    public static function cut(string $s, int $limit): string
    {
        if (\strlen($s) <= $limit) {
            return $s;
        }
        $end = $limit;
        // Back off over continuation bytes to the start of the character the cut falls in.
        while ($end > 0 && (\ord($s[$end]) & 0xC0) === 0x80) {
            $end--;
        }
        return substr($s, 0, $end);
    }

    /** @return list<string> */
    public static function split(string $text): array
    {
        $text = self::scrub($text);
        $pieces = [];
        while ($text !== '') {
            $piece = self::cut($text, MAX_PROGRESS_TEXT);
            $pieces[] = $piece;
            $text = substr($text, \strlen($piece));
        }
        return $pieces;
    }
}
