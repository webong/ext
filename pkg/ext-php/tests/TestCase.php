<?php

declare(strict_types=1);

namespace ExtIO\Tests;

use ExtIO\Plugin\Json;

/** The assertions the tests use. */
abstract class TestCase
{
    public function setUp(): void
    {
    }

    public function tearDown(): void
    {
    }

    public static function fail(string $message): never
    {
        throw new \AssertionError($message);
    }

    public static function assertTrue(mixed $value, string $message = ''): void
    {
        if ($value !== true) {
            self::fail(($message !== '' ? "$message: " : '') . 'expected true, got ' . self::show($value));
        }
    }

    public static function assertFalse(mixed $value, string $message = ''): void
    {
        if ($value !== false) {
            self::fail(($message !== '' ? "$message: " : '') . 'expected false, got ' . self::show($value));
        }
    }

    public static function assertNull(mixed $value, string $message = ''): void
    {
        if ($value !== null) {
            self::fail(($message !== '' ? "$message: " : '') . 'expected null, got ' . self::show($value));
        }
    }

    public static function assertNotNull(mixed $value, string $message = ''): void
    {
        if ($value === null) {
            self::fail(($message !== '' ? "$message: " : '') . 'expected a value, got null');
        }
    }

    public static function assertSame(mixed $want, mixed $got, string $message = ''): void
    {
        if ($want !== $got) {
            self::fail(($message !== '' ? "$message: " : '') . 'expected ' . self::show($want) . ', got ' . self::show($got));
        }
    }

    /** Equal as JSON: the same encoding, so {} and [] differ and objects compare by members. */
    public static function assertJson(mixed $want, mixed $got, string $message = ''): void
    {
        self::assertSame(Json::encode($want), Json::encode($got), $message);
    }

    /**
     * @template T of \Throwable
     * @param class-string<T> $class
     * @return T
     */
    public static function assertThrows(string $class, callable $f, string $message = ''): \Throwable
    {
        try {
            $f();
        } catch (\Throwable $e) {
            if ($e instanceof $class) {
                return $e;
            }
            self::fail(($message !== '' ? "$message: " : '') . "expected $class, got " . get_class($e) . ': ' . $e->getMessage());
        }
        self::fail(($message !== '' ? "$message: " : '') . "expected $class, nothing was thrown");
    }

    public static function show(mixed $value): string
    {
        try {
            $text = Json::encode($value);
        } catch (\Throwable) {
            $text = var_export($value, true);
        }
        return \strlen($text) > 300 ? substr($text, 0, 300) . '...' : $text;
    }

    /** A decoded JSON value from a PHP value: arrays with string keys become objects. */
    public static function obj(mixed $value): mixed
    {
        return Json::decode(Json::encode($value));
    }
}
