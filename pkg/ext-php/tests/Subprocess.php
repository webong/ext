<?php

declare(strict_types=1);

namespace ExtIO\Tests;

/**
 * A child process, for the tests that need a guest or a host on the other side: input goes in on
 * a pipe, and what the child writes to standard output and standard error is read from files.
 *
 * Files, not pipes, because PHP for Windows cannot wait on a pipe: stream_set_blocking() fails on
 * it and stream_select() calls it ready whether or not it holds anything, so a read blocks until
 * the child writes or ends. A file is read without blocking everywhere.
 */
final class Subprocess
{
    /** @var resource */
    private $proc;
    /** @var resource */
    public $stdin;
    private string $outPath;
    private string $errPath;
    private int $outRead = 0;
    private int $errRead = 0;
    private string $buffer = '';
    /** What the child has written to standard error so far, as of the last read of its output. */
    public string $errors = '';
    private ?int $code = null;
    private bool $closed = false;

    /** @param list<string> $command */
    public function __construct(array $command)
    {
        $base = sys_get_temp_dir() . \DIRECTORY_SEPARATOR . 'ext-php-child-' . bin2hex(random_bytes(6));
        $this->outPath = $base . '.out';
        $this->errPath = $base . '.err';
        $proc = proc_open($command, [0 => ['pipe', 'r'], 1 => ['file', $this->outPath, 'wb'], 2 => ['file', $this->errPath, 'wb']], $pipes);
        if ($proc === false) {
            throw new \RuntimeException('cannot start ' . implode(' ', $command));
        }
        $this->proc = $proc;
        $this->stdin = $pipes[0];
    }

    public function __destruct()
    {
        $this->closeInput();
        if (!$this->closed) {
            if ($this->running()) {
                proc_terminate($this->proc, 9);
            }
            proc_close($this->proc);
            $this->closed = true;
        }
        @unlink($this->outPath);
        @unlink($this->errPath);
    }

    /**
     * Runs a command to the end with $input on its standard input.
     *
     * @param list<string> $command
     * @return array{int, string, string} exit code, standard output, standard error
     */
    public static function run(array $command, string $input, float $timeout = 30.0): array
    {
        $p = new self($command);
        fwrite($p->stdin, $input);
        $p->closeInput();
        $code = $p->wait($timeout);
        return [$code, $p->buffer, $p->errors];
    }

    public function write(string $data): void
    {
        fwrite($this->stdin, $data);
        fflush($this->stdin);
    }

    public function closeInput(): void
    {
        if (\is_resource($this->stdin)) {
            fclose($this->stdin);
        }
    }

    /** The next line of standard output without its line feed, or null at its end or after $wait seconds. */
    public function line(float $wait = 5.0): ?string
    {
        $until = microtime(true) + $wait;
        while (true) {
            $at = strpos($this->buffer, "\n");
            if ($at === false) {
                // Ask whether it ran before reading, so what it wrote before it ended is read.
                $running = $this->running();
                $this->collect();
                $at = strpos($this->buffer, "\n");
                if ($at === false) {
                    if (!$running || microtime(true) >= $until) {
                        return null;
                    }
                    usleep(2000);
                    continue;
                }
            }
            $line = substr($this->buffer, 0, $at);
            $this->buffer = substr($this->buffer, $at + 1);
            return $line;
        }
    }

    /** Waits for the process to end; its exit code. */
    public function wait(float $timeout = 10.0): int
    {
        $until = microtime(true) + $timeout;
        while ($this->running()) {
            if (microtime(true) > $until) {
                proc_terminate($this->proc, 9);
                $this->collect();
                throw new \RuntimeException('the process did not end: ' . $this->errors);
            }
            usleep(2000);
        }
        $this->collect();
        return $this->code ?? -1;
    }

    private function running(): bool
    {
        if ($this->code !== null) {
            return false;
        }
        // The exit code is only reported the first time the status is read after the process ended.
        $status = proc_get_status($this->proc);
        if ($status['running']) {
            return true;
        }
        $this->code = $status['exitcode'];
        return false;
    }

    /** Reads what the child has written since the last read. */
    private function collect(): void
    {
        $this->buffer .= self::tail($this->outPath, $this->outRead);
        $this->errors .= self::tail($this->errPath, $this->errRead);
    }

    private static function tail(string $path, int &$read): string
    {
        $data = @file_get_contents($path, false, null, $read);
        if ($data === false || $data === '') {
            return '';
        }
        $read += \strlen($data);
        return $data;
    }
}
