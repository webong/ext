<?php

declare(strict_types=1);

namespace ExtIO\Tests;

use ExtIO\Host\Engine;
use ExtIO\Host\HostError;
use ExtIO\Plugin\Json;
use ExtIO\Plugin\RemoteError;

/**
 * The host SDK (ExtIO\Host) on the C engine, through FFI.
 *
 * It needs the FFI extension, the engine's shared library (EXT_HOST_LIBRARY), and the reference
 * guest twice: EXT_GO_GUEST, the JSON-line Go guest, and EXT_GO_SHARED, the same guest as a C ABI
 * library. It runs every case of lib/plugin/testdata/v1/host-cases.json on both. Without them the
 * host tests pass without running, after saying so, or fail when EXT_HOST_REQUIRED=1.
 * scripts/host-cases.sh builds everything and runs them.
 */
final class HostTest extends TestCase
{
    private const FIXTURES = __DIR__ . '/../../../lib/plugin/testdata/v1';

    private static ?Engine $engine = null;
    private static bool $told = false;

    private static function fixture(string $name): mixed
    {
        return Json::decode((string) file_get_contents(self::FIXTURES . '/' . $name));
    }

    /** The engine, or null (after saying why once) when the host cannot run here. */
    private static function engine(string ...$needs): ?Engine
    {
        $why = null;
        if (!\extension_loaded('ffi')) {
            $why = 'the FFI extension is not loaded';
        } else {
            foreach (['EXT_HOST_LIBRARY', ...$needs] as $name) {
                if ((string) getenv($name) === '') {
                    $why = "set $name, or run scripts/host-cases.sh";
                    break;
                }
            }
        }
        if ($why !== null) {
            if (getenv('EXT_HOST_REQUIRED') === '1') {
                self::fail($why);
            }
            if (!self::$told) {
                fwrite(STDERR, "skip HostTest: $why\n");
                self::$told = true;
            }
            return null;
        }
        return self::$engine ??= new Engine();
    }

    public function testGuestNeverLoadsTheHost(): void
    {
        // A script file and an argument list, not `php -r` through a shell: cmd.exe quotes differently.
        $script = sys_get_temp_dir() . \DIRECTORY_SEPARATOR . 'ext-php-guest-' . bin2hex(random_bytes(6)) . '.php';
        file_put_contents($script, '<?php require ' . var_export(__DIR__ . '/../autoload.php', true) . '; '
            . 'ExtIO\\Plugin\\Json::encode(1); echo json_encode(class_exists("ExtIO\\\\Host\\\\Engine", false));');
        try {
            [$code, $out, $err] = Subprocess::run([\PHP_BINARY, $script], '');
        } finally {
            unlink($script);
        }
        self::assertSame(0, $code, $err);
        self::assertSame('false', $out);
    }

    // Every case of host-cases.json, on both reference guests.
    public function testHostCases(): void
    {
        $engine = self::engine('EXT_GO_GUEST', 'EXT_GO_SHARED');
        if ($engine === null) {
            return;
        }
        $spec = self::fixture('host-cases.json');
        $descriptor = self::fixture($spec->descriptor);
        foreach ($spec->guests as $guest) {
            $path = (string) getenv($guest === 'jsonline' ? 'EXT_GO_GUEST' : 'EXT_GO_SHARED');
            foreach ($spec->cases as $case) {
                try {
                    $this->runCase($engine, $guest, $path, $descriptor, $spec->contract, $case);
                } catch (\AssertionError $e) {
                    self::fail("$guest/{$case->name}: " . $e->getMessage());
                }
            }
        }
    }

    private function runCase(Engine $engine, string $guest, string $path, \stdClass $descriptor, \stdClass $contract, \stdClass $case): void
    {
        $d = Json::decode(Json::encode($descriptor));
        if (isset($case->revision)) {
            $d->identity->revision = $case->revision;
        }
        $verify = ($case->verify ?? null) === 'deny' ? static fn () => false : null;
        $authorize = ($case->authorize ?? null) === 'deny' ? static fn () => false : null;
        $h = $guest === 'jsonline'
            ? $engine->process($d, $path, verify: $verify, authorize: $authorize)
            : $engine->sharedLibrary($d, $path, $verify, $authorize);
        try {
            try {
                $h->start(5.0);
                $got = 'ok';
            } catch (HostError $e) {
                $got = $e->status;
            }
            self::assertSame($case->start ?? 'ok', $got, 'start');
            if ($got !== 'ok') {
                return;
            }
            foreach ($case->steps ?? [] as $i => $step) {
                if (isset($step->close)) {
                    $h->close();
                } elseif (isset($step->state)) {
                    self::assertSame($step->state, $h->state(), "step $i");
                } else {
                    $this->runCall($h, $contract, $i, $step);
                }
            }
        } finally {
            $h->destroy();
        }
    }

    private function runCall(\ExtIO\Host\Host $h, \stdClass $contract, int $i, \stdClass $step): void
    {
        $where = "step $i ({$step->call})";
        $outcome = 'result';
        $value = null;
        try {
            $value = $h->call($contract, $step->call, $step->payload ?? null, ($step->timeoutMs ?? 3000) / 1000.0);
        } catch (RemoteError $e) {
            [$outcome, $value] = ['error', $e];
        } catch (HostError $e) {
            [$outcome, $value] = ['status', $e->status];
        }
        if (isset($step->fails)) {
            self::assertTrue($outcome !== 'result', "$where succeeded with " . self::show($value));
        } elseif (property_exists($step, 'result')) {
            self::assertSame('result', $outcome, "$where: " . self::show($outcome === 'error' ? $value->getMessage() : $value));
            self::assertJson($step->result, $value, $where);
        } elseif (isset($step->error)) {
            self::assertSame('error', $outcome, "$where: " . self::show($value));
            self::assertSame($step->error->code, $value->getCode(), $where);
            self::assertSame($step->error->message, $value->getMessage(), $where);
            self::assertSame($step->error->retryAfterMilliseconds ?? 0, $value->retryAfterMs ?? 0, $where);
        } else {
            self::assertSame('status', $outcome, "$where: " . self::show($value));
            $allowed = \is_array($step->status) ? $step->status : [$step->status];
            self::assertTrue(\in_array($value, $allowed, true), "$where: status $value, want " . self::show($step->status));
        }
    }

    // What the cases leave to each binding: policies, envelopes, reentry and ownership.

    /** @return array{0: Engine, 1: string, 2: \stdClass, 3: \stdClass}|null */
    private static function fixtures(): ?array
    {
        $engine = self::engine('EXT_GO_GUEST');
        if ($engine === null) {
            return null;
        }
        $descriptor = self::fixture('descriptor.json');
        return [$engine, (string) getenv('EXT_GO_GUEST'), $descriptor, $descriptor->contracts[0]];
    }

    public function testMissingLibraryIsReported(): void
    {
        if (self::fixtures() === null) {
            return;
        }
        self::assertThrows(\FFI\Exception::class, static fn () => new Engine('/does/not/exist/libext_host.so'));
    }

    public function testPoliciesSeeJson(): void
    {
        if (($s = self::fixtures()) === null) {
            return;
        }
        [$engine, $guest, $descriptor, $contract] = $s;
        $seen = [];
        $h = $engine->process($descriptor, $guest,
            verify: static function ($d) use (&$seen) { $seen['verify'] = $d->identity->id; return true; },
            authorize: static function ($r) use (&$seen) { $seen['authorize'] = [$r->operation, $r->payload]; return true; });
        $h->start();
        self::assertJson(['value' => 1], $h->call($contract, 'echo', ['value' => 1]));
        $h->destroy();
        self::assertJson(['verify' => 'ctx/conformance', 'authorize' => ['echo', ['value' => 1]]], $seen);
    }

    public function testThrowingPolicyDeniesAndKeepsTheSession(): void
    {
        if (($s = self::fixtures()) === null) {
            return;
        }
        [$engine, $guest, $descriptor, $contract] = $s;
        $h = $engine->process($descriptor, $guest, authorize: static function () { throw new \RuntimeException('policy bug'); });
        $h->start();
        $e = self::assertThrows(HostError::class, static fn () => $h->call($contract, 'echo', ['value' => 1]));
        self::assertSame('denied', $e->status);
        self::assertSame('ready', $h->state());
        $h->destroy();
    }

    public function testPolicyCannotReenterItsHost(): void
    {
        if (($s = self::fixtures()) === null) {
            return;
        }
        [$engine, $guest, $descriptor, $contract] = $s;
        $reentered = null;
        $h = null;
        $h = $engine->process($descriptor, $guest, authorize: static function () use (&$h, &$reentered) {
            try {
                $h->state();
                $reentered = 'allowed';
            } catch (HostError $e) {
                $reentered = $e->status;
            }
            return true;
        });
        $h->start();
        self::assertJson(['value' => 2], $h->call($contract, 'echo', ['value' => 2]));
        self::assertSame('invalid', $reentered);
        $h->destroy();
    }

    public function testInvokeTakesEnvelopes(): void
    {
        if (($s = self::fixtures()) === null) {
            return;
        }
        [$engine, $guest, $descriptor, $contract] = $s;
        $h = $engine->process($descriptor, $guest);
        $h->start();
        $request = [
            'apiVersion' => 'ext.plugin/v1', 'id' => 'r1', 'plugin' => $descriptor->identity,
            'contract' => ['name' => $contract->name, 'version' => $contract->version],
            'operation' => 'echo', 'deadline' => '2099-01-01T00:00:00Z', 'payload' => ['value' => 3],
        ];
        $response = Json::decode($h->invoke(Json::encode($request)));
        self::assertJson(['r1', ['value' => 3]], [$response->id, $response->payload]);
        $h->destroy();
    }

    public function testRelativePathsAreRefused(): void
    {
        if (($s = self::fixtures()) === null) {
            return;
        }
        [$engine, , $descriptor] = $s;
        self::assertSame('invalid', self::assertThrows(HostError::class, static fn () => $engine->sharedLibrary($descriptor, 'relative/libguest.so'))->status);
        self::assertSame('invalid', self::assertThrows(HostError::class, static fn () => $engine->process($descriptor, 'relative/guest'))->status);
    }

    public function testDestroyedHostRefusesUse(): void
    {
        if (($s = self::fixtures()) === null) {
            return;
        }
        [$engine, $guest, $descriptor] = $s;
        $h = $engine->process($descriptor, $guest);
        $h->destroy();
        $h->destroy(); // once is enough; again is harmless
        $h->close();
        self::assertTrue($h->destroyed());
        self::assertSame('closed', self::assertThrows(HostError::class, static fn () => $h->start())->status);
    }

    public function testStderrIsKept(): void
    {
        if (($s = self::fixtures()) === null) {
            return;
        }
        [$engine, $guest, $descriptor] = $s;
        $h = $engine->process($descriptor, $guest);
        $h->start();
        self::assertTrue(\is_string($h->stderr()));
        $h->destroy();
    }

    public function testDrainWaitsAndCloses(): void
    {
        if (($s = self::fixtures()) === null) {
            return;
        }
        [$engine, $guest, $descriptor, $contract] = $s;
        $h = $engine->process($descriptor, $guest);
        $h->start();
        $h->call($contract, 'echo', ['value' => 4]);
        $h->drain(1.0);
        self::assertSame('closed', $h->state());
        $h->destroy();
    }

    public function testDroppedHostsAreFreed(): void
    {
        if (($s = self::fixtures()) === null) {
            return;
        }
        [$engine, $guest, $descriptor, $contract] = $s;
        for ($i = 0; $i < 3; $i++) {
            $h = $engine->process($descriptor, $guest);
            $h->start();
            $h->call($contract, 'echo', ['value' => 5]);
            $h = null;
        }
        gc_collect_cycles();
        self::assertTrue(true);
    }
}
