<?php

declare(strict_types=1);

// The frames conformance guest (lib/plugin/testdata/v1/frames-descriptor.json,
// docs/plugin-guest-frames.md): reports progress and calls its host. It reads inline, so it
// answers the operations that need a reader of its own (idle, collect, burst) with the public
// error "unsupported".

require __DIR__ . '/../autoload.php';

use ExtIO\Plugin;
use ExtIO\Plugin\CallInfo;
use ExtIO\Plugin\FrameError;
use ExtIO\Plugin\Progress;
use ExtIO\Plugin\RemoteError;

const HOST = ['host.svc', 'v1'];

/** How a reverse call came out, in the shape the suite reads. */
function outcome(callable $call): array
{
    try {
        return ['result' => $call()];
    } catch (RemoteError $e) {
        $error = ['code' => $e->getCode(), 'message' => $e->getMessage()];
        if ($e->retryAfterMs) {
            $error['retry'] = $e->retryAfterMs;
        }
        return ['error' => $error];
    } catch (FrameError $e) {
        return ['error' => ['other' => \in_array($e->getCode(), ['closed', 'deadline', 'canceled'], true) ? $e->getCode() : 'failed']];
    }
}

function report(CallInfo $call, Progress $event): ?string
{
    try {
        return $call->progress($event);
    } catch (FrameError) {
        return null;
    }
}

function handle(string $contract, string $operation, mixed $payload): mixed
{
    $call = Plugin\currentCall();
    $payload = $payload instanceof stdClass ? $payload : new stdClass();
    switch ($operation) {
        case 'stream':
            report($call, Progress::fraction(1, 3));
            report($call, Progress::out('hello'));
            report($call, Progress::err('oops'));
            return ['done' => true];
        case 'flood':
            $dropped = 0;
            for ($i = 0, $n = $payload->count ?? 300; $i < $n; $i++) {
                if (report($call, Progress::log('x')) === Plugin\DROPPED) {
                    $dropped++;
                }
            }
            return ['dropped' => $dropped];
        case 'limits':
            $limits = $call->progressLimits;
            return [
                'declared' => $limits !== null,
                'events' => $limits?->eventsPerSecond ?? 0,
                'bytes' => $limits?->bytesPerSecond ?? 0,
            ];
        case 'split':
            $pieces = Plugin\splitProgressText($payload->text);
            foreach ($pieces as $piece) {
                report($call, Progress::out($piece));
            }
            return ['pieces' => \count($pieces)];
        case 'plain':
            try {
                $call->progress(Progress::log('never sent'));
                $unsupported = false;
            } catch (FrameError $e) {
                $unsupported = $e->getCode() === 'unsupported';
            }
            return ['unsupported' => $unsupported, 'limits' => $call->progressLimits !== null];
        case 'call':
            if ($call->reverse === null) {
                return ['error' => ['other' => 'unsupported']];
            }
            return outcome(static fn () => $call->reverse->call(HOST, $payload->op, $payload->payload ?? null));
        case 'twice':
            try {
                $first = $call->reverse->call(HOST, 'sign', 1);
                $second = $call->reverse->call(HOST, 'sign', 2);
            } catch (RemoteError | FrameError $e) {
                throw new RuntimeException('a reverse call failed: ' . $e->getMessage());
            }
            return ['first' => $first, 'second' => $second];
        case 'idle':
        case 'collect':
        case 'burst':
            throw new RemoteError('unsupported', 'not supported here');
    }
    throw new RuntimeException('unknown operation');
}

Plugin\serveStdio(__DIR__ . '/../../../lib/plugin/testdata/v1/frames-descriptor.json', handle(...), redactErrors: true);
