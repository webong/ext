// Progress events and reverse calls for a guest (docs/plugin-guest-frames.md).
//
// A guest on a transport that carries frames can report progress while a call runs and call its
// host. The rules are the Go contract's, pinned by
// lib/plugin/testdata/v1/{frame-cases,progress-limiter-cases}.json. This guest runs inline: it
// has no reader of its own, so the thread that waits for the host's answer to a reverse call
// reads the connection itself, which suffices because the host sends nothing but answers during
// a call. A call therefore has one reverse call in flight, from the handler's thread.
using System.Diagnostics;
using System.Numerics;
using System.Text;
using System.Text.Json.Nodes;

namespace ExtIO.Plugin;

/// <summary>What <see cref="CallInfo.Progress"/> returns.</summary>
public enum ProgressResult
{
    /// <summary>The event was written.</summary>
    Sent,
    /// <summary>The rate limit dropped it (routine: the next event that passes carries the count).</summary>
    Dropped,
}

/// <summary>The rate the host set for a call's progress events.</summary>
public sealed record ProgressLimits(long EventsPerSecond, long BytesPerSecond);

/// <summary>A contract by name and exact version.</summary>
public readonly record struct ContractRef(string Name, string Version);

/// <summary>
/// A progress or reverse-call operation failed locally (a host's refusal is a <see cref="RemoteError"/>).
/// <c>Code</c> is <c>unsupported</c> (no reporter or caller on this call), <c>invalid</c> (the event
/// or request breaks the rules; nothing was sent), <c>closed</c> (the call is over or the connection
/// ended), or <c>protocol</c> (the host broke the framing rules; the session ends).
/// </summary>
public sealed class FrameError : Exception
{
    public string Code { get; }

    public FrameError(string code, string? message = null) : base(message ?? code) => Code = code;
}

/// <summary>
/// One progress event: a <c>progress</c> count of <c>Completed</c> out of <c>Total</c> (zero when not
/// known), or a <c>log</c> line, optionally on a stream (<c>out</c> or <c>err</c>). Text is at most
/// 2048 bytes of UTF-8; use <see cref="Progress.Split"/> to cut a longer line.
/// </summary>
public sealed record Progress(string Kind, string Stream = "", string Text = "", ulong Completed = 0, ulong Total = 0)
{
    public const int MaxText = 2048;
    public const long DefaultEvents = 50;
    public const long DefaultBytes = 64 * 1024;

    public static Progress Log(string text) => new("log", "", text);

    /// <summary>A log line of a program's standard output.</summary>
    public static Progress Out(string text) => new("log", "out", text);

    /// <summary>A log line of a program's standard error.</summary>
    public static Progress Err(string text) => new("log", "err", text);

    /// <summary><paramref name="completed"/> of <paramref name="total"/>; zero when the total is not known.</summary>
    public static Progress Fraction(ulong completed, ulong total) => new("progress", "", "", completed, total);

    /// <summary>
    /// Cuts text into pieces that each fit one event: at most 2048 bytes of UTF-8, never ending
    /// inside a character. A lone surrogate becomes U+FFFD.
    /// </summary>
    public static List<string> Split(string text)
    {
        var pieces = new List<string>();
        var piece = new StringBuilder();
        var size = 0;
        for (var i = 0; i < text.Length;)
        {
            if (!Rune.TryGetRuneAt(text, i, out var rune))
            {
                rune = Rune.ReplacementChar;
                i++;
            }
            else
            {
                i += rune.Utf16SequenceLength;
            }
            if (size + rune.Utf8SequenceLength > MaxText)
            {
                pieces.Add(piece.ToString());
                piece.Clear();
                size = 0;
            }
            piece.Append(rune.ToString());
            size += rune.Utf8SequenceLength;
        }
        if (piece.Length > 0) pieces.Add(piece.ToString());
        return pieces;
    }
}

/// <summary>The frame rules and the connection with the host.</summary>
public static class Frames
{
    static readonly BigInteger Uint64Limit = BigInteger.One << 64;

    /// <summary>
    /// A token bucket counted in events and bytes of text, with a burst of twice the rate, and a
    /// count of events dropped since the last one that passed. Time is a monotonic clock in
    /// nanoseconds, given by the caller.
    /// </summary>
    public sealed class Limiter
    {
        readonly double eventRate;
        readonly double byteRate;
        double events;
        double bytes;
        long last;
        ulong dropped;

        public Limiter(long eventsPerSecond = 0, long bytesPerSecond = 0, long nowNs = 0)
        {
            eventRate = eventsPerSecond > 0 ? eventsPerSecond : Progress.DefaultEvents;
            byteRate = bytesPerSecond > 0 ? bytesPerSecond : Progress.DefaultBytes;
            events = 2 * eventRate;
            bytes = 2 * byteRate;
            last = nowNs;
        }

        /// <summary>
        /// The number of events dropped since the previous one that passed (which the caller
        /// attaches to this event), or null when the rate limit drops it.
        /// </summary>
        public ulong? Admit(long textBytes, long nowNs)
        {
            var delta = nowNs - last;
            // Seconds as Go's Duration.Seconds computes them.
            var whole = delta / 1_000_000_000;
            var elapsed = whole + (double)(delta - whole * 1_000_000_000) / 1e9;
            if (elapsed > 0)
            {
                events = Math.Min(events + elapsed * eventRate, 2 * eventRate);
                bytes = Math.Min(bytes + elapsed * byteRate, 2 * byteRate);
                last = nowNs;
            }
            if (events < 1 || bytes < textBytes)
            {
                dropped++;
                return null;
            }
            events -= 1;
            bytes -= textBytes;
            var reported = dropped;
            dropped = 0;
            return reported;
        }

        /// <summary>The drops not yet reported, reset to zero.</summary>
        public ulong Pending()
        {
            var reported = dropped;
            dropped = 0;
            return reported;
        }
    }

    // --- judging frames ---------------------------------------------------------------------

    static readonly string[] ProgressFields = { "frame", "apiVersion", "parent", "kind", "stream", "text", "completed", "total", "dropped" };
    static readonly string[] ReverseRequestFields = { "frame", "apiVersion", "id", "parent", "contract", "operation", "deadline", "payload" };
    static readonly string[] ReverseResponseFields = { "frame", "apiVersion", "id", "payload", "error" };

    static ProtocolError Violation() => new("plugin protocol violation");

    static JsonObject Strict(JsonNode? value, params string[] fields) =>
        Wire.OnlyKeys(value, fields) ? (JsonObject)value! : throw Violation();

    // A member Go decodes into a string: absent and null are empty.
    static string Text(JsonObject m, string key)
    {
        var v = m[key];
        if (v is null) return "";
        return Wire.AsString(v) ?? throw Violation();
    }

    // A member Go decodes into a uint64: absent and null are zero; anything but a whole number
    // that fits is refused.
    static ulong Count(JsonObject m, string key)
    {
        var v = m[key];
        if (v is null) return 0;
        if (Wire.AsInteger(v) is { } n && n >= 0 && n < Uint64Limit) return (ulong)n;
        throw Violation();
    }

    internal static void CheckEvent(string kind, string stream, string text, ulong completed, ulong total)
    {
        var size = Wire.Utf8Length(text);
        if (kind is not ("progress" or "log") ||
            stream is not ("" or "out" or "err") ||
            (stream != "" && kind != "log") ||
            size < 0 || size > Progress.MaxText ||
            (total != 0 && completed > total))
        {
            throw Violation();
        }
    }

    /// <summary>
    /// Judges a progress frame: returns its parent (the call it belongs to), the event and its
    /// dropped count; throws <see cref="ProtocolError"/>.
    /// </summary>
    public static (string Parent, Progress Event, ulong Dropped) ValidateProgressFrame(JsonNode? value)
    {
        var v = Strict(value, ProgressFields);
        var parent = Text(v, "parent");
        var e = new Progress(Text(v, "kind"), Text(v, "stream"), Text(v, "text"), Count(v, "completed"), Count(v, "total"));
        var dropped = Count(v, "dropped");
        if (Text(v, "frame") != "progress" || Text(v, "apiVersion") != Wire.ApiVersion || !Wire.Matches(Wire.Revision, parent))
        {
            throw Violation();
        }
        CheckEvent(e.Kind, e.Stream, e.Text, e.Completed, e.Total);
        return (parent, e, dropped);
    }

    /// <summary>Judges a reverse request frame, as the host reads it; throws <see cref="ProtocolError"/>.</summary>
    public static void ValidateReverseRequest(JsonNode? value)
    {
        var v = Strict(value, ReverseRequestFields);
        var r = Strict(v["contract"], "name", "version");
        var parent = Text(v, "parent");
        if (Text(v, "frame") != "reverse_request" ||
            Text(v, "apiVersion") != Wire.ApiVersion ||
            !Wire.Matches(Wire.Revision, Text(v, "id")) ||
            (parent != "" && !Wire.Matches(Wire.Revision, parent)) ||
            !Wire.Matches(Wire.Identifier, Text(r, "name")) ||
            !Wire.Matches(Wire.Revision, Text(r, "version")) ||
            !Wire.Matches(Wire.Identifier, Text(v, "operation")) ||
            Wire.ParseDeadline(v["deadline"]) is null)
        {
            throw Violation();
        }
    }

    /// <summary>
    /// Judges a reverse response, which a guest accepts only for the request it is waiting on.
    /// Returns the payload, or the host's error; throws <see cref="ProtocolError"/>.
    /// </summary>
    public static (JsonNode? Payload, RemoteError? Error) ValidateReverseResponse(JsonNode? value, string requestId)
    {
        var v = Strict(value, ReverseResponseFields);
        if (Text(v, "frame") != "reverse_response" || Text(v, "apiVersion") != Wire.ApiVersion || Text(v, "id") != requestId)
        {
            throw Violation();
        }
        var failed = v["error"] is not null;
        // A response carries a payload (null counts) or an error, never both or neither.
        if (failed == v.ContainsKey("payload")) throw Violation();
        if (!failed) return (v["payload"], null);
        var e = Strict(v["error"], "code", "message", "retryAfterMilliseconds");
        var retry = Count(e, "retryAfterMilliseconds");
        var message = Text(e, "message");
        var size = Wire.Utf8Length(message);
        var code = Text(e, "code");
        if (!Wire.Matches(Wire.Identifier, code) || size < 0 || size > Wire.MaxMessageBytes || retry > (ulong)Wire.MaxRetryAfterMs)
        {
            throw Violation();
        }
        return (null, new RemoteError(code, message, (long)retry));
    }

    internal static long MonotonicNs() => (long)(Stopwatch.GetTimestamp() * (1e9 / Stopwatch.Frequency));

    // --- the connection -----------------------------------------------------------------------

    /// <summary>
    /// One connection with the host: frames written one at a time, and the read side that the
    /// thread waiting for an answer drives. <c>Failure</c> is set when the host broke the rules.
    /// </summary>
    public sealed class Link
    {
        readonly Wire.FrameReader input;
        readonly Stream output;
        readonly object writeLock = new();
        long issued;

        internal int Owner { get; }
        internal bool Ended { get; set; }
        public ProtocolError? Failure { get; private set; }

        public Link(Stream input, Stream output) : this(new Wire.FrameReader(input), output) { }

        internal Link(Wire.FrameReader input, Stream output)
        {
            this.input = input;
            this.output = output;
            Owner = Environment.CurrentManagedThreadId;
        }

        /// <summary>Writes one encoded frame and its line feed; frames never interleave.</summary>
        internal void Write(byte[] frame)
        {
            lock (writeLock)
            {
                output.Write(frame);
                output.WriteByte((byte)'\n');
                output.Flush();
            }
        }

        /// <summary>Calls a host operation and waits for the answer. Returns the host's payload.</summary>
        internal JsonNode? Reverse(string parent, string name, string version, string operation, JsonNode? payload, long deadline)
        {
            if (Ended) throw new FrameError("closed", "plugin connection ended");
            issued++;
            var requestId = $"g{issued}";
            byte[] data;
            try
            {
                var frame = new JsonObject { ["frame"] = "reverse_request", ["apiVersion"] = Wire.ApiVersion, ["id"] = requestId };
                if (parent != "") frame["parent"] = parent;
                frame["contract"] = new JsonObject { ["name"] = name, ["version"] = version };
                frame["operation"] = operation;
                frame["deadline"] = Wire.FormatTime(deadline);
                frame["payload"] = payload?.Parent is null ? payload : payload.DeepClone();
                ValidateReverseRequest(frame);
                data = Wire.Encode(frame);
                if (data.Length > Wire.MaxFrameBytes) throw new ArgumentException("frame too large");
                Wire.Decode(data); // the payload must read back as the host reads it
            }
            catch (Exception e) when (e is ProtocolError or EncodeError or ArgumentException or InvalidOperationException)
            {
                throw new FrameError("invalid", "reverse request breaks the rules");
            }
            try
            {
                Write(data);
            }
            catch (Exception e) when (e is IOException or ObjectDisposedException or NotSupportedException)
            {
                Ended = true;
                throw new FrameError("closed", "plugin connection ended");
            }
            // The host sends nothing but this answer during a call.
            byte[]? raw;
            try
            {
                raw = input.Read();
            }
            catch (ProtocolError)
            {
                throw Violated();
            }
            catch (Exception e) when (e is IOException or ObjectDisposedException)
            {
                raw = null;
            }
            if (raw is null)
            {
                Ended = true;
                throw new FrameError("closed", "plugin connection ended");
            }
            (JsonNode? Payload, RemoteError? Error) answer;
            try
            {
                var value = Wire.Decode(raw);
                if (value is not JsonObject o || Wire.AsString(o["frame"]) != "reverse_response" || Wire.AsString(o["id"]) != requestId)
                {
                    throw Violation();
                }
                answer = ValidateReverseResponse(value, requestId);
            }
            catch (ProtocolError)
            {
                throw Violated();
            }
            if (answer.Error is not null) throw answer.Error;
            return answer.Payload?.DeepClone();
        }

        FrameError Violated()
        {
            Failure = new ProtocolError("plugin protocol violation");
            Ended = true;
            return new FrameError("protocol", "plugin protocol violation");
        }
    }

    /// <summary>
    /// Calls an operation the host offers (declared in the descriptor's <c>reverse</c> list) and
    /// waits for the answer. A guest that reads inline makes one call at a time, from the thread
    /// that runs the handler.
    /// </summary>
    public sealed class ReverseCaller
    {
        readonly Link link;
        readonly HashSet<(string, string, string)> declared;
        readonly string parent;
        readonly long deadline;

        internal bool Over { get; set; }

        internal ReverseCaller(Link link, HashSet<(string, string, string)> declared, string parent, long deadline)
        {
            this.link = link;
            this.declared = declared;
            this.parent = parent;
            this.deadline = deadline;
        }

        /// <summary>Several reverse calls cannot be outstanding at once.</summary>
        public bool Concurrent => false;

        /// <summary>
        /// Calls <paramref name="operation"/> of <paramref name="contract"/> with a JSON payload and
        /// returns the host's payload. The host's refusal or error throws <see cref="RemoteError"/>
        /// with its code (<c>reverse_undeclared</c> when the descriptor does not list it,
        /// <c>denied</c>, <c>busy</c>, <c>too_large</c>, <c>expired</c>, ...); a local failure throws
        /// <see cref="FrameError"/>. <paramref name="timeout"/> bounds the deadline the host is given,
        /// never past the call's own; the wait itself ends with the host's answer or the end of the
        /// connection.
        /// </summary>
        public JsonNode? Call(ContractRef contract, string operation, JsonNode? payload = null, TimeSpan? timeout = null)
        {
            if (!declared.Contains((contract.Name, contract.Version, operation)))
            {
                throw new RemoteError("reverse_undeclared", "reverse call refused");
            }
            if (Over) throw new FrameError("closed", "the call is over");
            if (Environment.CurrentManagedThreadId != link.Owner)
            {
                throw new FrameError("unsupported", "reverse calls are made from the handler's thread");
            }
            var limit = deadline;
            if (timeout is { } t)
            {
                limit = Math.Min(limit, Wire.NowMicros() + Math.Max(0, t.Ticks / 10));
            }
            return link.Reverse(parent, contract.Name, contract.Version, operation, payload, limit);
        }

        public JsonNode? Call(string name, string version, string operation, JsonNode? payload = null, TimeSpan? timeout = null) =>
            Call(new ContractRef(name, version), operation, payload, timeout);
    }

    /// <summary>Sends the progress events of one call, within the rate limit the host set.</summary>
    internal sealed class Reporter
    {
        readonly Link link;
        readonly string parent;
        readonly Limiter limiter;
        readonly object gate = new();
        bool finished;

        public Reporter(Link link, string parent, ProgressLimits limits)
        {
            this.link = link;
            this.parent = parent;
            limiter = new Limiter(limits.EventsPerSecond, limits.BytesPerSecond, MonotonicNs());
        }

        // Events reach the host in the order they are reported, and none after the call is over.
        public ProgressResult Report(Progress e)
        {
            try
            {
                if (e is null || e.Kind is null || e.Stream is null || e.Text is null) throw Violation();
                CheckEvent(e.Kind, e.Stream, e.Text, e.Completed, e.Total);
            }
            catch (ProtocolError)
            {
                throw new FrameError("invalid", "progress event breaks the rules");
            }
            lock (gate)
            {
                var dropped = limiter.Admit(Wire.Utf8Length(e.Text), MonotonicNs());
                if (dropped is null) return ProgressResult.Dropped;
                if (finished) throw new FrameError("closed", "the call is over");
                Send(e.Kind, e.Stream, e.Text, e.Completed, e.Total, dropped.Value);
            }
            return ProgressResult.Sent;
        }

        // Ends reporting after a marker for events never reported: nothing may follow the response.
        public void Finish()
        {
            lock (gate)
            {
                if (finished) return;
                finished = true;
                var pending = limiter.Pending();
                if (pending > 0)
                {
                    try
                    {
                        Send("log", "", "", 0, 0, pending);
                    }
                    catch (FrameError)
                    {
                        // The connection is gone; there is nobody to tell.
                    }
                }
            }
        }

        void Send(string kind, string stream, string text, ulong completed, ulong total, ulong dropped)
        {
            var frame = new JsonObject { ["frame"] = "progress", ["apiVersion"] = Wire.ApiVersion, ["parent"] = parent, ["kind"] = kind };
            if (stream != "") frame["stream"] = stream;
            if (text != "") frame["text"] = text;
            if (completed != 0) frame["completed"] = completed;
            if (total != 0) frame["total"] = total;
            if (dropped != 0) frame["dropped"] = dropped;
            try
            {
                link.Write(Wire.Encode(frame));
            }
            catch (Exception ex) when (ex is IOException or ObjectDisposedException or NotSupportedException)
            {
                throw new FrameError("closed", "plugin connection ended");
            }
        }
    }
}
