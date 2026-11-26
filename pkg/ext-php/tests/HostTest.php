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
        self::assertThrows(\FFI\Exception::class, static fn () => new Engine('/does/not/exist/libext_plugin_host.so'));
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
            'apiVersion' => 'ext.io.pc/v1', 'id' => 'r1', 'plugin' => $descriptor->identity,
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

    /** The engine and both scripted guests, or null when they are not set. */
    private static function scripted(): ?array
    {
        $engine = self::engine('EXT_REVERSE_TEST_GUEST', 'EXT_REGISTRY_TEST_GUEST');
        if ($engine === null) {
            return null;
        }
        return [$engine, (string) getenv('EXT_REVERSE_TEST_GUEST'), (string) getenv('EXT_REGISTRY_TEST_GUEST')];
    }

    private static function output(string ...$command): string
    {
        $out = shell_exec(implode(' ', array_map('escapeshellarg', $command)));
        self::assertTrue(\is_string($out) && $out !== '', implode(' ', $command));
        return $out;
    }

    /** @return array{0: mixed, 1: list<mixed>} the reply the scripted guest got, and what the handler saw */
    private static function reverseReply(Engine $engine, string $guest, string $mode, bool $serve = true): array
    {
        $handled = [];
        $h = $engine->process(self::output($guest, '--descriptor'), $guest, ['--guest', $mode]);
        if ($serve) {
            $h->serve(
                static fn (\ExtIO\Host\ReverseCall $call): bool => $call->remaining > 0 && Json::get($call->request, 'operation') !== 'secret',
                static function (\ExtIO\Host\ReverseCall $call) use (&$handled): mixed {
                    $handled[] = $call->request;
                    return match (Json::get($call->request, 'operation')) {
                        'sign' => 'signed',
                        'empty' => null,
                        'errok' => throw new RemoteError('policy', 'refused for you', 5),
                        default => throw new \RuntimeException('a private failure'),
                    };
                },
                timeout: 0.15,
                maxPayloadBytes: 64,
            );
        }
        $h->start(5.0);
        $reply = Json::get($h->call(['name' => 'reverse.test', 'version' => 'v1'], 'run', new \stdClass(), 5.0), 'reply');
        $h->destroy();
        return [$reply, $handled];
    }

    public function testReverseCallsAreServedOnTheCallingThread(): void
    {
        if (($s = self::scripted()) === null) {
            return;
        }
        [$engine, $guest] = $s;
        [$reply, $handled] = self::reverseReply($engine, $guest, 'call:sign');
        self::assertSame('signed', Json::get($reply, 'payload'));
        self::assertSame('signing', Json::get($handled[0], 'surface'));
        self::assertSame('1', Json::get($handled[0], 'parent'));
        self::assertNull(Json::get(self::reverseReply($engine, $guest, 'call:empty')[0], 'payload'));
        self::assertJson(['code' => 'policy', 'message' => 'refused for you', 'retryAfterMilliseconds' => 5],
            Json::get(self::reverseReply($engine, $guest, 'call:errok')[0], 'error'));
        self::assertSame('operation_failed', Json::get(Json::get(self::reverseReply($engine, $guest, 'call:fail')[0], 'error'), 'code'));
    }

    public function testReversePolicyAndRefusals(): void
    {
        if (($s = self::scripted()) === null) {
            return;
        }
        [$engine, $guest] = $s;
        $code = static fn (array $r): string => Json::get(Json::get($r[0], 'error'), 'code');
        $secret = self::reverseReply($engine, $guest, 'call:secret');
        self::assertSame('denied', $code($secret));
        self::assertSame([], $secret[1]);
        self::assertSame('reverse_undeclared', $code(self::reverseReply($engine, $guest, 'call:nothere')));
        self::assertSame('reverse_unavailable', $code(self::reverseReply($engine, $guest, 'call:sign', false)));
    }

    public function testRequestsBetweenCallsWaitForServeReverse(): void
    {
        if (($s = self::scripted()) === null) {
            return;
        }
        [$engine, $guest] = $s;
        $handled = [];
        $h = $engine->process(self::output($guest, '--descriptor'), $guest, ['--guest', 'idle']);
        $h->serve(static fn (): bool => true, static function (\ExtIO\Host\ReverseCall $call) use (&$handled): string {
            $handled[] = $call->request;
            return 'signed';
        });
        $h->start(5.0);
        self::assertTrue($h->serveReverse(5.0));
        self::assertNull(Json::get($handled[0], 'parent'));
        self::assertSame('signed', Json::get(Json::get($h->call(['name' => 'reverse.test', 'version' => 'v1'], 'run', new \stdClass(), 5.0), 'reply'), 'payload'));
        self::assertFalse($h->serveReverse(0.05));
        $h->destroy();
    }

    private static function relay(Engine $engine, string $guest, \ExtIO\Host\Registry $registry, string $name, bool $denyForward = false, bool $denyReverse = false): \ExtIO\Host\Host
    {
        $cases = realpath(self::FIXTURES . '/registry-cases.json');
        $h = $engine->process(self::output($guest, '--descriptor', $cases, $name), $guest, ['--guest', $cases, $name],
            authorize: static fn (): bool => !$denyForward);
        $h->attach($registry, static fn (): bool => !$denyReverse);
        $h->start(5.0);
        return $h;
    }

    public function testRegistryAnswersTheSharedCases(): void
    {
        if (($s = self::scripted()) === null) {
            return;
        }
        [$engine, , $guest] = $s;
        $cases = self::fixture('registry-cases.json');
        $guests = (array) $cases->guests;
        $contract = static fn (string $name): array => ['name' => $guests[$name], 'version' => 'v1'];
        foreach ($cases->cases as $c) {
            $registry = new \ExtIO\Host\Registry($engine);
            $hosts = [];
            foreach (array_keys($guests) as $name) {
                $hosts[$name] = self::relay($engine, $guest, $registry, $name,
                    \in_array($name, $c->forwardDeny ?? [], true), \in_array($name, $c->reverseDeny ?? [], true));
                $registry->register($contract($name), $hosts[$name]);
            }
            foreach ($cases->edges as $e) {
                $registry->route($e->from, $e->to);
            }
            foreach ($c->steps as $i => $step) {
                if (isset($step->unregister)) {
                    $registry->unregister($contract($step->unregister));
                    continue;
                }
                $got = $hosts[$step->call]->call($contract($step->call), $step->operation, $step->payload ?? null,
                    ($step->timeoutMs ?? 5000) / 1000.0);
                if (property_exists($step, 'result')) {
                    self::assertJson($step->result, $got, "{$c->name} step $i");
                } else {
                    $code = Json::get(Json::get($got, 'error') ?? new \stdClass(), 'code');
                    self::assertTrue(\in_array($code, $step->resultError, true), "{$c->name} step $i: " . Json::encode($got));
                }
            }
            foreach ($hosts as $h) {
                $h->destroy();
            }
        }
    }

    public function testRegistryRefusesWhatItCannotRoute(): void
    {
        if (($s = self::scripted()) === null) {
            return;
        }
        [$engine, , $guest] = $s;
        $registry = new \ExtIO\Host\Registry($engine);
        $b = self::relay($engine, $guest, $registry, 'b');
        $status = static function (callable $f): string {
            try {
                $f();
            } catch (HostError $e) {
                return $e->status;
            }
            return 'nothing thrown';
        };
        $b1 = ['name' => 'registry.b', 'version' => 'v1'];
        self::assertSame('invalid', $status(static fn () => $registry->register(['name' => 'Not A Name', 'version' => 'v1'], $b)));
        self::assertSame('unsupported', $status(static fn () => $registry->register(['name' => 'registry.other', 'version' => 'v1'], $b)));
        $registry->register($b1, $b);
        self::assertSame('exists', $status(static fn () => $registry->register($b1, $b)));
        $from = ['name' => 'registry.ask', 'version' => 'v1', 'operation' => 'echo-b'];
        $to = ['name' => 'registry.b', 'version' => 'v1', 'operation' => 'echo'];
        $registry->route($from, $to);
        self::assertSame('exists', $status(static fn () => $registry->route($from, $to)));
        self::assertSame('invalid', $status(static fn () => $registry->route(['operation' => 'other'] + $from, ['operation' => 'Bad Name'] + $to)));
        self::assertSame('not_found', $status(static fn () => $registry->unregister(['name' => 'registry.x', 'version' => 'v1'])));
        $registry->unregister($b1);
        $registry->register($b1, $b);
        $b->destroy();
        // A destroyed host has left the registry.
        self::assertSame('not_found', $status(static fn () => $registry->unregister($b1)));
        // Dropping the registry before its hosts, or the reverse, frees both safely.
        $other = new \ExtIO\Host\Registry($engine);
        $source = self::relay($engine, $guest, $other, 'a');
        $other = null;
        $source = null;
        gc_collect_cycles();
    }
    public function testPackageWriter(): void
    {
        $engine = self::engine();
        if ($engine === null) { return; }
        $home = sys_get_temp_dir() . '/ext-php-writer-' . bin2hex(random_bytes(6));
        mkdir($home, 0700);
        $path = $home . '/.package-write.lock';
        try {
            $writer = $engine->packageWriter($home);
            self::assertThrows(\Error::class, static fn() => clone $writer);
            $error = self::assertThrows(HostError::class, fn() => $engine->packageWriter($home));
            self::assertSame('exists', $error->status);
            $code = 'require $argv[1]; try { $w=(new ExtIO\\Host\\Engine())->packageWriter($argv[2]); exit(3); } catch (ExtIO\\Host\\HostError $e) { exit($e->status === "exists" ? 0 : 4); }';
            [$status, $out, $err] = Subprocess::run([PHP_BINARY, '-r', $code, __DIR__ . '/../autoload.php', $home], '');
            self::assertSame(0, $status, $out . $err);
            $writer->close();
            $writer->close();
            self::assertFalse(is_dir($path));
            $error = self::assertThrows(\RuntimeException::class, fn() => $engine->withPackageWriter($home, static function (): void {
                throw new \RuntimeException('application failure');
            }));
            self::assertSame('application failure', $error->getMessage());
            self::assertFalse(is_dir($path));
            $writer = $engine->packageWriter($home);
            rename($path, $home . '/old-writer');
            $other = $engine->packageWriter($home);
            $error = self::assertThrows(HostError::class, fn() => $writer->close());
            self::assertSame('mismatch', $error->status);
            self::assertTrue(is_dir($path));
            $other->close();
            rmdir($home . '/old-writer');
            mkdir($path, 0700); // CLI-created or stale directory: never steal it.
            $error = self::assertThrows(HostError::class, fn() => $engine->packageWriter($home));
            self::assertSame('exists', $error->status);
            rmdir($path);
            self::assertSame(7, $engine->withPackageWriter($home, static fn() => 7));
        } finally {
            unset($writer, $other);
            if (is_dir($path)) { rmdir($path); }
            if (is_dir($home . '/old-writer')) { rmdir($home . '/old-writer'); }
            rmdir($home);
        }
    }

}
