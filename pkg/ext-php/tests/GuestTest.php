<?php

declare(strict_types=1);

namespace ExtIO\Tests;

use ExtIO\Plugin;
use ExtIO\Plugin\Guest;
use ExtIO\Plugin\Json;
use ExtIO\Plugin\JsonInteger;
use ExtIO\Plugin\ProtocolError;
use ExtIO\Plugin\RemoteError;

final class GuestTest extends TestCase
{
    public const IDENT = ['id' => 'example/p', 'revision' => 'r1'];
    public const FUTURE = '2999-01-01T00:00:00Z';

    public static function desc(): array
    {
        return [
            'apiVersion' => 'ext.plugin/v1',
            'identity' => self::IDENT,
            'contracts' => [[
                'name' => 'example.echo',
                'version' => 'v1',
                'operations' => [['name' => 'echo'], ['name' => 'seen', 'surface' => 'observation'], ['name' => 'boom'], ['name' => 'busy'], ['name' => 'noisy']],
            ]],
        ];
    }

    public static function req(string $op = 'echo', array $kw = []): array
    {
        return array_merge([
            'apiVersion' => 'ext.plugin/v1',
            'id' => 'c1',
            'plugin' => self::IDENT,
            'contract' => ['name' => 'example.echo', 'version' => 'v1'],
            'operation' => $op,
            'deadline' => self::FUTURE,
            'payload' => ['v' => 1],
        ], $kw);
    }

    public static function hello(array $kw = []): array
    {
        return array_merge([
            'apiVersion' => 'ext.plugin/v1',
            'id' => 'hello',
            'plugin' => ['id' => '', 'revision' => ''],
            'contract' => ['name' => '', 'version' => ''],
            'operation' => 'plugin.hello',
            'deadline' => self::FUTURE,
        ], $kw);
    }

    public static function handler(string $contract, string $op, mixed $payload): mixed
    {
        if ($op === 'boom') {
            throw new \RuntimeException(str_repeat('x', 5000));
        }
        if ($op === 'busy') {
            throw new RemoteError('busy', 'later', 250);
        }
        if ($op === 'noisy') {
            echo 'stray output';
            return 'ok';
        }
        return $payload;
    }

    public static function frames(array ...$objs): string
    {
        $out = '';
        foreach ($objs as $o) {
            $out .= Json::encode($o) . "\n";
        }
        return $out;
    }

    /** @return array{list<mixed>, ?ProtocolError} */
    public static function run(string $bytes, array $options = [], ?callable $handler = null): array
    {
        $in = fopen('php://memory', 'w+b');
        fwrite($in, $bytes);
        rewind($in);
        $out = fopen('php://memory', 'w+b');
        $guest = new Guest(self::desc(), $handler ?? self::handler(...), ...$options);
        $err = null;
        try {
            Plugin\serve($guest, $in, $out);
        } catch (ProtocolError $e) {
            $err = $e;
        }
        rewind($out);
        $lines = [];
        while (($line = fgets($out)) !== false) {
            $lines[] = Json::decode(rtrim($line, "\n"));
        }
        return [$lines, $err];
    }

    private static function invoke(Guest $guest, array $request): array
    {
        return $guest->invoke(self::obj($request));
    }

    public function testHelloAndEcho(): void
    {
        [$out, $err] = self::run(self::frames(self::hello(), self::req()));
        self::assertNull($err);
        self::assertJson(self::desc(), $out[0]->payload);
        self::assertJson(['apiVersion' => 'ext.plugin/v1', 'id' => 'c1', 'payload' => ['v' => 1]], $out[1]);
    }

    public function testHelloMustBeFirstAndStrict(): void
    {
        foreach ([self::req(), self::hello(['id' => 'x']), self::hello(['operation' => 'echo']), self::hello(['payload' => null]), self::hello(['plugin' => self::IDENT]), self::hello(['surface' => 's']), self::hello(['surface' => null])] as $bad) {
            [$out, $err] = self::run(self::frames($bad));
            self::assertNotNull($err, self::show($bad));
            self::assertSame([], $out);
        }
    }

    public function testEmptyResultIsNull(): void
    {
        $guest = new Guest(self::desc(), static fn () => null);
        $resp = self::invoke($guest, self::req());
        self::assertTrue(\array_key_exists('payload', $resp));
        self::assertNull($resp['payload']);
        self::assertSame('{"apiVersion":"ext.plugin/v1","id":"c1","payload":null}', Json::encode($resp));
    }

    public function testEmptyObjectsAndArraysStayApart(): void
    {
        $guest = new Guest(self::desc(), self::handler(...));
        $payload = Json::decode('{"o":{},"a":[],"n":[{}],"f":1.5,"z":1.0,"big":18446744073709551615,"neg":-0,"s":"a/bé "}');
        $resp = $guest->invoke(self::obj(self::req('echo', ['payload' => $payload])));
        self::assertSame('{"o":{},"a":[],"n":[{}],"f":1.5,"z":1.0,"big":18446744073709551615,"neg":0,"s":"a/bé\\u2028"}', Json::encode($resp['payload']));
        self::assertTrue($resp['payload']->big instanceof JsonInteger);
    }

    public function testRequestValidation(): void
    {
        $cases = [
            self::req('echo', ['apiVersion' => 'ext.plugin/v2']),
            self::req('echo', ['id' => '-bad']),
            self::req('echo', ['plugin' => ['id' => 'example/p', 'revision' => 'r2']]),
            self::req('echo', ['contract' => ['name' => 'example.echo', 'version' => 'v2']]),
            self::req('missing'),
            self::req('seen'), // declared surface missing
            self::req('echo', ['deadline' => '0001-01-01T00:00:00Z']),
            self::req('plugin.hello'),
        ];
        $guest = new Guest(self::desc(), self::handler(...));
        foreach ($cases as $r) {
            $resp = self::invoke($guest, $r);
            self::assertSame('invalid_request', $resp['error']['code'], self::show($r));
            self::assertFalse(\array_key_exists('payload', $resp));
        }
        self::assertJson(['v' => 1], self::invoke($guest, self::req('seen', ['surface' => 'observation']))['payload']);
        self::assertSame('invalid_request', self::invoke($guest, self::req('seen', ['surface' => 'other']))['error']['code']);
    }

    public function testMissingDeadlineIsInvalidRequest(): void
    {
        $r = self::req();
        unset($r['deadline']);
        self::assertSame('invalid_request', self::invoke(new Guest(self::desc(), self::handler(...)), $r)['error']['code']);
    }

    public function testExpiredDeadline(): void
    {
        $resp = self::invoke(new Guest(self::desc(), self::handler(...)), self::req('echo', ['deadline' => '2000-01-01T00:00:00Z']));
        self::assertSame('provider_error', $resp['error']['code']);
    }

    public function testDeadlineOffsetsAndFractions(): void
    {
        foreach (['2999-01-01T00:00:00.123456789Z', '2999-01-01T05:30:00+05:30', '2999-01-01T00:00:00.5-07:00'] as $stamp) {
            self::assertJson(['v' => 1], self::invoke(new Guest(self::desc(), self::handler(...)), self::req('echo', ['deadline' => $stamp]))['payload'], $stamp);
        }
    }

    public function testErrors(): void
    {
        $guest = new Guest(self::desc(), self::handler(...));
        $busy = self::invoke($guest, self::req('busy'))['error'];
        self::assertSame(['code' => 'busy', 'message' => 'later', 'retryAfterMilliseconds' => 250], $busy);
        $boom = self::invoke($guest, self::req('boom'))['error'];
        self::assertSame('provider_error', $boom['code']);
        self::assertSame(4096, \strlen($boom['message']));
        $redacted = self::invoke(new Guest(self::desc(), self::handler(...), redactErrors: true), self::req('boom'))['error'];
        self::assertSame(['code' => 'operation_failed', 'message' => 'plugin operation failed'], $redacted);
        $multibyte = new Guest(self::desc(), static fn () => throw new \RuntimeException(str_repeat('é', 4000)));
        $message = self::invoke($multibyte, self::req())['error']['message'];
        self::assertTrue(\strlen($message) <= 4096 && preg_match('//u', $message) === 1);
        // A message that is not UTF-8 is repaired, so the response still encodes.
        $binary = new Guest(self::desc(), static fn () => throw new \RuntimeException("bad \xff byte"));
        self::assertSame("bad \u{FFFD} byte", self::invoke($binary, self::req())['error']['message']);
        // A PHP error (not an exception) is a provider error too.
        $typed = new Guest(self::desc(), static fn () => intdiv(1, 0));
        self::assertSame('provider_error', self::invoke($typed, self::req())['error']['code']);
    }

    public function testRemoteErrorValidation(): void
    {
        foreach ([['Bad', 'm'], ['ok', str_repeat('x', 4097)], ['ok', 'm', -1], ['ok', 'm', Plugin\MAX_RETRY_AFTER_MS + 1], ['ok', 'm', true], ['ok', "\xff"], [1, 'm']] as $args) {
            self::assertThrows(\InvalidArgumentException::class, static fn () => new RemoteError(...$args), self::show($args));
        }
        $e = new RemoteError('ok', 'm', Plugin\MAX_RETRY_AFTER_MS);
        self::assertSame('ok', $e->getCode());
        self::assertSame('m', $e->getMessage());
        self::assertSame(Plugin\MAX_RETRY_AFTER_MS, $e->retryAfterMs);
    }

    public function testUnserializableResultIsAnError(): void
    {
        $stream = fopen('php://memory', 'r');
        foreach ([static fn () => $stream, static fn () => NAN, static fn () => INF, static fn () => "\xff"] as $h) {
            $resp = self::invoke(new Guest(self::desc(), $h), self::req());
            self::assertSame('provider_error', $resp['error']['code']);
        }
    }

    public function testCurrentCall(): void
    {
        $seen = [];
        $h = static function () use (&$seen) {
            $info = Plugin\currentCall();
            $seen = ['op' => $info->operation, 'id' => $info->requestId, 'ok' => $info->remaining() > 0];
            return null;
        };
        self::invoke(new Guest(self::desc(), $h), self::req());
        self::assertSame(['op' => 'echo', 'id' => 'c1', 'ok' => true], $seen);
        self::assertNull(Plugin\currentCall());
    }

    public function testStrictJsonEndsStream(): void
    {
        $bad = [
            '{"apiVersion":"ext.plugin/v1","id":"hello","id":"hello"}' . "\n",
            '{"apiVersion":"ext.plugin/v1","unknown":1}' . "\n",
            '[1,2]' . "\n",
            '{"a":NaN}' . "\n",
            '{"a":"\\ud800"}' . "\n",
            '{"a":"\\udc00"}' . "\n",
            '{"a":1} {"b":2}' . "\n",
            "\xff\xfe\n",
            "\xef\xbb\xbf{}\n",
            '{"a":"' . "\x01" . '"}' . "\n",
            '{"a":01}' . "\n",
            '{"a":' . str_repeat('[', 70) . str_repeat(']', 70) . '}' . "\n",
        ];
        foreach ($bad as $raw) {
            [$out, $err] = self::run($raw);
            self::assertNotNull($err, bin2hex($raw));
            self::assertSame([], $out);
        }
    }

    public function testNestingLimit(): void
    {
        // The root is at depth 0, and no value may be deeper than 64.
        Json::decode(str_repeat('[', 64) . '1' . str_repeat(']', 64));
        Json::decode(str_repeat('[', 65) . str_repeat(']', 65));
        self::assertThrows(ProtocolError::class, static fn () => Json::decode(str_repeat('[', 65) . '1' . str_repeat(']', 65)));
    }

    public function testPayloadStrictnessAfterHello(): void
    {
        [$out, $err] = self::run(self::frames(self::hello()) . '{"apiVersion":"ext.plugin/v1","id":"c1","operation":"echo","deadline":"2999-01-01T00:00:00Z","payload":{"a":1,"a":2}}' . "\n");
        self::assertNotNull($err);
        self::assertSame(1, \count($out));
        // Keys that differ only in their escapes are the same key.
        [, $err] = self::run(self::frames(self::hello()) . '{"apiVersion":"ext.plugin/v1","id":"c1","operation":"echo","deadline":"2999-01-01T00:00:00Z","payload":{"a":1,"\\u0061":2}}' . "\n");
        self::assertNotNull($err);
    }

    public function testAstralPairAllowed(): void
    {
        [$out, $err] = self::run(self::frames(self::hello()) . '{"apiVersion":"ext.plugin/v1","id":"c1","plugin":{"id":"example/p","revision":"r1"},"contract":{"name":"example.echo","version":"v1"},"operation":"echo","deadline":"2999-01-01T00:00:00Z","payload":"\\ud83d\\ude00"}' . "\n");
        self::assertNull($err);
        self::assertSame("\u{1F600}", $out[1]->payload);
    }

    public function testLongStringsOfEscapesDecode(): void
    {
        // A regular expression over this would reach PCRE's backtracking limit.
        $text = str_repeat("a\"\\\u{e9}\n", 1 << 20);
        self::assertSame($text, Json::decode(json_encode($text, \JSON_UNESCAPED_UNICODE)));
        self::assertSame($text, Json::decode(json_encode($text)));
    }

    public function testFrameLimit(): void
    {
        $limit = Plugin\MAX_FRAME_BYTES;
        [, $err] = self::run(str_repeat('x', $limit + 1) . "\n");
        self::assertNotNull($err);
        [, $err] = self::run(str_repeat('x', $limit + 5)); // no newline, unbounded
        self::assertNotNull($err);
        [, $err] = self::run(substr(self::frames(self::hello()), 0, -1)); // EOF inside a frame
        self::assertNotNull($err);
    }

    public function testOversizeResponseBecomesError(): void
    {
        [$out] = self::run(self::frames(self::hello(), self::req()), [], static fn () => str_repeat('x', Plugin\MAX_FRAME_BYTES));
        self::assertSame('response_too_large', $out[1]->error->code);
    }

    public function testCleanEof(): void
    {
        self::assertSame([[], null], self::run(''));
    }

    public function testDescriptorLoading(): void
    {
        $dir = sys_get_temp_dir() . '/ext-php-' . bin2hex(random_bytes(6));
        mkdir($dir);
        try {
            $bare = "$dir/descriptor.json";
            $pkg = "$dir/plugin.json";
            file_put_contents($bare, Json::encode(self::desc()));
            file_put_contents($pkg, Json::encode(['apiVersion' => 'ext.package/v1', 'descriptor' => self::desc(), 'artifacts' => []]));
            self::assertJson(self::desc(), Plugin\loadDescriptor($bare));
            self::assertJson(self::desc(), Plugin\loadDescriptor($pkg));
            self::assertTrue(new Guest($pkg, self::handler(...)) instanceof Guest);
            file_put_contents($pkg, Json::encode(['apiVersion' => 'other', 'descriptor' => self::desc()]));
            self::assertThrows(\InvalidArgumentException::class, static fn () => Plugin\loadDescriptor($pkg));
            self::assertThrows(\InvalidArgumentException::class, static fn () => Plugin\loadDescriptor("$dir/missing.json"));
        } finally {
            array_map('unlink', glob("$dir/*"));
            rmdir($dir);
        }
    }

    public function testDescriptorValidation(): void
    {
        $mutate = static function (callable $f): array {
            $d = self::desc();
            $f($d);
            return $d;
        };
        $bad = [
            $mutate(static function (&$d) { $d['apiVersion'] = 'x'; }),
            $mutate(static function (&$d) { $d['identity']['id'] = 'Bad'; }),
            $mutate(static function (&$d) { $d['contracts'] = []; }),
            $mutate(static function (&$d) { $d['contracts'][0]['operations'] = []; }),
            $mutate(static function (&$d) { $d['contracts'][0]['operations'][] = ['name' => 'plugin.hello']; }),
            $mutate(static function (&$d) { $d['contracts'][0]['operations'][] = ['name' => 'echo']; }),
            $mutate(static function (&$d) { $d['contracts'][] = $d['contracts'][0]; }),
            $mutate(static function (&$d) { $d['extra'] = 1; }),
        ];
        foreach ($bad as $d) {
            self::assertThrows(\InvalidArgumentException::class, static fn () => Plugin\validateDescriptor($d), self::show($d));
        }
        self::assertThrows(\InvalidArgumentException::class, static fn () => new Guest($bad[0], self::handler(...)));
        Plugin\validateDescriptor(self::desc());
        Plugin\validateDescriptor(self::obj(self::desc()));
    }

    public function testStdoutCarriesOnlyFrames(): void
    {
        $dir = sys_get_temp_dir() . '/ext-php-' . bin2hex(random_bytes(6));
        mkdir($dir);
        file_put_contents("$dir/plugin.json", Json::encode(self::desc()));
        file_put_contents("$dir/guest.php", sprintf(<<<'PHP'
            <?php
            require %s;
            ExtIO\Plugin\serveStdio(__DIR__ . '/plugin.json', function ($c, $o, $p) {
                echo "noise on stdout\n";
                print "print noise\n";
                file_put_contents('php://stdout', "raw noise\n");
                trigger_error('a warning', E_USER_WARNING);
                return 'ok';
            });
            PHP, var_export(\dirname(__DIR__) . '/autoload.php', true)));
        try {
            [$code, $stdout, $stderr] = Subprocess::run([\PHP_BINARY, '-d', 'display_errors=1', "$dir/guest.php"], self::frames(self::hello(), self::req()));
        } finally {
            array_map('unlink', glob("$dir/*"));
            rmdir($dir);
        }
        self::assertSame(0, $code, $stderr);
        $lines = explode("\n", rtrim($stdout, "\n"));
        self::assertSame(2, \count($lines), $stdout);
        self::assertSame('ok', Json::decode($lines[1])->payload);
        foreach (['noise on stdout', 'print noise', 'raw noise', 'a warning'] as $noise) {
            self::assertTrue(str_contains($stderr, $noise), "$noise not on stderr: $stderr");
        }
    }
}
