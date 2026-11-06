<?php

declare(strict_types=1);

namespace ExtIO\Tests;

use ExtIO\Plugin\Capability\Router;
use ExtIO\Plugin\Capability\Shape;
use ExtIO\Plugin\Guest;
use ExtIO\Plugin\RemoteError;

final class CapabilityTest extends TestCase
{
    private const IDENT = ['id' => 'example/p', 'revision' => 'r1'];

    private array $updates = [];
    private array $closed = [];
    private Guest $guest;

    private static function desc(): array
    {
        return [
            'apiVersion' => 'ext.plugin/v1',
            'identity' => self::IDENT,
            'contracts' => [
                ['name' => 'ext.health', 'version' => 'v1', 'operations' => [['name' => 'check', 'surface' => 'observation']]],
                ['name' => 'ext.configuration', 'version' => 'v1', 'operations' => [['name' => 'update', 'surface' => 'configuration']]],
                ['name' => 'ext.stream', 'version' => 'v1', 'operations' => [['name' => 'open'], ['name' => 'read'], ['name' => 'close']]],
            ],
        ];
    }

    private static function request(string $contract, string $operation, mixed $payload, string $surface = ''): mixed
    {
        $r = [
            'apiVersion' => 'ext.plugin/v1',
            'id' => 'c1',
            'plugin' => self::IDENT,
            'contract' => ['name' => $contract, 'version' => 'v1'],
            'operation' => $operation,
            'deadline' => '2999-01-01T00:00:00Z',
            'payload' => $payload,
        ];
        if ($surface !== '') {
            $r['surface'] = $surface;
        }
        return self::obj($r);
    }

    public function setUp(): void
    {
        $this->updates = [];
        $this->closed = [];
        $router = new Router();
        $router->health(static fn () => ['status' => 'degraded', 'ready' => false]);
        $router->configuration(function ($i, $r, $v) {
            $this->updates[] = [$i, $r, $v];
        });
        $router->stream(
            static fn ($parameters) => 's1',
            static fn ($id, $seq, $limit) => ['items' => array_fill(0, min($limit, 2), ['v' => $seq]), 'done' => $seq >= 2],
            function ($id) {
                $this->closed[] = $id;
            },
        );
        $this->guest = new Guest(self::desc(), $router);
    }

    private function call(string $contract, string $operation, mixed $payload, string $surface = ''): array
    {
        return $this->guest->invoke(self::request($contract, $operation, $payload, $surface));
    }

    public function testHealth(): void
    {
        $r = $this->call('ext.health', 'check', new \stdClass(), 'observation');
        self::assertJson(['status' => 'degraded', 'ready' => false], $r['payload']);
        $r = $this->call('ext.health', 'check', ['x' => 1], 'observation');
        self::assertSame('invalid_input', $r['error']['code']);
        $r = $this->call('ext.health', 'check', [], 'observation'); // an array is not the empty object
        self::assertSame('invalid_input', $r['error']['code']);
    }

    public function testHealthResultIsValidated(): void
    {
        $bad = (new Router())->health(static fn () => ['status' => 'great', 'ready' => true]);
        $r = (new Guest(self::desc(), $bad))->invoke(self::request('ext.health', 'check', new \stdClass(), 'observation'));
        self::assertTrue(isset($r['error']));
        self::assertTrue($r['error']['code'] !== 'invalid_request');
    }

    public function testConfiguration(): void
    {
        $ok = ['instance' => 'one', 'revision' => 'r1', 'values' => ['name' => 'a']];
        $r = $this->call('ext.configuration', 'update', $ok, 'configuration');
        self::assertJson(['instance' => 'one', 'revision' => 'r1'], $r['payload']);
        self::assertSame(1, \count($this->updates));
        self::assertJson(['one', 'r1', ['name' => 'a']], $this->updates[0]);
        foreach ([
            ['instance' => ''] + $ok,
            ['revision' => ''] + $ok,
            ['instance' => str_repeat('x', 257)] + $ok,
            ['revision' => str_repeat('r', 257)] + $ok,
            ['instance' => 'one', 'revision' => 'r1'],
            $ok + ['extra' => 1],
            ['instance' => 1] + $ok,
        ] as $bad) {
            $r = $this->call('ext.configuration', 'update', $bad, 'configuration');
            self::assertSame('invalid_input', $r['error']['code'], self::show($bad));
        }
        self::assertSame(1, \count($this->updates));
        // The limit counts characters, not bytes.
        $long = ['instance' => str_repeat('é', 256), 'revision' => str_repeat('r', 256)] + $ok;
        self::assertSame(str_repeat('é', 256), $this->call('ext.configuration', 'update', $long, 'configuration')['payload']->instance);
    }

    public function testStream(): void
    {
        self::assertJson(['id' => 's1'], $this->call('ext.stream', 'open', ['parameters' => new \stdClass()])['payload']);
        $r = $this->call('ext.stream', 'read', ['id' => 's1', 'sequence' => 1, 'limit' => 5]);
        self::assertJson(['items' => [['v' => 1], ['v' => 1]], 'done' => false], $r['payload']);
        self::assertTrue($this->call('ext.stream', 'read', ['id' => 's1', 'sequence' => 2, 'limit' => 1])['payload']->done);
        foreach ([
            ['id' => '', 'sequence' => 1, 'limit' => 1],
            ['id' => str_repeat('i', 65), 'sequence' => 1, 'limit' => 1],
            ['id' => 's1', 'sequence' => 0, 'limit' => 1],
            ['id' => 's1', 'sequence' => 1, 'limit' => 0],
            ['id' => 's1', 'sequence' => 1, 'limit' => -1],
            ['id' => 's1', 'sequence' => 1, 'limit' => Shape::MAX_STREAM_ITEMS + 1],
            ['id' => 's1', 'sequence' => true, 'limit' => 1],
            ['id' => 's1', 'sequence' => 1.5, 'limit' => 1],
        ] as $bad) {
            $r = $this->call('ext.stream', 'read', $bad);
            self::assertSame('invalid_input', $r['error']['code'], self::show($bad));
        }
        self::assertSame('{}', \ExtIO\Plugin\Json::encode($this->call('ext.stream', 'close', ['id' => 's1'])['payload']));
        self::assertSame(['s1'], $this->closed);
        self::assertSame('invalid_input', $this->call('ext.stream', 'close', ['id' => ''])['error']['code']);
    }

    public function testBatchLimits(): void
    {
        Shape::streamBatch(['items' => array_map(static fn ($i) => ['v' => $i], range(1, Shape::MAX_STREAM_ITEMS)), 'done' => false]);
        self::assertThrows(\InvalidArgumentException::class, static fn () => Shape::streamBatch(['items' => array_map(static fn ($i) => ['v' => $i], range(0, Shape::MAX_STREAM_ITEMS)), 'done' => false]));
        $big = str_repeat('x', 600 << 10);
        self::assertThrows(\InvalidArgumentException::class, static fn () => Shape::streamBatch(['items' => [$big, $big], 'done' => false]));
        self::assertThrows(\InvalidArgumentException::class, static fn () => Shape::streamBatch(['items' => [], 'done' => 1]));
    }

    public function testUnregisteredOperationAndDuplicates(): void
    {
        $router = new Router();
        self::assertThrows(RemoteError::class, static fn () => $router('ext.health', 'check', new \stdClass()));
        $router->health(static fn () => ['status' => 'healthy', 'ready' => true]);
        self::assertThrows(\InvalidArgumentException::class, static fn () => $router->health(static fn () => ['status' => 'healthy', 'ready' => true]));
    }
}
