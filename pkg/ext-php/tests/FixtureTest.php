<?php

declare(strict_types=1);

namespace ExtIO\Tests;

use ExtIO\Plugin;
use ExtIO\Plugin\Frames;
use ExtIO\Plugin\Guest;
use ExtIO\Plugin\Json;
use ExtIO\Plugin\Limiter;
use ExtIO\Plugin\Link;
use ExtIO\Plugin\ProtocolError;

/**
 * The shared fixtures for progress events and reverse calls (lib/plugin/testdata/v1), which the
 * Go contract computes and every SDK reproduces: frames, the sender's rate limiter and the wire
 * model.
 */
final class FixtureTest extends TestCase
{
    public const FIXTURES = __DIR__ . '/../../../lib/plugin/testdata/v1';

    private int $count = 0;
    /** @var list<string> */
    private array $first = [];

    public static function fixture(string $name): mixed
    {
        return Json::decode(file_get_contents(self::FIXTURES . '/' . $name));
    }

    public function setUp(): void
    {
        $this->count = 0;
        $this->first = [];
    }

    private function note(string $section, \stdClass $case, string $what): void
    {
        $this->count++;
        if (\count($this->first) < 12) {
            $this->first[] = "$section: {$case->name}: $what";
        }
    }

    private function check(): void
    {
        self::assertSame(0, $this->count, $this->count . " mismatches, the first:\n" . implode("\n", $this->first));
    }

    private static function accepts(callable $judge): bool
    {
        try {
            $judge();
            return true;
        } catch (ProtocolError | \InvalidArgumentException) {
            return false;
        }
    }

    public function testFramesAreJudgedAsTheGoContractJudgesThem(): void
    {
        $root = self::fixture('frame-cases.json');
        $total = 0;
        $sections = [
            'progress' => static fn (string $raw, string $id) => Frames::validateProgress(Json::decode($raw)),
            'reverseRequest' => static fn (string $raw, string $id) => Frames::validateReverseRequest(Json::decode($raw)),
            'reverseResponse' => static fn (string $raw, string $id) => Frames::validateReverseResponse(Json::decode($raw), $id),
        ];
        foreach ($sections as $section => $judge) {
            $cases = $root->{$section};
            $valid = \count(array_filter($cases, static fn ($c) => $c->valid));
            self::assertTrue(\count($cases) >= 20 && $valid >= 5 && \count($cases) - $valid >= 5, "$section proves little");
            foreach ($cases as $case) {
                $total++;
                $got = self::accepts(static fn () => $judge($case->frame, Json::get($case, 'id', '')));
                if ($got !== $case->valid) {
                    $this->note($section, $case, 'accepted ' . var_export($got, true) . ': ' . $case->frame);
                }
            }
        }
        self::assertTrue($total >= 500);
        $this->check();
    }

    public function testTheRateLimiterReproducesEveryStep(): void
    {
        $cases = self::fixture('progress-limiter-cases.json')->cases;
        self::assertTrue(\count($cases) >= 10);
        foreach ($cases as $case) {
            $limiter = new Limiter($case->eventsPerSecond, $case->bytesPerSecond, 0);
            foreach ($case->steps as $i => $step) {
                $got = $limiter->admit($step->textBytes, $step->atMs * 1_000_000);
                $want = $step->admitted;
                if (($got !== null) !== $want || ($want && $got !== Json::get($step, 'dropped', 0))) {
                    $this->note('limiter', $case, "step $i: " . var_export($got, true));
                }
            }
            if ($limiter->pending() !== $case->pending) {
                $this->note('limiter', $case, 'pending count');
            }
        }
        $this->check();
    }

    public function testTheWireModelIsJudgedAsTheGoContractJudgesIt(): void
    {
        $root = self::fixture('model-cases.json');
        self::assertTrue(\count($root->descriptors) > 100);
        foreach ($root->descriptors as $case) {
            $got = self::accepts(static fn () => Plugin\validateDescriptor(Json::decode($case->descriptor)));
            if ($got !== $case->valid) {
                $this->note('descriptor', $case, 'accepted ' . var_export($got, true) . ': ' . $case->descriptor);
            }
        }
        $selected = $root->requestDescriptor;
        Plugin\validateDescriptor($selected);
        self::assertTrue(\count($root->requests) > 100);
        foreach ($root->requests as $case) {
            $ran = [];
            $guest = new Guest($selected, static function () use (&$ran) {
                $ran[] = Plugin\currentCall();
                return null;
            });
            $link = new Link(fopen('php://memory', 'r+b'), fopen('php://memory', 'w+b'));
            try {
                $guest->invoke(Json::decode($case->request), $link);
            } catch (ProtocolError) {
            }
            if (($ran !== []) !== $case->valid) {
                $this->note('request', $case, 'accepted ' . var_export($ran !== [], true) . ': ' . $case->request);
                continue;
            }
            if ($ran === []) {
                continue;
            }
            // The call gets a reporter when its operation declared progress and the request
            // carries a rate; the rate has its defaults filled in.
            $limits = $ran[0]->progressLimits;
            if (($limits !== null) !== (bool) Json::get($case, 'reporter', false)) {
                $this->note('request', $case, 'reporter ' . var_export($limits !== null, true));
            } elseif ($limits !== null && ($limits->eventsPerSecond !== $case->events || $limits->bytesPerSecond !== $case->bytes)) {
                $this->note('request', $case, "rate {$limits->eventsPerSecond} {$limits->bytesPerSecond}");
            }
        }
        $this->check();
    }

    public function testTheConformanceDescriptorsLoad(): void
    {
        foreach (['descriptor.json', 'frames-descriptor.json', 'descriptor-frames.json'] as $name) {
            Plugin\loadDescriptor(self::FIXTURES . '/' . $name);
        }
        // invalid.json lists documents that every decoder refuses.
        foreach (self::fixture('invalid.json') as $raw) {
            self::assertThrows(ProtocolError::class, static fn () => Json::decode($raw), $raw);
        }
    }
}
