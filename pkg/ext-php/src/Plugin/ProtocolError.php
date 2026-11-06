<?php

declare(strict_types=1);

namespace ExtIO\Plugin;

/** The peer violated the framing or envelope rules; the stream is unusable. */
class ProtocolError extends \RuntimeException
{
}
