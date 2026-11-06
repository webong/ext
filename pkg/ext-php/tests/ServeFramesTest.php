<?php

declare(strict_types=1);

namespace ExtIO\Tests;

use ExtIO\Plugin;
use ExtIO\Plugin\Frames;
use ExtIO\Plugin\Guest;
use ExtIO\Plugin\Json;

require_once __DIR__ . '/serve_frames_guest.php';

/** Serves the frames guest in a child process and reads what it writes, as a host would. */
final class FramesHost
{
    private Subprocess $p;
    private int $next = 0;

    public function __construct(bool $first = true)
    {
        $this->p = new Subprocess([\PHP_BINARY, __DIR__ . '/serve_frames_guest.php']);
        if ($first) {
            $this->send(ServeFramesTest::hello());
        }
    }

    public function send(mixed $frame): void
    {
        $this->p->write(Json::encode($frame) . "\n");
    }

    /** The next frame the guest wrote, or null when none comes within $wait seconds. */
    public function recv(float $wait = 5.0): mixed
    {
        $line = $this->p->line($wait);
        return $line === null ? null : Json::decode($line);
    }

    public function request(string $operation, mixed $payload = null, bool $progress = false): string
    {
        $this->next++;
        $id = (string) $this->next;
        $r = [
            'apiVersion' => 'ext.plugin/v1',
            'id' => $id,
            'plugin' => ['id' => 'php/frames', 'revision' => 'r1'],
            'contract' => ['name' => 'test.frames', 'version' => 'v1'],
            'operation' => $operation,
            'payload' => $payload,
            'deadline' => GuestTest::FUTURE,
        ];
        if ($progress) {
            $r['progress'] = ['eventsPerSecond' => 5];
        }
        $this->send($r);
        return $id;
    }

    /** @return array{list<\stdClass>, \stdClass} the frames before the response to $id, and it */
    public function untilResponse(string $id): array
    {
        $before = [];
        while (true) {
            $frame = $this->recv();
            TestCase::assertNotNull($frame, 'the guest wrote nothing; stderr: ' . $this->p->errors);
            if (!Json::has($frame, 'frame') && $frame->id === $id) {
                return [$before, $frame];
            }
            $before[] = $frame;
        }
    }

    /** Ends the host's side and waits for the guest to end; what ended the session, or null. */
    public function finish(): ?string
    {
        $this->p->closeInput();
        $code = $this->p->wait();
        if ($code === 0) {
            return null;
        }
        TestCase::assertSame(3, $code, $this->p->errors);
        preg_match('/ended: (\S+):/', $this->p->errors, $m);
        return $m[1] ?? $this->p->errors;
    }
}

final class ServeFramesTest extends TestCase
{
    public static function hello(array $kw = []): array
    {
        return array_merge(['apiVersion' => 'ext.plugin/v1', 'id' => 'hello', 'operation' => 'plugin.hello', 'deadline' => GuestTest::FUTURE], $kw);
    }

    private static function answer(string $id, array $body): array
    {
        return ['frame' => 'reverse_response', 'apiVersion' => 'ext.plugin/v1', 'id' => $id] + $body;
    }

    private static function started(): FramesHost
    {
        $host = new FramesHost();
        self::assertSame('hello', $host->recv()->id);
        return $host;
    }

    public function testTheDescriptorDeclaresProgressAndReverse(): void
    {
        $host = new FramesHost();
        $served = $host->recv()->payload;
        $ops = [];
        foreach ($served->contracts[0]->operations as $op) {
            $ops[$op->name] = $op;
        }
        self::assertTrue($ops['events']->progress);
        self::assertFalse(property_exists($ops['quiet'], 'progress'));
        self::assertSame('host.svc', $served->reverse[0]->name);
        self::assertNull($host->finish());
    }

    public function testEventsComeInOrderBeforeTheResponse(): void
    {
        $host = self::started();
        $id = $host->request('events', null, true);
        [$events, $response] = $host->untilResponse($id);
        self::assertSame(2, \count($events));
        foreach ($events as $event) {
            [$parent] = Frames::validateProgress($event);
            self::assertSame($id, $parent);
        }
        self::assertSame(['log', 'a'], [$events[0]->kind, $events[0]->text]);
        self::assertSame(['progress', 1, 2], [$events[1]->kind, $events[1]->completed, $events[1]->total]);
        self::assertJson(['sent' => [true, true]], $response->payload);
        self::assertNull($host->recv(0.1), 'a frame after the response');
        self::assertNull($host->finish());
    }

    public function testAFloodIsLimitedCountedAndClosedWithAMarker(): void
    {
        $host = self::started();
        $id = $host->request('flood', null, true);
        [$events, $response] = $host->untilResponse($id);
        $dropped = $response->payload->dropped;
        $delivered = \count(array_filter($events, static fn ($e) => property_exists($e, 'text')));
        $reported = array_sum(array_map(static fn ($e) => $e->dropped ?? 0, $events));
        self::assertTrue($delivered >= 10 && $delivered + $dropped === 300 && $reported === $dropped, "$delivered $dropped $reported");
        // The drops still unreported when the handler returned are in a marker with only a kind and a count.
        $last = end($events);
        self::assertTrue(!property_exists($last, 'text') && $last->kind === 'log' && $last->dropped > 0, Json::encode($last));
        self::assertNull($host->finish());
    }

    public function testAnOperationThatDidNotDeclareProgressCannotReport(): void
    {
        $host = self::started();
        foreach ([false, true] as $progress) { // a rate for an operation that declared none is not a reporter either
            $id = $host->request('quiet', null, $progress);
            [$events, $response] = $host->untilResponse($id);
            self::assertSame([], $events);
            self::assertTrue($response->payload->unsupported);
        }
        // A declared operation the host asked no events of cannot report.
        $id = $host->request('events');
        [$events, $response] = $host->untilResponse($id);
        self::assertSame([], $events);
        self::assertSame('operation_failed', $response->error->code);
        self::assertNull($host->finish());
    }

    public function testAnEventThatBreaksTheRulesIsRefusedAndNeverSent(): void
    {
        $host = self::started();
        $id = $host->request('invalid', null, true);
        [$events, $response] = $host->untilResponse($id);
        self::assertSame([], $events);
        self::assertSame(array_fill(0, 9, 'invalid'), $response->payload->codes);
        self::assertNull($host->finish());
    }

    public function testAReverseCallNamesTheCallAndReturnsTheAnswer(): void
    {
        $host = self::started();
        $id = $host->request('ask', ['op' => 'sign', 'payload' => ['n' => 1]]);
        $request = $host->recv();
        Frames::validateReverseRequest($request);
        self::assertSame(['reverse_request', 'g1', $id, 'sign'], [$request->frame, $request->id, $request->parent, $request->operation]);
        self::assertJson(['name' => 'host.svc', 'version' => 'v1'], $request->contract);
        self::assertJson(['n' => 1], $request->payload);
        $host->send(self::answer('g1', ['payload' => 'signed']));
        self::assertJson(['result' => 'signed'], $host->untilResponse($id)[1]->payload);
        // The host's error comes back with its code, and ids go on counting.
        $id = $host->request('ask', ['op' => 'sign']);
        $request = $host->recv();
        self::assertSame('g2', $request->id);
        $host->send(self::answer('g2', ['error' => ['code' => 'denied', 'message' => 'reverse call refused']]));
        self::assertJson(['code' => 'denied'], $host->untilResponse($id)[1]->payload);
        // An operation the descriptor does not list is refused without a frame.
        $id = $host->request('ask', ['op' => 'nothere']);
        [$before, $response] = $host->untilResponse($id);
        self::assertSame([], $before);
        self::assertJson(['code' => 'reverse_undeclared'], $response->payload);
        self::assertNull($host->finish());
    }

    public function testOnlyOneReverseCallAtATimeAndNoSessionCaller(): void
    {
        $host = self::started();
        $id = $host->request('session');
        [, $response] = $host->untilResponse($id);
        self::assertJson(['session' => false, 'reverse' => true, 'concurrent' => false], $response->payload);
        self::assertNull($host->finish());
    }

    public function testACallerEndsWithItsCall(): void
    {
        $host = self::started();
        $host->untilResponse($host->request('stale'));
        $id = $host->request('stale-use');
        [$before, $response] = $host->untilResponse($id);
        self::assertSame([], $before);
        self::assertJson(['frame' => 'closed'], $response->payload);
        self::assertNull($host->finish());
    }

    public function testWhatTheHostMayNotDoEndsTheSession(): void
    {
        $violations = [
            'a second request during a call' => null,
            'an unknown frame' => ['frame' => 'mystery'],
            'an answer to a request never issued' => self::answer('g99', ['payload' => 1]),
            'a progress frame from the host' => ['frame' => 'progress', 'apiVersion' => 'ext.plugin/v1', 'parent' => '1', 'kind' => 'log'],
            'a malformed answer to the request waiting' => self::answer('g1', ['payload' => 1, 'extra' => 1]),
            'an answer with both payload and error' => self::answer('g1', ['payload' => 1, 'error' => ['code' => 'x', 'message' => 'm']]),
            'an answer with neither payload nor error' => self::answer('g1', []),
            'a frame that is not an object' => 7,
        ];
        foreach ($violations as $what => $frame) {
            $host = self::started();
            $id = $host->request('ask', ['op' => 'sign']);
            self::assertSame('g1', $host->recv()->id);
            if ($frame === null) {
                $host->request('events', null, true);
            } else {
                $host->send($frame);
            }
            // The call that was in flight is never answered.
            while (($left = $host->recv(0.5)) !== null) {
                self::assertTrue(property_exists($left, 'frame') || $left->id !== $id, "$what: answered " . Json::encode($left));
            }
            self::assertSame('ExtIO\Plugin\ProtocolError', $host->finish(), $what);
        }
    }

    public function testAConnectionThatEndsReleasesAWaitingHandler(): void
    {
        $host = self::started();
        $id = $host->request('ask', ['op' => 'sign']);
        self::assertSame('g1', $host->recv()->id);
        // The host closes its end: the handler waiting for the answer is released, and the call is
        // still answered (the host may be reading), then the session ends.
        $outcome = $host->finish();
        [, $response] = $host->untilResponse($id);
        self::assertJson(['frame' => 'closed'], $response->payload);
        self::assertNull($outcome);
    }

    public function testAGuestInvokedDirectlyHasNoFrames(): void
    {
        $guest = new Guest(\ServeFramesGuest::descriptor(), \ServeFramesGuest::handle(...), redactErrors: true);
        $request = static fn (string $operation) => self::obj([
            'apiVersion' => 'ext.plugin/v1', 'id' => '1', 'plugin' => ['id' => 'php/frames', 'revision' => 'r1'],
            'contract' => ['name' => 'test.frames', 'version' => 'v1'], 'operation' => $operation,
            'payload' => new \stdClass(), 'deadline' => GuestTest::FUTURE, 'progress' => new \stdClass(),
        ]);
        self::assertSame('operation_failed', $guest->invoke($request('events'))['error']['code']); // needs a reporter
        self::assertSame('operation_failed', $guest->invoke($request('ask'))['error']['code']); // needs a reverse caller
        self::assertJson(['session' => false, 'reverse' => false, 'concurrent' => false], $guest->invoke($request('session'))['payload']);
    }

    public function testHelloMayNotCarryARateOrAParent(): void
    {
        foreach ([['progress' => new \stdClass()], ['parent' => '1']] as $extra) {
            $host = new FramesHost(false);
            $host->send(self::hello($extra));
            self::assertNull($host->recv(0.5));
            self::assertSame('ExtIO\Plugin\ProtocolError', $host->finish(), Json::encode($extra));
        }
    }

    public function testTextIsCutOnCharacters(): void
    {
        $pieces = Plugin\splitProgressText(str_repeat('é', 1500) . "\xED\xA0\x80"); // 3000 bytes, then three bytes that are not UTF-8
        self::assertSame(str_repeat('é', 1500) . "\u{FFFD}\u{FFFD}\u{FFFD}", implode('', $pieces));
        foreach ($pieces as $p) {
            self::assertTrue(\strlen($p) <= 2048 && preg_match('//u', $p) === 1);
        }
        self::assertSame(2, \count($pieces));
        self::assertSame([], Plugin\splitProgressText(''));
        self::assertSame(['abc'], Plugin\splitProgressText('abc'));
    }
}
