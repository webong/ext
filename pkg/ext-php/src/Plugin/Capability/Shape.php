<?php

declare(strict_types=1);

namespace ExtIO\Plugin\Capability;

use ExtIO\Plugin\Json;
use ExtIO\Plugin\JsonInteger;
use ExtIO\Plugin\Text;

/**
 * Typed definitions of the SDK's optional capability contracts, with the same wire shapes and
 * limits as the Go library: ext.health, ext.configuration and ext.stream. Each check takes a
 * decoded value (an array is taken as the JSON it encodes), throws InvalidArgumentException when
 * it does not fit, and returns the value as JSON (objects as stdClass).
 */
final class Shape
{
    public const MAX_STREAM_ITEMS = 256;
    public const MAX_STREAM_BATCH_BYTES = 1 << 20;
    private const MAX_ID = 64;
    private const MAX_NAME = 256;
    private const STATUSES = ['healthy', 'degraded', 'unhealthy'];

    public static function healthRequest(mixed $value): \stdClass
    {
        return self::object($value, []);
    }

    public static function health(mixed $value): \stdClass
    {
        $value = self::object($value, ['status', 'ready']);
        if (!\in_array($value->status, self::STATUSES, true) || !\is_bool($value->ready)) {
            throw new \InvalidArgumentException('invalid health');
        }
        return $value;
    }

    public static function configuration(mixed $value): \stdClass
    {
        $value = self::object($value, ['instance', 'revision', 'values']);
        self::text($value->instance, self::MAX_NAME);
        self::text($value->revision, self::MAX_NAME);
        return $value;
    }

    public static function configured(mixed $value): \stdClass
    {
        $value = self::object($value, ['instance', 'revision']);
        if (!\is_string($value->instance) || !\is_string($value->revision)) {
            throw new \InvalidArgumentException('invalid configured');
        }
        return $value;
    }

    public static function streamOpen(mixed $value): \stdClass
    {
        return self::object($value, ['parameters']);
    }

    public static function streamOpened(mixed $value): \stdClass
    {
        $value = self::object($value, ['id']);
        self::text($value->id, self::MAX_ID);
        return $value;
    }

    public static function streamRead(mixed $value): \stdClass
    {
        $value = self::object($value, ['id', 'sequence', 'limit']);
        self::text($value->id, self::MAX_ID);
        $sequence = $value->sequence;
        $limit = $value->limit;
        if (!(\is_int($sequence) || $sequence instanceof JsonInteger) || !\is_int($limit)) {
            throw new \InvalidArgumentException('expected an integer');
        }
        if ((\is_int($sequence) ? $sequence < 1 : $sequence->negative()) || $limit < 1 || $limit > self::MAX_STREAM_ITEMS) {
            throw new \InvalidArgumentException('invalid read');
        }
        return $value;
    }

    public static function streamClose(mixed $value): \stdClass
    {
        $value = self::object($value, ['id']);
        self::text($value->id, self::MAX_ID);
        return $value;
    }

    /** At most MAX_STREAM_ITEMS items and MAX_STREAM_BATCH_BYTES of JSON. */
    public static function streamBatch(mixed $value): \stdClass
    {
        $value = self::object($value, ['items', 'done']);
        if (!\is_array($value->items) || !\is_bool($value->done)) {
            throw new \InvalidArgumentException('invalid batch');
        }
        if (\count($value->items) > self::MAX_STREAM_ITEMS) {
            throw new \InvalidArgumentException('too many items');
        }
        $size = 0;
        foreach ($value->items as $item) {
            $size += Json::encodedSize($item);
            if ($size > self::MAX_STREAM_BATCH_BYTES) {
                throw new \InvalidArgumentException('batch too large');
            }
        }
        return $value;
    }

    /**
     * An object with exactly these members. An empty PHP array is the JSON array [], so the empty
     * object is `new \stdClass()`.
     */
    public static function object(mixed $value, array $fields): \stdClass
    {
        $value = Json::normalize($value);
        if (!Json::exactly($value, $fields)) {
            throw new \InvalidArgumentException('object does not have exactly the fields ' . implode(', ', $fields));
        }
        return $value;
    }

    private static function text(mixed $value, int $limit): void
    {
        if (!\is_string($value) || $value === '' || Text::characters($value) > $limit) {
            throw new \InvalidArgumentException("expected a non-empty string of at most $limit characters");
        }
    }
}
