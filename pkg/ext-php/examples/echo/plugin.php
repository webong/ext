<?php

declare(strict_types=1);

// An example guest: echoes payloads, fails in public, reports progress and answers health
// checks. Run it under a host, or by hand:
//
//   printf '%s\n' '{"apiVersion":"ext.plugin/v1","id":"hello","operation":"plugin.hello","deadline":"2999-01-01T00:00:00Z"}' \
//     '{"apiVersion":"ext.plugin/v1","id":"1","plugin":{"id":"example/php-echo","revision":"1"},"contract":{"name":"example.echo","version":"v1"},"operation":"echo","deadline":"2999-01-01T00:00:00Z","payload":{"hi":[]}}' \
//     | php plugin.php

require __DIR__ . '/../../autoload.php'; // or Composer's vendor/autoload.php

use ExtIO\Plugin;
use ExtIO\Plugin\Capability\Router;
use ExtIO\Plugin\Progress;
use ExtIO\Plugin\RemoteError;

$health = (new Router())->health(static fn () => ['status' => 'healthy', 'ready' => true]);

Plugin\serveStdio(__DIR__ . '/plugin.json', static function (string $contract, string $operation, mixed $payload) use ($health): mixed {
    if ($contract === 'ext.health') {
        return $health($contract, $operation, $payload);
    }
    switch ($operation) {
        case 'echo':
            return $payload; // {} stays {}, [] stays []
        case 'busy':
            throw new RemoteError('busy', 'try later', 250);
        case 'count':
            $call = Plugin\currentCall();
            $n = \is_int($payload) ? $payload : 3;
            for ($i = 1; $i <= $n; $i++) {
                try {
                    $call->progress(Progress::fraction($i, $n));
                } catch (Plugin\FrameError) {
                    break; // the host asked for no events
                }
            }
            return $n;
    }
    throw new RemoteError('unsupported', 'operation is not implemented');
});
