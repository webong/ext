<?php

declare(strict_types=1);

// The guest that ServeFramesTest drives as a host on pipes. It exits 3 and names the exception
// on standard error when the session ends with one.

require __DIR__ . '/../autoload.php';

use ExtIO\Plugin;
use ExtIO\Plugin\FrameError;
use ExtIO\Plugin\Progress;
use ExtIO\Plugin\RemoteError;

final class ServeFramesGuest
{
    public const HOST = ['host.svc', 'v1'];

    public static ?Plugin\ReverseCaller $kept = null;

    public static function descriptor(): array
    {
        return [
            'apiVersion' => 'ext.plugin/v1',
            'identity' => ['id' => 'php/frames', 'revision' => 'r1'],
            'contracts' => [[
                'name' => 'test.frames',
                'version' => 'v1',
                'operations' => [
                    ['name' => 'events', 'progress' => true],
                    ['name' => 'flood', 'progress' => true],
                    ['name' => 'invalid', 'progress' => true],
                    ['name' => 'quiet'],
                    ['name' => 'ask'],
                    ['name' => 'session'],
                    ['name' => 'stale'],
                    ['name' => 'stale-use'],
                ],
            ]],
            'reverse' => [['name' => 'host.svc', 'version' => 'v1', 'operations' => [['name' => 'sign']]]],
        ];
    }

    public static function handle(string $contract, string $operation, mixed $payload): mixed
    {
        $call = Plugin\currentCall();
        switch ($operation) {
            case 'events':
                $first = $call->progress(Progress::log('a'));
                $second = $call->progress(Progress::fraction(1, 2));
                return ['sent' => [$first === Plugin\SENT, $second === Plugin\SENT]];
            case 'flood':
                $dropped = 0;
                for ($i = 0; $i < 300; $i++) {
                    if ($call->progress(Progress::log('x')) === Plugin\DROPPED) {
                        $dropped++;
                    }
                }
                return ['dropped' => $dropped];
            case 'quiet':
                try {
                    $call->progress(Progress::log('x'));
                } catch (FrameError $e) {
                    return ['unsupported' => $e->getCode() === 'unsupported'];
                }
                return ['unsupported' => false];
            case 'invalid':
                $codes = [];
                foreach ([
                    new Progress('mystery'),
                    Progress::fraction(3, 2),
                    Progress::log(str_repeat('x', 2049)),
                    new Progress('log', 'both'),
                    Progress::log("\xED\xA0\x80"), // a surrogate encoded in UTF-8 is not text
                    new Progress('progress', 'out'),
                    Progress::fraction(-1, 0),
                    Progress::fraction(true, 2),
                    null,
                ] as $event) {
                    try {
                        $call->progress($event);
                        $codes[] = 'sent';
                    } catch (FrameError $e) {
                        $codes[] = $e->getCode();
                    }
                }
                return ['codes' => $codes];
            case 'ask':
                try {
                    return ['result' => $call->reverse->call(self::HOST, $payload->op ?? 'sign', $payload->payload ?? null)];
                } catch (RemoteError $e) {
                    return ['code' => $e->getCode()];
                } catch (FrameError $e) {
                    return ['frame' => $e->getCode()];
                }
            case 'session':
                return ['session' => $call->sessionReverse !== null, 'reverse' => $call->reverse !== null, 'concurrent' => (bool) $call->reverse?->concurrent];
            case 'stale':
                self::$kept = $call->reverse;
                return null;
            case 'stale-use':
                // A request names the call it belongs to, so a caller kept past its call writes nothing.
                try {
                    self::$kept->call(self::HOST, 'sign', null);
                    return ['frame' => 'none'];
                } catch (FrameError $e) {
                    return ['frame' => $e->getCode()];
                }
        }
        throw new \LogicException($operation);
    }
}

if (realpath($_SERVER['SCRIPT_FILENAME'] ?? '') === __FILE__) {
    try {
        Plugin\serveStdio(ServeFramesGuest::descriptor(), ServeFramesGuest::handle(...), redactErrors: true);
    } catch (Throwable $e) {
        fwrite(STDERR, 'ended: ' . get_class($e) . ': ' . $e->getMessage() . "\n");
        exit(3);
    }
}
