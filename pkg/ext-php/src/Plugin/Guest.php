<?php

declare(strict_types=1);

namespace ExtIO\Plugin;

/**
 * An immutable descriptor plus a domain handler.
 *
 * The handler, `fn (string $contract, string $operation, mixed $payload): mixed`, returns a
 * JSON-encodable result (a JSON object arrives as a stdClass; return `new \stdClass()` for an
 * empty object, since an empty PHP array encodes as `[]`), or throws RemoteError for a deliberate
 * public failure. Any other Throwable becomes a `provider_error` whose message is the exception's,
 * cut to 4096 bytes; pass $redactErrors to send a generic `operation_failed` instead, so internal
 * text never reaches the host. Calls are sequential.
 */
final class Guest
{
    private const HELLO_FIELDS = ['apiVersion', 'id', 'plugin', 'contract', 'operation', 'surface', 'deadline', 'payload'];
    private const REQUEST_FIELDS = ['apiVersion', 'id', 'plugin', 'contract', 'operation', 'surface', 'deadline', 'payload', 'progress', 'parent'];

    private static ?CallInfo $current = null;

    private \stdClass $descriptor;
    /** @var \Closure(string, string, mixed): mixed */
    private \Closure $handler;
    private float $maxCall;
    private bool $redact;
    /** @var array<string, array{string, bool}> "contract\0version\0operation" => [surface, reports progress] */
    private array $ops = [];
    /** @var array<string, true> the host operations the plugin may call */
    private array $reverse = [];
    /** @var array{id: string, revision: string, version: string} */
    private array $identity;

    public function __construct(
        array|\stdClass|string $descriptor,
        callable $handler,
        float $maxCallSeconds = DEFAULT_MAX_CALL_SECONDS,
        bool $redactErrors = false,
    ) {
        if (\is_string($descriptor)) {
            $descriptor = Descriptor::load($descriptor);
        } else {
            $descriptor = Json::normalize($descriptor);
            Descriptor::validate($descriptor);
        }
        if ($maxCallSeconds <= 0) {
            throw new \InvalidArgumentException('maxCallSeconds must be positive');
        }
        $this->descriptor = $descriptor;
        $this->handler = \Closure::fromCallable($handler);
        $this->maxCall = $maxCallSeconds;
        $this->redact = $redactErrors;
        foreach ($descriptor->contracts as $c) {
            foreach ($c->operations as $op) {
                $this->ops[self::key($c->name, $c->version, $op->name)] = [
                    Json::get($op, 'surface') ?? '',
                    Json::get($op, 'progress') === true,
                ];
            }
        }
        foreach (Json::get($descriptor, 'reverse') ?? [] as $c) {
            foreach ($c->operations as $op) {
                $this->reverse[self::key($c->name, $c->version, $op->name)] = true;
            }
        }
        $ident = $descriptor->identity;
        $this->identity = ['id' => $ident->id, 'revision' => $ident->revision, 'version' => Json::get($ident, 'version', '')];
    }

    /** The CallInfo of the in-flight call, or null outside a handler. */
    public static function currentCall(): ?CallInfo
    {
        return self::$current;
    }

    /** A copy of the descriptor, as plugin.hello answers it. */
    public function handshake(): \stdClass
    {
        return Json::decode(Json::encode($this->descriptor));
    }

    /**
     * Validates one decoded request and returns the response envelope.
     *
     * Throws ProtocolError for envelopes the Go host would not even decode (unknown fields, wrong
     * types); those end the stream. $link is the connection the request came in on (serve()
     * passes it): without one the call can neither report progress nor call its host.
     *
     * @return array<string, mixed>
     */
    public function invoke(mixed $request, ?Link $link = null): array
    {
        if (!Json::strict($request, self::REQUEST_FIELDS)) {
            throw new ProtocolError('invalid request envelope');
        }
        $requestId = Json::get($request, 'id');
        if (!\is_string($requestId)) {
            throw new ProtocolError('invalid request id');
        }
        $plugin = self::identityOf(Json::get($request, 'plugin'), ['id', 'revision', 'version']);
        $contract = self::identityOf(Json::get($request, 'contract'), ['name', 'version']);
        foreach (['apiVersion', 'operation', 'surface'] as $k) {
            if (Json::has($request, $k) && !\is_string($request->{$k})) {
                throw new ProtocolError('invalid request field');
            }
        }
        if (Json::has($request, 'deadline') && !\is_string($request->deadline)) {
            throw new ProtocolError('invalid request deadline');
        }
        $parent = Json::get($request, 'parent');
        if ($parent !== null && !\is_string($parent)) {
            throw new ProtocolError('invalid request parent');
        }
        $rate = self::rateOf(Json::get($request, 'progress'));
        $response = ['apiVersion' => API_VERSION, 'id' => $requestId];
        $deadline = Time::parse(Json::get($request, 'deadline'));
        $operation = Json::get($request, 'operation', '');
        $surface = Json::get($request, 'surface', '');
        $key = self::key($contract['name'], $contract['version'], $operation);
        if (Json::get($request, 'apiVersion') !== API_VERSION
            || !Pattern::revision($requestId)
            || $deadline === null
            || $plugin !== $this->identity
            || !isset($this->ops[$key])
            || $this->ops[$key][0] !== $surface
        ) {
            $response['error'] = ['code' => 'invalid_request', 'message' => 'request does not match selected contract'];
            return $response;
        }
        $limit = min($deadline, microtime(true) + $this->maxCall);
        $error = null;
        $result = null;
        if (microtime(true) >= $limit) {
            $error = $this->failure('deadline exceeded');
        } else {
            $info = self::$current = new CallInfo($requestId, $contract['name'], $contract['version'], $operation, $surface, $limit);
            $reporter = $caller = null;
            if ($link !== null) {
                // A call reports progress when its operation declared it and the host asked for
                // events; it can call the host on the connection it came in on.
                if ($rate !== null && $this->ops[$key][1]) {
                    $info->progressLimits = new ProgressLimits(
                        $rate[0] > 0 ? $rate[0] : DEFAULT_PROGRESS_EVENTS,
                        $rate[1] > 0 ? $rate[1] : DEFAULT_PROGRESS_BYTES,
                    );
                    $reporter = $info->reporter = new Reporter($link, $requestId, $info->progressLimits);
                }
                $caller = $info->reverse = new ReverseCaller($link, $this->reverse, $requestId, $limit);
            }
            try {
                $result = ($this->handler)($contract['name'], $operation, Json::get($request, 'payload'));
                Json::encode($result); // surface results that are not JSON as provider errors
                if (microtime(true) >= $limit) {
                    $error = $this->failure('deadline exceeded');
                }
            } catch (RemoteError $e) {
                $error = $e->toWire();
            } catch (\Throwable $e) {
                $error = $this->failure($e->getMessage());
            } finally {
                self::$current = null;
                if ($caller !== null) {
                    $caller->over = true; // a request names its call, so a caller cannot outlive it
                }
                $reporter?->finish(); // a marker for events never reported, before the response
            }
        }
        if ($error !== null) {
            $response['error'] = $error;
        } else {
            $response['payload'] = $result;
        }
        return $response;
    }

    /**
     * Serves this guest over streams until the input ends; see serve().
     *
     * @param resource $in
     * @param resource $out
     */
    public function serve($in, $out): void
    {
        $link = new Link($in, $out);
        $hello = false;
        while (true) {
            $raw = Link::readFrame($in);
            if ($raw === null) {
                return;
            }
            $request = Json::decode($raw);
            if (!$hello) {
                $response = $this->answerHello($request);
                $hello = true;
            } else {
                $response = $this->invoke($request, $link);
                if ($link->failure !== null) {
                    throw $link->failure; // the host broke the rules while the handler waited
                }
            }
            $frame = Json::encode($response);
            if (\strlen($frame) > MAX_FRAME_BYTES) {
                $frame = Json::encode([
                    'apiVersion' => API_VERSION,
                    'id' => $response['id'],
                    'error' => ['code' => 'response_too_large', 'message' => 'response exceeds the frame limit'],
                ]);
            }
            $link->write($frame);
        }
    }

    /** @return array<string, mixed> */
    private function answerHello(mixed $request): array
    {
        if (!Json::strict($request, self::HELLO_FIELDS)) {
            throw new ProtocolError('invalid hello');
        }
        $plugin = self::identityOf(Json::get($request, 'plugin'), ['id', 'revision', 'version']);
        $contract = self::identityOf(Json::get($request, 'contract'), ['name', 'version']);
        $deadline = Time::parse(Json::get($request, 'deadline'));
        if (Json::get($request, 'apiVersion') !== API_VERSION
            || Json::get($request, 'id') !== 'hello'
            || Json::get($request, 'operation') !== 'plugin.hello'
            || implode('', $plugin) !== ''
            || implode('', $contract) !== ''
            || Json::get($request, 'surface', '') !== ''
            || Json::has($request, 'payload')
            || $deadline === null
        ) {
            throw new ProtocolError('invalid hello');
        }
        if (microtime(true) >= $deadline) {
            throw new ProtocolError('hello deadline exceeded');
        }
        return ['apiVersion' => API_VERSION, 'id' => 'hello', 'payload' => $this->descriptor];
    }

    /** @return array{code: string, message: string} */
    private function failure(string $message): array
    {
        if ($this->redact) {
            return ['code' => 'operation_failed', 'message' => 'plugin operation failed'];
        }
        return ['code' => 'provider_error', 'message' => Text::cut(Text::scrub($message), MAX_MESSAGE_BYTES)];
    }

    /**
     * The rate a request sets for a call's progress events: null when absent, else
     * [eventsPerSecond, bytesPerSecond] with 0 for a member left out. Go decodes the members into
     * int64s, so a number that is not a whole one that fits is not a request at all.
     *
     * @return array{int, int}|null
     */
    private static function rateOf(mixed $value): ?array
    {
        if ($value === null) {
            return null;
        }
        if (!Json::strict($value, ['eventsPerSecond', 'bytesPerSecond'])) {
            throw new ProtocolError('invalid progress rate');
        }
        $out = [];
        foreach (['eventsPerSecond', 'bytesPerSecond'] as $k) {
            $n = Json::get($value, $k) ?? 0;
            if (!\is_int($n)) {
                throw new ProtocolError('invalid progress rate');
            }
            $out[] = $n;
        }
        return $out;
    }

    /**
     * @param list<string> $fields
     * @return array<string, string>
     */
    private static function identityOf(mixed $obj, array $fields): array
    {
        if ($obj === null) {
            return array_fill_keys($fields, '');
        }
        if (!Json::strict($obj, $fields)) {
            throw new ProtocolError('invalid envelope object');
        }
        $out = [];
        foreach ($fields as $f) {
            $v = Json::get($obj, $f, '');
            if (!\is_string($v)) {
                throw new ProtocolError('invalid envelope field');
            }
            $out[$f] = $v;
        }
        return $out;
    }

    /** @internal */
    public static function key(string $contract, string $version, string $operation): string
    {
        return $contract . "\0" . $version . "\0" . $operation;
    }
}
