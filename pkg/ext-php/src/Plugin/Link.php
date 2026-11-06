<?php

declare(strict_types=1);

namespace ExtIO\Plugin;

/**
 * One connection with the host: frames written one at a time, and the read side that a handler
 * waiting for an answer drives. $failure is set when the host broke the rules.
 *
 * @internal
 */
final class Link
{
    private int $issued = 0;
    public bool $ended = false;
    public ?ProtocolError $failure = null;

    /**
     * @param resource $in
     * @param resource $out
     */
    public function __construct(private $in, private $out)
    {
    }

    /**
     * Reads one LF-terminated frame; null at a clean end of the input. Bounded by the limit.
     *
     * @param resource $stream
     */
    public static function readFrame($stream): ?string
    {
        $line = fgets($stream, MAX_FRAME_BYTES + 3);
        if ($line === false || $line === '') {
            return null;
        }
        if ($line[-1] !== "\n") {
            if (\strlen($line) > MAX_FRAME_BYTES + 1) {
                throw new ProtocolError('frame too large');
            }
            throw new ProtocolError('unexpected EOF inside a frame');
        }
        return substr($line, 0, -1);
    }

    /** Writes one encoded frame and its line feed. */
    public function write(string $frame): void
    {
        $data = $frame . "\n";
        $length = \strlen($data);
        $done = 0;
        while ($done < $length) {
            $n = @fwrite($this->out, $done === 0 ? $data : substr($data, $done));
            if ($n === false || $n === 0) {
                throw new FrameError('closed', 'plugin connection ended');
            }
            $done += $n;
        }
        fflush($this->out);
    }

    /**
     * Calls a host operation and waits for the answer. $parent is the id of the call it belongs
     * to; $deadline is seconds since the epoch. Returns the host's payload.
     */
    public function reverse(string $parent, string $name, string $version, string $operation, mixed $payload, float $deadline): mixed
    {
        if ($this->ended) {
            throw new FrameError('closed', 'plugin connection ended');
        }
        $this->issued++;
        $requestId = 'g' . $this->issued;
        $frame = ['frame' => 'reverse_request', 'apiVersion' => API_VERSION, 'id' => $requestId];
        if ($parent !== '') {
            $frame['parent'] = $parent;
        }
        $frame += [
            'contract' => ['name' => $name, 'version' => $version],
            'operation' => $operation,
            'deadline' => Time::format($deadline),
            'payload' => $payload,
        ];
        try {
            $data = Json::encode($frame);
            if (\strlen($data) > MAX_FRAME_BYTES) {
                throw new ProtocolError('frame too large');
            }
            // The frame must read back as the host reads it.
            Frames::validateReverseRequest(Json::decode($data));
        } catch (ProtocolError | \JsonException) {
            throw new FrameError('invalid', 'reverse request breaks the rules');
        }
        try {
            $this->write($data);
        } catch (FrameError $e) {
            $this->ended = true;
            throw $e;
        }
        // The host sends nothing but this answer during a call.
        try {
            $raw = self::readFrame($this->in);
        } catch (ProtocolError) {
            throw $this->violated();
        }
        if ($raw === null) {
            $this->ended = true;
            throw new FrameError('closed', 'plugin connection ended');
        }
        try {
            $answer = Json::decode($raw);
            if (!$answer instanceof \stdClass || Json::get($answer, 'frame') !== 'reverse_response' || Json::get($answer, 'id') !== $requestId) {
                throw new ProtocolError('plugin protocol violation');
            }
            [$result, $error] = Frames::validateReverseResponse($answer, $requestId);
        } catch (ProtocolError) {
            throw $this->violated();
        }
        if ($error !== null) {
            throw $error;
        }
        return $result;
    }

    private function violated(): FrameError
    {
        $this->failure = new ProtocolError('plugin protocol violation');
        $this->ended = true;
        return new FrameError('protocol', 'plugin protocol violation');
    }
}
