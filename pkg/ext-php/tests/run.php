<?php

declare(strict_types=1);

// A small test runner with no dependencies: `php tests/run.php [filter]` runs every public
// test* method of the classes in tests/*Test.php and exits non-zero when one fails.

require __DIR__ . '/../autoload.php';
require __DIR__ . '/TestCase.php';
require __DIR__ . '/Subprocess.php';

error_reporting(E_ALL);
ini_set('memory_limit', '2G'); // the frame limit tests hold several 24 MiB strings
set_error_handler(static function (int $severity, string $message, string $file, int $line): bool {
    if (!(error_reporting() & $severity)) {
        return false;
    }
    throw new ErrorException($message, 0, $severity, $file, $line);
});

$filter = $argv[1] ?? '';
$failed = 0;
$passed = 0;
$started = microtime(true);
foreach (glob(__DIR__ . '/*Test.php') as $file) {
    require_once $file;
    $class = 'ExtIO\\Tests\\' . basename($file, '.php');
    foreach (get_class_methods($class) as $method) {
        if (!str_starts_with($method, 'test') || ($filter !== '' && !str_contains("$class::$method", $filter))) {
            continue;
        }
        $test = new $class();
        try {
            $test->setUp();
            $test->{$method}();
            $passed++;
        } catch (Throwable $e) {
            $failed++;
            fwrite(STDERR, sprintf("FAIL %s::%s\n  %s: %s\n  at %s:%d\n", basename($file, '.php'), $method, get_class($e), $e->getMessage(), $e->getFile(), $e->getLine()));
            foreach (array_slice($e->getTrace(), 0, 4) as $frame) {
                if (isset($frame['file'])) {
                    fwrite(STDERR, sprintf("    %s:%d\n", $frame['file'], $frame['line']));
                }
            }
        } finally {
            $test->tearDown();
        }
    }
}
printf("%d passed, %d failed in %.1fs (PHP %s)\n", $passed, $failed, microtime(true) - $started, PHP_VERSION);
exit($failed === 0 && $passed > 0 ? 0 : 1);
