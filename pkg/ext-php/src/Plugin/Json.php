<?php

declare(strict_types=1);

namespace ExtIO\Plugin;

/**
 * Strict JSON with the Go decoder's rules, and the encoding every frame uses.
 *
 * Decoding refuses what json_decode accepts silently (duplicate keys) and keeps what it loses:
 * a JSON object is a stdClass (so {} and [] stay apart), an array is a list, and an integer
 * literal outside the int64 range is a JsonInteger, so a uint64 count is judged exactly.
 *
 * @internal
 */
final class Json
{
    public const MAX_DEPTH = 64;

    private const ENCODE_FLAGS = \JSON_UNESCAPED_SLASHES | \JSON_UNESCAPED_UNICODE
        | \JSON_PRESERVE_ZERO_FRACTION | \JSON_THROW_ON_ERROR;

    private const CONTROL = "\x00\x01\x02\x03\x04\x05\x06\x07\x08\x09\x0a\x0b\x0c\x0d\x0e\x0f"
        . "\x10\x11\x12\x13\x14\x15\x16\x17\x18\x19\x1a\x1b\x1c\x1d\x1e\x1f";
    private const NUMBER = '/\G-?(?:0|[1-9][0-9]*+)(\.[0-9]++)?([eE][+-]?+[0-9]++)?/';

    private string $s;
    private int $i = 0;
    private int $n;

    private function __construct(string $s)
    {
        $this->s = $s;
        $this->n = \strlen($s);
    }

    /**
     * Decodes one frame (bytes without the line feed). Throws ProtocolError.
     */
    public static function decode(string $raw): mixed
    {
        if (\strlen($raw) > MAX_FRAME_BYTES) {
            throw new ProtocolError('frame too large');
        }
        if (!Text::isUtf8($raw)) {
            throw new ProtocolError('JSON must be UTF-8');
        }
        $p = new self($raw);
        $p->space();
        $value = $p->value(0);
        $p->space();
        if ($p->i !== $p->n) {
            throw new ProtocolError('malformed JSON: trailing data');
        }
        return $value;
    }

    /**
     * Encodes a value as one compact frame. Throws \JsonException for a value that is not JSON
     * (a resource, NAN or INF, a string that is not UTF-8, nesting beyond 512 levels).
     */
    public static function encode(mixed $value): string
    {
        JsonInteger::$encoded = false;
        $out = json_encode($value, self::ENCODE_FLAGS);
        if (JsonInteger::$encoded) {
            $out = JsonInteger::restore($out);
        }
        return $out;
    }

    /** The size of a value encoded without escaping characters, as the stream batch limit counts it. */
    public static function encodedSize(mixed $value): int
    {
        JsonInteger::$encoded = false;
        $out = json_encode($value, self::ENCODE_FLAGS | \JSON_UNESCAPED_LINE_TERMINATORS);
        if (JsonInteger::$encoded) {
            $out = JsonInteger::restore($out);
        }
        return \strlen($out);
    }

    /**
     * An array becomes the JSON value it encodes (the way a frame would carry it), so associative
     * arrays are objects and lists are arrays. Other values are returned unchanged.
     */
    public static function normalize(mixed $value): mixed
    {
        if (\is_array($value) || $value instanceof \stdClass) {
            try {
                return self::decode(self::encode($value));
            } catch (\JsonException | ProtocolError $e) {
                throw new \InvalidArgumentException('value is not JSON: ' . $e->getMessage(), 0, $e);
            }
        }
        return $value;
    }

    // --- reading decoded values -----------------------------------------------------------

    /** Whether $o is an object whose members are all among $fields. */
    public static function strict(mixed $o, array $fields): bool
    {
        if (!$o instanceof \stdClass) {
            return false;
        }
        $allowed = array_flip($fields);
        foreach (get_object_vars($o) as $key => $_) {
            if (!isset($allowed[(string) $key])) {
                return false;
            }
        }
        return true;
    }

    /** Whether $o is an object with exactly the members $fields. */
    public static function exactly(mixed $o, array $fields): bool
    {
        if (!$o instanceof \stdClass) {
            return false;
        }
        $keys = array_map('strval', array_keys(get_object_vars($o)));
        sort($keys);
        sort($fields);
        return $keys === $fields;
    }

    public static function has(\stdClass $o, string $key): bool
    {
        return property_exists($o, $key);
    }

    /** A member, or $default when it is absent (a member that is null is null). */
    public static function get(\stdClass $o, string $key, mixed $default = null): mixed
    {
        return property_exists($o, $key) ? $o->{$key} : $default;
    }

    // --- the parser -------------------------------------------------------------------------

    private function space(): void
    {
        $this->i += strspn($this->s, " \t\n\r", $this->i);
    }

    private function fail(string $why): never
    {
        throw new ProtocolError('malformed JSON: ' . $why);
    }

    private function value(int $depth): mixed
    {
        if ($depth > self::MAX_DEPTH) {
            throw new ProtocolError('JSON nesting exceeds 64 levels');
        }
        if ($this->i >= $this->n) {
            $this->fail('unexpected end');
        }
        $c = $this->s[$this->i];
        switch ($c) {
            case '{':
                return $this->object($depth);
            case '[':
                return $this->list($depth);
            case '"':
                return $this->string();
            case 't':
                return $this->literal('true', true);
            case 'f':
                return $this->literal('false', false);
            case 'n':
                return $this->literal('null', null);
        }
        if ($c === '-' || ($c >= '0' && $c <= '9')) {
            return $this->number();
        }
        $this->fail('unexpected character');
    }

    private function literal(string $word, mixed $value): mixed
    {
        if (substr_compare($this->s, $word, $this->i, \strlen($word)) !== 0) {
            $this->fail('invalid literal');
        }
        $this->i += \strlen($word);
        return $value;
    }

    private function object(int $depth): \stdClass
    {
        $this->i++;
        $members = [];
        $this->space();
        if ($this->i < $this->n && $this->s[$this->i] === '}') {
            $this->i++;
            return new \stdClass();
        }
        while (true) {
            if ($this->i >= $this->n || $this->s[$this->i] !== '"') {
                $this->fail('expected a key');
            }
            $key = $this->string();
            if (\array_key_exists($key, $members)) {
                throw new ProtocolError('duplicate JSON key');
            }
            if ($key !== '' && $key[0] === "\0") {
                // A PHP object cannot hold a property whose name starts with NUL.
                throw new ProtocolError('unsupported JSON key');
            }
            $this->space();
            if ($this->i >= $this->n || $this->s[$this->i] !== ':') {
                $this->fail('expected a colon');
            }
            $this->i++;
            $this->space();
            $members[$key] = $this->value($depth + 1);
            $this->space();
            if ($this->i >= $this->n) {
                $this->fail('unexpected end');
            }
            $c = $this->s[$this->i++];
            if ($c === '}') {
                return (object) $members;
            }
            if ($c !== ',') {
                $this->fail('expected a comma');
            }
            $this->space();
        }
    }

    private function list(int $depth): array
    {
        $this->i++;
        $items = [];
        $this->space();
        if ($this->i < $this->n && $this->s[$this->i] === ']') {
            $this->i++;
            return $items;
        }
        while (true) {
            $items[] = $this->value($depth + 1);
            $this->space();
            if ($this->i >= $this->n) {
                $this->fail('unexpected end');
            }
            $c = $this->s[$this->i++];
            if ($c === ']') {
                return $items;
            }
            if ($c !== ',') {
                $this->fail('expected a comma');
            }
            $this->space();
        }
    }

    private function string(): string
    {
        // Find the closing quote, stepping over escapes, with no regular expression (whose
        // backtracking limit a long string of escapes would reach).
        $start = $this->i;
        $j = $start + 1;
        $escaped = false;
        while (true) {
            $j += strcspn($this->s, '"\\', $j);
            if ($j >= $this->n) {
                $this->fail('unterminated string');
            }
            if ($this->s[$j] === '"') {
                break;
            }
            $escaped = true;
            $j += 2;
        }
        $this->i = $j + 1;
        if (!$escaped) {
            $inner = substr($this->s, $start + 1, $j - $start - 1);
            if (strcspn($inner, self::CONTROL) !== \strlen($inner)) {
                $this->fail('control character in string');
            }
            return $inner;
        }
        // json_decode checks the escapes and control characters and refuses an unpaired surrogate.
        try {
            return json_decode(substr($this->s, $start, $j - $start + 1), false, 1, \JSON_THROW_ON_ERROR);
        } catch (\JsonException $e) {
            throw new ProtocolError($e->getCode() === \JSON_ERROR_UTF16 ? 'unpaired surrogate escape' : 'malformed JSON: invalid string');
        }
    }

    private function number(): int|float|JsonInteger
    {
        if (preg_match(self::NUMBER, $this->s, $m, 0, $this->i) !== 1) {
            $this->fail('invalid number');
        }
        $text = $m[0];
        $this->i += \strlen($text);
        if (($m[1] ?? '') === '' && ($m[2] ?? '') === '') {
            $int = (int) $text;
            if ((string) $int === $text || $text === '-0') {
                return $int;
            }
            return new JsonInteger($text);
        }
        return (float) $text;
    }
}
