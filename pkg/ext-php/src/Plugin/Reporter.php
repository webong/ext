<?php

declare(strict_types=1);

namespace ExtIO\Plugin;

/**
 * Sends the progress events of one call, within the rate limit the host set.
 *
 * @internal
 */
final class Reporter
{
    private Limiter $limiter;
    private bool $finished = false;

    public function __construct(private readonly Link $link, private readonly string $parent, ProgressLimits $limits)
    {
        $this->limiter = new Limiter($limits->eventsPerSecond, $limits->bytesPerSecond, hrtime(true));
    }

    /** Events reach the host in the order they are reported, and none after the call is over. */
    public function report(mixed $event): string
    {
        if (!$event instanceof Progress
            || !\is_string($event->kind) || !\is_string($event->stream) || !\is_string($event->text)
            || !\is_int($event->completed) || $event->completed < 0
            || !\is_int($event->total) || $event->total < 0
        ) {
            throw new FrameError('invalid', 'progress event breaks the rules');
        }
        try {
            Frames::checkEvent($event->kind, $event->stream, $event->text, $event->completed, $event->total);
        } catch (ProtocolError) {
            throw new FrameError('invalid', 'progress event breaks the rules');
        }
        $dropped = $this->limiter->admit(\strlen($event->text), hrtime(true));
        if ($dropped === null) {
            return DROPPED;
        }
        if ($this->finished) {
            throw new FrameError('closed', 'the call is over');
        }
        $this->send($event->kind, $event->stream, $event->text, $event->completed, $event->total, $dropped);
        return SENT;
    }

    /** Ends reporting after a marker for events never reported: nothing may follow the response. */
    public function finish(): void
    {
        if ($this->finished) {
            return;
        }
        $this->finished = true;
        $pending = $this->limiter->pending();
        if ($pending > 0) {
            try {
                $this->send('log', '', '', 0, 0, $pending);
            } catch (FrameError) {
                // The connection is gone; there is nobody to tell.
            }
        }
    }

    private function send(string $kind, string $stream, string $text, int $completed, int $total, int $dropped): void
    {
        $frame = ['frame' => 'progress', 'apiVersion' => API_VERSION, 'parent' => $this->parent, 'kind' => $kind];
        foreach (['stream' => $stream, 'text' => $text, 'completed' => $completed, 'total' => $total, 'dropped' => $dropped] as $key => $value) {
            if ($value !== '' && $value !== 0) {
                $frame[$key] = $value;
            }
        }
        $this->link->write(Json::encode($frame));
    }
}
