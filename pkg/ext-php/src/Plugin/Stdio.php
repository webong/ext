<?php

declare(strict_types=1);

namespace ExtIO\Plugin;

/** @internal Keeps standard output for protocol frames. */
final class Stdio
{
    /** @var resource|null the descriptor that now holds file descriptor 1 (standard error) */
    private static $held = null;

    /**
     * Returns a private duplicate of standard output and points file descriptor 1 at standard
     * error, so that echo, print, warnings and child processes write there instead.
     *
     * @return resource
     */
    public static function claimStdout()
    {
        @ini_set('display_errors', 'stderr');
        $out = fopen('php://fd/1', 'wb');
        if ($out === false) {
            throw new \RuntimeException('cannot duplicate standard output');
        }
        if (self::$held === null) {
            // Closing STDOUT frees descriptor 1; the next duplicate takes the lowest free
            // descriptor, which is 1, so descriptor 1 becomes standard error.
            fclose(\STDOUT);
            $held = fopen('php://fd/2', 'wb');
            self::$held = $held === false ? false : $held;
        }
        if (!self::redirected()) {
            // Without descriptor 1 on standard error, at least the output layer goes there.
            ob_start(static function (string $buffer): string {
                fwrite(\STDERR, $buffer);
                return '';
            }, 1);
        }
        return $out;
    }

    /**
     * Raises memory_limit to 512 MiB when it is lower: one 24 MiB frame is held several times
     * over (the line, the decoded request, the encoded response).
     */
    public static function roomForFrames(): void
    {
        $limit = trim((string) ini_get('memory_limit'));
        if ($limit === '' || $limit === '-1') {
            return;
        }
        $bytes = (int) $limit;
        $bytes *= match (strtolower(substr($limit, -1))) {
            'g' => 1 << 30,
            'm' => 1 << 20,
            'k' => 1 << 10,
            default => 1,
        };
        if ($bytes < 512 << 20) {
            @ini_set('memory_limit', '512M');
        }
    }

    private static function redirected(): bool
    {
        if (!\is_resource(self::$held)) {
            return false;
        }
        $probe = @fopen('php://fd/1', 'wb');
        if ($probe === false) {
            return false;
        }
        $a = fstat($probe);
        $b = fstat(\STDERR);
        fclose($probe);
        return $a !== false && $b !== false && $a['dev'] === $b['dev'] && $a['ino'] === $b['ino'];
    }
}
