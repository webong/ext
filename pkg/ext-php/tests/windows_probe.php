<?php

declare(strict_types=1);

// Temporary diagnostics for the Windows port of the PHP guest. Not part of the suite.

function say(string $m): void
{
    fwrite(STDERR, $m . "\n");
}

say('PHP ' . PHP_VERSION . ' ' . PHP_OS . ' ' . (PHP_ZTS ? 'ts' : 'nts') . ' ' . PHP_SAPI . ' ' . PHP_BINARY);

// 1. Duplicates of the standard streams.
foreach (['php://fd/0', 'php://fd/1', 'php://fd/2', 'php://stdout', 'php://stderr'] as $url) {
    $h = @fopen($url, 'wb');
    say("$url => " . ($h === false ? 'FAILED ' . json_encode(error_get_last()) : 'ok'));
    if ($h) {
        $s = fstat($h);
        say('   fstat dev=' . ($s['dev'] ?? '?') . ' ino=' . ($s['ino'] ?? '?') . ' mode=' . decoct($s['mode'] ?? 0));
        fclose($h);
    }
}
$s = fstat(STDERR);
say('STDERR fstat dev=' . $s['dev'] . ' ino=' . $s['ino']);

// 2. The stdout claim, in a child whose output goes to files.
$dir = sys_get_temp_dir() . '/ext-probe-' . bin2hex(random_bytes(4));
mkdir($dir);
$child = $dir . '/child.php';
file_put_contents($child, '<?php
require ' . var_export(dirname(__DIR__) . '/autoload.php', true) . ';
try {
    $out = ExtIO\Plugin\Stdio::claimStdout();
} catch (Throwable $e) {
    fwrite(STDERR, "CLAIM FAILED " . get_class($e) . ": " . $e->getMessage() . "\n");
    exit(7);
}
echo "echo\n";
print "print\n";
file_put_contents("php://stdout", "raw\n");
trigger_error("warn", E_USER_WARNING);
fwrite($out, "FRAME\n");
fclose($out);
');
foreach ([[], ['-d', 'display_errors=1']] as $extra) {
    $o = $dir . '/out.txt';
    $e = $dir . '/err.txt';
    $p = proc_open([PHP_BINARY, ...$extra, $child], [0 => ['pipe', 'r'], 1 => ['file', $o, 'wb'], 2 => ['file', $e, 'wb']], $pipes);
    fclose($pipes[0]);
    $code = proc_close($p);
    say('claim ' . json_encode($extra) . ': exit=' . $code);
    say('   stdout=' . json_encode(file_get_contents($o)));
    say('   stderr=' . json_encode(file_get_contents($e)));
}

// 3. Pipes to a child: blocking, select, exit codes.
$script = 'fwrite(STDOUT, "first\n"); fwrite(STDERR, "err\n"); usleep(1500000); fwrite(STDOUT, "late\n"); exit(3);';
$p = proc_open([PHP_BINARY, '-r', $script], [0 => ['pipe', 'r'], 1 => ['pipe', 'w'], 2 => ['pipe', 'w']], $pipes);
say('set_blocking(out,false)=' . var_export(stream_set_blocking($pipes[1], false), true));
say('set_blocking(err,false)=' . var_export(stream_set_blocking($pipes[2], false), true));
usleep(500000);
$t = microtime(true);
say('fgets(out) after 0.5s => ' . json_encode(fgets($pipes[1])) . sprintf(' in %.2fs', microtime(true) - $t));
$t = microtime(true);
say('fgets(out) again => ' . json_encode(fgets($pipes[1])) . sprintf(' in %.2fs', microtime(true) - $t));
$t = microtime(true);
say('fread(err) => ' . json_encode(fread($pipes[2], 100)) . sprintf(' in %.2fs', microtime(true) - $t));
$r = [$pipes[1], $pipes[2]];
$w = $x = null;
$t = microtime(true);
$n = @stream_select($r, $w, $x, 3);
say('stream_select => ' . var_export($n, true) . ' ready=' . count($r) . sprintf(' in %.2fs', microtime(true) - $t) . ' ' . json_encode(error_get_last()));
$st = proc_get_status($p);
say('status while running: ' . json_encode($st));
$until = microtime(true) + 10;
while (microtime(true) < $until) {
    $st = proc_get_status($p);
    if (!$st['running']) {
        break;
    }
    usleep(50000);
}
say('status after: ' . json_encode($st));
say('late => ' . json_encode(fread($pipes[1], 100)));
say('proc_close => ' . proc_close($p));

// 4. A child that never closes its stdin: does a blocking read of stdin end when the parent closes the pipe?
$p = proc_open([PHP_BINARY, '-r', 'echo json_encode(fgets(STDIN)), "\n"; echo json_encode(fgets(STDIN)), "\n";'], [0 => ['pipe', 'r'], 1 => ['pipe', 'w'], 2 => ['pipe', 'w']], $pipes);
fwrite($pipes[0], "hello\n");
fflush($pipes[0]);
fclose($pipes[0]);
$t = microtime(true);
say('blocking read of child output => ' . json_encode(stream_get_contents($pipes[1])) . sprintf(' in %.2fs', microtime(true) - $t));
say('child exit => ' . proc_close($p));

array_map('unlink', glob($dir . '/*'));
rmdir($dir);
