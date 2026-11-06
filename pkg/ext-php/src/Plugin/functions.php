<?php

declare(strict_types=1);

/*
 * Guest SDK for ext.plugin/v1 over bounded JSON lines (needs only ext-json).
 *
 *     use ExtIO\Plugin;
 *
 *     Plugin\serveStdio('plugin.json', function (string $contract, string $operation, mixed $payload): mixed {
 *         if ($operation === 'busy') {
 *             throw new Plugin\RemoteError('busy', 'try later', 250);
 *         }
 *         return $payload;
 *     });
 *
 * The guest answers plugin.hello with the descriptor, validates every request the way the Go
 * plugin.ValidateRequest does, and calls the handler sequentially. It never writes to standard
 * output except protocol frames.
 */

namespace ExtIO\Plugin;

const API_VERSION = 'ext.plugin/v1';
const MAX_FRAME_BYTES = 24 << 20;
const MAX_RETRY_AFTER_MS = 365 * 24 * 60 * 60 * 1000;
const MAX_MESSAGE_BYTES = 4096;
const MAX_PROGRESS_TEXT = 2048;
const DEFAULT_PROGRESS_EVENTS = 50;
const DEFAULT_PROGRESS_BYTES = 64 * 1024;
const DEFAULT_MAX_CALL_SECONDS = 30.0;

/** What CallInfo::progress() returns: the event was written. */
const SENT = 'sent';
/** What CallInfo::progress() returns: the rate limit dropped the event (routine). */
const DROPPED = 'dropped';

/**
 * Serves $guest over streams until the input ends. Throws ProtocolError on violations.
 *
 * A handler can report progress and call its host (see CallInfo). The host sends nothing during a
 * call but the answer to a reverse call: anything else ends the session with ProtocolError, and
 * the call in flight is not answered. When the input ends during a call, the call is answered and
 * the session ends.
 *
 * @param resource $in
 * @param resource $out
 */
function serve(Guest $guest, $in, $out): void
{
    $guest->serve($in, $out);
}

/**
 * Serves on this process's standard input and output. Returns when the input ends.
 *
 * $descriptor is an array or stdClass, or the path of a plugin.json or bare descriptor file.
 * Protocol frames use a private duplicate of the original standard output; file descriptor 1 is
 * pointed at standard error, so echo, print and stray output cannot corrupt the stream. The
 * STDOUT constant is closed: write diagnostics to STDERR. A memory_limit below 512M is raised to
 * 512M, since a frame may be 24 MiB. Call once, from the guest's main.
 */
function serveStdio(
    array|\stdClass|string $descriptor,
    callable $handler,
    float $maxCallSeconds = DEFAULT_MAX_CALL_SECONDS,
    bool $redactErrors = false,
): void {
    $guest = new Guest($descriptor, $handler, $maxCallSeconds, $redactErrors);
    Stdio::roomForFrames();
    $out = Stdio::claimStdout();
    try {
        $guest->serve(\STDIN, $out);
    } finally {
        fclose($out);
    }
}

/** The CallInfo of the in-flight call, or null outside a handler. */
function currentCall(): ?CallInfo
{
    return Guest::currentCall();
}

/**
 * Reads a descriptor from a plugin.json (ext.package/v1) or a bare descriptor file.
 *
 * @throws \InvalidArgumentException
 */
function loadDescriptor(string $path): \stdClass
{
    return Descriptor::load($path);
}

/**
 * Throws InvalidArgumentException unless $descriptor is a valid ext.plugin/v1 descriptor.
 */
function validateDescriptor(mixed $descriptor): void
{
    Descriptor::validate($descriptor);
}

/**
 * Cuts text into pieces that each fit one progress event: at most 2048 bytes of UTF-8, never
 * ending inside a character. A byte that is not UTF-8 becomes U+FFFD.
 *
 * @return list<string>
 */
function splitProgressText(string $text): array
{
    return Text::split($text);
}
