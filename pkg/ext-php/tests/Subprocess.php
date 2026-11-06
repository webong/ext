<?php

declare(strict_types=1);

namespace ExtIO\Tests;

/** A child process on pipes, for the tests that need a guest or a host on the other side. */
final class Subprocess
{
    /** @var resource */
    private $proc;
    /** @var resource */
    public $stdin;
    /** @var resource */
    public $stdout;
    /** @var resource */
    public $stderr;
    private string $buffer = '';
    public string $errors = '';
    private ?int $code = null;

    /** @param list<string> $command */
    public function __construct(array $command)
    {
        $proc = proc_open($command, [0 => ['pipe', 'r'], 1 => ['pipe', 'w'], 2 => ['pipe', 'w']], $pipes);
        if ($proc === false) {
            throw new \RuntimeException('cannot start ' . implode(' ', $command));
        }
        $this->proc = $proc;
        [$this->stdin, $this->stdout, $this->stderr] = $pipes;
        stream_set_blocking($this->stdout, false);
        stream_set_blocking($this->stderr, false);
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
        $out = '';
        while (($line = $p->line($timeout)) !== null) {
            $out .= $line . "\n";
        }
        $code = $p->wait($timeout);
        return [$code, $out . $p->buffer, $p->errors];
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
            if ($at !== false) {
                $line = substr($this->buffer, 0, $at);
                $this->buffer = substr($this->buffer, $at + 1);
                return $line;
            }
            $left = $until - microtime(true);
            if ($left <= 0 || !\is_resource($this->stdout) || feof($this->stdout)) {
                $this->drainErrors();
                return null;
            }
            $read = [$this->stdout, $this->stderr];
            $write = $except = null;
            if (@stream_select($read, $write, $except, (int) $left, (int) (($left - (int) $left) * 1e6)) > 0) {
                foreach ($read as $r) {
                    $chunk = fread($r, 1 << 20);
                    if ($r === $this->stdout) {
                        $this->buffer .= (string) $chunk;
                    } else {
                        $this->errors .= (string) $chunk;
                    }
                }
            }
        }
    }

    /** Waits for the process to end; its exit code. */
    public function wait(float $timeout = 10.0): int
    {
        if ($this->code !== null) {
            return $this->code;
        }
        $until = microtime(true) + $timeout;
        while (true) {
            $status = proc_get_status($this->proc);
            if (!$status['running']) {
                $this->drainErrors();
                // Keep what the process wrote before it ended for line() to return.
                while (\is_resource($this->stdout) && ($chunk = fread($this->stdout, 1 << 20)) !== false && $chunk !== '') {
                    $this->buffer .= $chunk;
                }
                $this->code = $status['exitcode'];
                proc_close($this->proc);
                return $this->code;
            }
            if (microtime(true) > $until) {
                proc_terminate($this->proc, 9);
                throw new \RuntimeException('the process did not end: ' . $this->errors);
            }
            $this->drainErrors();
            while (\is_resource($this->stdout) && ($chunk = fread($this->stdout, 1 << 20)) !== false && $chunk !== '') {
                $this->buffer .= $chunk;
            }
            usleep(5000);
        }
    }

    private function drainErrors(): void
    {
        if (\is_resource($this->stderr)) {
            while (($chunk = fread($this->stderr, 1 << 16)) !== false && $chunk !== '') {
                $this->errors .= $chunk;
            }
        }
    }
}
