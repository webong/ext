<?php

declare(strict_types=1);

// The guest the Go conformance suite runs (pkg/ext-go/engine/plugintest, TestPHPGuest).

require __DIR__ . '/../autoload.php';

use ExtIO\Plugin;

Plugin\serveStdio(__DIR__ . '/descriptor.json', static function (string $contract, string $operation, mixed $payload): mixed {
    if ($operation === 'wait') {
        sleep(60);
    }
    if ($operation === 'private-error') {
        throw new RuntimeException('secret diagnostic');
    }
    if ($operation === 'public-error') {
        throw new Plugin\RemoteError('busy', 'try later', 10);
    }
    return $payload;
}, redactErrors: true);
