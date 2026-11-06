<?php

declare(strict_types=1);

namespace ExtIO\Plugin;

/**
 * How progress and reverse-call frames are judged (docs/plugin-guest-frames.md), with the rules
 * of the Go contract pinned by lib/plugin/testdata/v1/frame-cases.json. Each judge takes a
 * decoded frame and throws ProtocolError when the frame breaks the rules.
 */
final class Frames
{
    private const PROGRESS_FIELDS = ['frame', 'apiVersion', 'parent', 'kind', 'stream', 'text', 'completed', 'total', 'dropped'];
    private const REVERSE_REQUEST_FIELDS = ['frame', 'apiVersion', 'id', 'parent', 'contract', 'operation', 'deadline', 'payload'];
    private const REVERSE_RESPONSE_FIELDS = ['frame', 'apiVersion', 'id', 'payload', 'error'];

    /**
     * Judges a progress frame: returns its parent (the call it belongs to) and the event as
     * [kind, stream, text, completed, total, dropped].
     *
     * @return array{string, array{string, string, string, int|JsonInteger, int|JsonInteger, int|JsonInteger}}
     */
    public static function validateProgress(mixed $v): array
    {
        self::strict($v, self::PROGRESS_FIELDS);
        $parent = self::text($v, 'parent');
        $event = [self::text($v, 'kind'), self::text($v, 'stream'), self::text($v, 'text'), self::count($v, 'completed'), self::count($v, 'total'), self::count($v, 'dropped')];
        if (self::text($v, 'frame') !== 'progress' || self::text($v, 'apiVersion') !== API_VERSION || !Pattern::revision($parent)) {
            self::violation();
        }
        self::checkEvent($event[0], $event[1], $event[2], $event[3], $event[4]);
        return [$parent, $event];
    }

    /** Judges a reverse request frame, as the host reads it. */
    public static function validateReverseRequest(mixed $v): void
    {
        self::strict($v, self::REVERSE_REQUEST_FIELDS);
        $ref = Json::get($v, 'contract');
        self::strict($ref, ['name', 'version']);
        $parent = self::text($v, 'parent');
        if (self::text($v, 'frame') !== 'reverse_request'
            || self::text($v, 'apiVersion') !== API_VERSION
            || !Pattern::revision(self::text($v, 'id'))
            || ($parent !== '' && !Pattern::revision($parent))
            || !Pattern::identifier(self::text($ref, 'name'))
            || !Pattern::revision(self::text($ref, 'version'))
            || !Pattern::identifier(self::text($v, 'operation'))
            || Time::parse(Json::get($v, 'deadline')) === null
        ) {
            self::violation();
        }
    }

    /**
     * Judges a reverse response, which a guest accepts only for the request it is waiting on.
     * Returns [payload, null] or [null, RemoteError].
     *
     * @return array{mixed, ?RemoteError}
     */
    public static function validateReverseResponse(mixed $v, string $requestId): array
    {
        self::strict($v, self::REVERSE_RESPONSE_FIELDS);
        if (self::text($v, 'frame') !== 'reverse_response' || self::text($v, 'apiVersion') !== API_VERSION || self::text($v, 'id') !== $requestId) {
            self::violation();
        }
        $failed = Json::get($v, 'error') !== null;
        // A response carries a payload (null counts) or an error, never both or neither.
        if ($failed === Json::has($v, 'payload')) {
            self::violation();
        }
        if (!$failed) {
            return [$v->payload, null];
        }
        $e = $v->error;
        self::strict($e, ['code', 'message', 'retryAfterMilliseconds']);
        $retry = self::count($e, 'retryAfterMilliseconds');
        $message = self::text($e, 'message');
        $size = Text::utf8Length($message);
        if (!Pattern::identifier(self::text($e, 'code')) || $size < 0 || $size > MAX_MESSAGE_BYTES
            || !\is_int($retry) || $retry > MAX_RETRY_AFTER_MS) {
            self::violation();
        }
        return [null, new RemoteError($e->code, $message, $retry)];
    }

    /** @internal */
    public static function checkEvent(mixed $kind, mixed $stream, mixed $text, int|JsonInteger $completed, int|JsonInteger $total): void
    {
        $size = Text::utf8Length($text);
        if (!\in_array($kind, ['progress', 'log'], true)
            || !\in_array($stream, ['', 'out', 'err'], true)
            || ($stream !== '' && $kind !== 'log')
            || $size < 0
            || $size > MAX_PROGRESS_TEXT
            || ($total !== 0 && JsonInteger::compare($completed, $total) > 0)
        ) {
            self::violation();
        }
    }

    private static function strict(mixed $v, array $fields): void
    {
        if (!Json::strict($v, $fields)) {
            self::violation();
        }
    }

    // A member Go decodes into a string: absent and null are empty.
    private static function text(\stdClass $m, string $key): string
    {
        $v = Json::get($m, $key);
        if ($v === null) {
            return '';
        }
        if (!\is_string($v)) {
            self::violation();
        }
        return $v;
    }

    // A member Go decodes into a uint64: absent and null are zero; anything but a whole number
    // that fits is refused (1.0 and 1e2 are not whole numbers to Go).
    private static function count(\stdClass $m, string $key): int|JsonInteger
    {
        $v = Json::get($m, $key);
        if ($v === null) {
            return 0;
        }
        if (\is_int($v)) {
            if ($v < 0) {
                self::violation();
            }
            return $v;
        }
        if ($v instanceof JsonInteger && !$v->negative() && JsonInteger::compare($v, new JsonInteger('18446744073709551615')) <= 0) {
            return $v;
        }
        self::violation();
    }

    private static function violation(): never
    {
        throw new ProtocolError('plugin protocol violation');
    }
}
