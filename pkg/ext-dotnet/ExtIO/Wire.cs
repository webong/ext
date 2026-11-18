// What the guest and frames code share: the wire limits, the patterns, strict JSON decoding and
// encoding over System.Text.Json.Nodes, and the RFC 3339 deadline.
using System.Globalization;
using System.Numerics;
using System.Text;
using System.Text.Json;
using System.Text.Json.Nodes;
using System.Text.RegularExpressions;

namespace ExtIO.Plugin;

/// <summary>The peer violated the framing or envelope rules; the stream is unusable.</summary>
public sealed class ProtocolError : Exception
{
    public ProtocolError(string message) : base(message) { }
}

/// <summary>
/// A value cannot be written as JSON: a type JSON has no form for, a number that is not finite,
/// text that is not valid UTF-16, or nesting without end.
/// </summary>
public sealed class EncodeError : Exception
{
    public EncodeError(string message) : base(message) { }
}

/// <summary>
/// A deliberate public failure sent to the host as the structured error. <c>Code</c> matches the
/// identifier syntax, the message is at most 4096 UTF-8 bytes, and <c>RetryAfterMs</c> is an
/// optional bound of at most one year.
/// </summary>
public sealed class RemoteError : Exception
{
    public string Code { get; }
    public long? RetryAfterMs { get; }

    public RemoteError(string code, string message, long? retryAfterMs = null) : base(Checked(code, message, retryAfterMs))
    {
        Code = code;
        RetryAfterMs = retryAfterMs;
    }

    static string Checked(string code, string message, long? retryAfterMs)
    {
        if (!Wire.Matches(Wire.Identifier, code))
        {
            throw new ArgumentException("error code must match [a-z0-9][a-z0-9._/-]{0,255}");
        }
        var size = Wire.Utf8Length(message);
        if (size < 0 || size > Wire.MaxMessageBytes)
        {
            throw new ArgumentException("error message must be UTF-8 text of at most 4096 bytes");
        }
        if (retryAfterMs is { } r && (r < 0 || r > Wire.MaxRetryAfterMs))
        {
            throw new ArgumentException("retryAfterMs must be in [0, one year]");
        }
        return message;
    }

    internal JsonObject ToWire()
    {
        var wire = new JsonObject { ["code"] = Code, ["message"] = Message };
        if (RetryAfterMs is { } r && r != 0)
        {
            wire["retryAfterMilliseconds"] = r;
        }
        return wire;
    }
}

/// <summary>The wire rules of ext.plugin/v1.</summary>
public static class Wire
{
    public const string ApiVersion = "ext.plugin/v1";
    public const int MaxFrameBytes = 24 << 20;
    public const long MaxRetryAfterMs = 365L * 24 * 60 * 60 * 1000;
    public const int MaxMessageBytes = 4096;
    public const int MaxDepth = 64;

    internal static readonly Regex Identifier = new(@"\A[a-z0-9][a-z0-9._/-]{0,255}\z", RegexOptions.CultureInvariant);
    internal static readonly Regex Revision = new(@"\A[A-Za-z0-9][A-Za-z0-9._:+/-]{0,255}\z", RegexOptions.CultureInvariant);
    static readonly Regex Time = new(@"\A(\d{4})-(\d\d)-(\d\d)[Tt](\d\d):(\d\d):(\d\d)(\.\d+)?([Zz]|[+-]\d\d:\d\d)\z", RegexOptions.CultureInvariant);
    static readonly UTF8Encoding Strict = new(false, true);

    internal static bool Matches(Regex pattern, string? value) => value is not null && pattern.IsMatch(value);

    /// <summary>The length of <paramref name="text"/> in bytes of UTF-8, or -1 when it is not valid text.</summary>
    public static int Utf8Length(string? text)
    {
        if (text is null)
        {
            return -1;
        }
        try
        {
            return Strict.GetByteCount(text);
        }
        catch (EncoderFallbackException)
        {
            return -1;
        }
    }

    // --- reading values --------------------------------------------------------------------

    /// <summary>The node as a string, or null when it is not a JSON string.</summary>
    public static string? AsString(JsonNode? node) =>
        node is JsonValue v && v.GetValueKind() == JsonValueKind.String && v.TryGetValue<string>(out var s) ? s : null;

    /// <summary>The node as a boolean, or null when it is not true or false.</summary>
    public static bool? AsBool(JsonNode? node) => node?.GetValueKind() switch
    {
        JsonValueKind.True => true,
        JsonValueKind.False => false,
        _ => null,
    };

    /// <summary>
    /// The node as a whole number, written without a fraction or an exponent (1.0 and 1e2 are not
    /// whole numbers to Go), or null.
    /// </summary>
    public static BigInteger? AsInteger(JsonNode? node)
    {
        if (node is not JsonValue v || v.GetValueKind() != JsonValueKind.Number)
        {
            return null;
        }
        if (v.TryGetValue<JsonElement>(out var e))
        {
            var raw = e.GetRawText();
            return raw.AsSpan().IndexOfAny(".eE") >= 0 ? null : BigInteger.Parse(raw, CultureInfo.InvariantCulture);
        }
        if (v.TryGetValue<long>(out var l)) return l;
        if (v.TryGetValue<int>(out var i)) return i;
        if (v.TryGetValue<ulong>(out var ul)) return ul;
        if (v.TryGetValue<uint>(out var ui)) return ui;
        if (v.TryGetValue<short>(out var sh)) return sh;
        if (v.TryGetValue<ushort>(out var ush)) return ush;
        if (v.TryGetValue<byte>(out var b)) return b;
        if (v.TryGetValue<sbyte>(out var sb)) return sb;
        if (v.TryGetValue<BigInteger>(out var big)) return big;
        return null;
    }

    internal static bool OnlyKeys(JsonNode? node, params string[] fields)
    {
        if (node is not JsonObject o)
        {
            return false;
        }
        foreach (var (key, _) in o)
        {
            if (Array.IndexOf(fields, key) < 0)
            {
                return false;
            }
        }
        return true;
    }

    // --- strict JSON -----------------------------------------------------------------------

    /// <summary>
    /// Decodes one frame (bytes without the LF) with the Go decoder's rules: UTF-8 only, no
    /// duplicate keys, no trailing values, no non-finite constants, no unpaired surrogate escapes,
    /// nesting of at most 64 levels. Numbers keep their text.
    /// </summary>
    public static JsonNode? Decode(ReadOnlySpan<byte> raw)
    {
        if (raw.Length > MaxFrameBytes)
        {
            throw new ProtocolError("frame too large");
        }
        string text;
        try
        {
            text = Strict.GetString(raw);
        }
        catch (DecoderFallbackException)
        {
            throw new ProtocolError("JSON must be UTF-8");
        }
        return new Decoder(text).Parse();
    }

    /// <summary>Encodes a value as compact JSON, escaping everything outside printable ASCII.</summary>
    public static byte[] Encode(JsonNode? value)
    {
        var sb = new StringBuilder();
        Encoder.Write(value, sb, 0, true);
        return Encoding.ASCII.GetBytes(sb.ToString());
    }

    /// <summary>The size of a value written as JSON in UTF-8, without escaping non-ASCII text.</summary>
    internal static int Utf8Size(JsonNode? value)
    {
        var sb = new StringBuilder();
        Encoder.Write(value, sb, 0, false);
        return Encoding.UTF8.GetByteCount(sb.ToString());
    }

    /// <summary>A deep copy of a value in the shape the host reads it.</summary>
    public static JsonNode? Copy(JsonNode? value)
    {
        try
        {
            return Decode(Encode(value));
        }
        catch (ProtocolError e)
        {
            throw new EncodeError(e.Message);
        }
    }

    sealed class Decoder
    {
        readonly string s;
        int i;

        public Decoder(string text) => s = text;

        static ProtocolError Malformed() => new("malformed JSON");

        public JsonNode? Parse()
        {
            SkipSpace();
            var value = ReadValue(0);
            SkipSpace();
            if (i != s.Length)
            {
                throw Malformed();
            }
            return value;
        }

        void SkipSpace()
        {
            while (i < s.Length && s[i] is ' ' or '\t' or '\n' or '\r')
            {
                i++;
            }
        }

        bool Literal(string word)
        {
            if (string.CompareOrdinal(s, i, word, 0, word.Length) != 0)
            {
                return false;
            }
            i += word.Length;
            return true;
        }

        JsonNode? ReadValue(int depth)
        {
            if (depth > MaxDepth)
            {
                throw new ProtocolError("JSON nesting exceeds 64 levels");
            }
            if (i >= s.Length)
            {
                throw Malformed();
            }
            switch (s[i])
            {
                case '{': return ReadObject(depth);
                case '[': return ReadArray(depth);
                case '"': return JsonValue.Create(ReadString());
                case 't': return Literal("true") ? JsonValue.Create(true) : throw Malformed();
                case 'f': return Literal("false") ? JsonValue.Create(false) : throw Malformed();
                case 'n': return Literal("null") ? null : throw Malformed();
                default: return ReadNumber();
            }
        }

        bool Digit() => i < s.Length && s[i] is >= '0' and <= '9';

        JsonNode ReadNumber()
        {
            var start = i;
            if (i < s.Length && s[i] == '-') i++;
            if (!Digit()) throw Malformed();
            if (s[i] == '0') i++;
            else while (Digit()) i++;
            if (i < s.Length && s[i] == '.')
            {
                i++;
                if (!Digit()) throw Malformed();
                while (Digit()) i++;
            }
            if (i < s.Length && s[i] is 'e' or 'E')
            {
                i++;
                if (i < s.Length && s[i] is '+' or '-') i++;
                if (!Digit()) throw Malformed();
                while (Digit()) i++;
            }
            using var doc = JsonDocument.Parse(s.AsMemory(start, i - start));
            return JsonValue.Create(doc.RootElement.Clone())!;
        }

        JsonObject ReadObject(int depth)
        {
            i++;
            var o = new JsonObject();
            SkipSpace();
            if (i < s.Length && s[i] == '}')
            {
                i++;
                return o;
            }
            while (true)
            {
                if (i >= s.Length || s[i] != '"') throw Malformed();
                var key = ReadString();
                if (o.ContainsKey(key)) throw new ProtocolError("duplicate JSON key");
                SkipSpace();
                if (i >= s.Length || s[i] != ':') throw Malformed();
                i++;
                SkipSpace();
                o[key] = ReadValue(depth + 1);
                SkipSpace();
                if (i < s.Length && s[i] == ',')
                {
                    i++;
                    SkipSpace();
                    continue;
                }
                if (i < s.Length && s[i] == '}')
                {
                    i++;
                    return o;
                }
                throw Malformed();
            }
        }

        JsonArray ReadArray(int depth)
        {
            i++;
            var a = new JsonArray();
            SkipSpace();
            if (i < s.Length && s[i] == ']')
            {
                i++;
                return a;
            }
            while (true)
            {
                a.Add(ReadValue(depth + 1));
                SkipSpace();
                if (i < s.Length && s[i] == ',')
                {
                    i++;
                    SkipSpace();
                    continue;
                }
                if (i < s.Length && s[i] == ']')
                {
                    i++;
                    return a;
                }
                throw Malformed();
            }
        }

        string ReadString()
        {
            i++;
            var sb = new StringBuilder();
            while (true)
            {
                if (i >= s.Length) throw Malformed();
                var c = s[i++];
                if (c == '"') return sb.ToString();
                if (c < 0x20) throw Malformed();
                if (c != '\\')
                {
                    sb.Append(c);
                    continue;
                }
                if (i >= s.Length) throw Malformed();
                switch (s[i++])
                {
                    case '"': sb.Append('"'); break;
                    case '\\': sb.Append('\\'); break;
                    case '/': sb.Append('/'); break;
                    case 'b': sb.Append('\b'); break;
                    case 'f': sb.Append('\f'); break;
                    case 'n': sb.Append('\n'); break;
                    case 'r': sb.Append('\r'); break;
                    case 't': sb.Append('\t'); break;
                    case 'u':
                        var cp = Hex4();
                        if (cp is >= 0xD800 and <= 0xDBFF)
                        {
                            if (i + 1 < s.Length && s[i] == '\\' && s[i + 1] == 'u')
                            {
                                i += 2;
                                var low = Hex4();
                                if (low is >= 0xDC00 and <= 0xDFFF)
                                {
                                    sb.Append((char)cp).Append((char)low);
                                    break;
                                }
                            }
                            throw new ProtocolError("unpaired surrogate escape");
                        }
                        if (cp is >= 0xDC00 and <= 0xDFFF) throw new ProtocolError("unpaired surrogate escape");
                        sb.Append((char)cp);
                        break;
                    default: throw Malformed();
                }
            }
        }

        int Hex4()
        {
            if (i + 4 > s.Length) throw Malformed();
            var v = 0;
            for (var k = 0; k < 4; k++)
            {
                var c = s[i + k];
                int d = c switch
                {
                    >= '0' and <= '9' => c - '0',
                    >= 'a' and <= 'f' => c - 'a' + 10,
                    >= 'A' and <= 'F' => c - 'A' + 10,
                    _ => throw Malformed(),
                };
                v = (v << 4) | d;
            }
            i += 4;
            return v;
        }
    }

    static class Encoder
    {
        const int MaxNesting = 1000;

        public static void Write(JsonNode? value, StringBuilder o, int depth, bool ascii)
        {
            if (depth > MaxNesting)
            {
                throw new EncodeError("nesting too deep to write as JSON");
            }
            switch (value)
            {
                case null:
                    o.Append("null");
                    return;
                case JsonObject obj:
                    o.Append('{');
                    var first = true;
                    foreach (var (key, child) in obj)
                    {
                        if (!first) o.Append(',');
                        first = false;
                        WriteString(key, o, ascii);
                        o.Append(':');
                        Write(child, o, depth + 1, ascii);
                    }
                    o.Append('}');
                    return;
                case JsonArray arr:
                    o.Append('[');
                    for (var k = 0; k < arr.Count; k++)
                    {
                        if (k > 0) o.Append(',');
                        Write(arr[k], o, depth + 1, ascii);
                    }
                    o.Append(']');
                    return;
                case JsonValue v:
                    WriteValue(v, o, ascii);
                    return;
            }
            throw new EncodeError($"value of type {value.GetType()} is not JSON serializable");
        }

        static void WriteValue(JsonValue v, StringBuilder o, bool ascii)
        {
            if (v.TryGetValue<JsonElement>(out var e))
            {
                switch (e.ValueKind)
                {
                    case JsonValueKind.Number: o.Append(e.GetRawText()); return;
                    case JsonValueKind.String: WriteString(e.GetString()!, o, ascii); return;
                    case JsonValueKind.True: o.Append("true"); return;
                    case JsonValueKind.False: o.Append("false"); return;
                    case JsonValueKind.Null: o.Append("null"); return;
                    default: Write(JsonNode.Parse(e.GetRawText()), o, 0, ascii); return;
                }
            }
            if (v.TryGetValue<string>(out var s)) { WriteString(s, o, ascii); return; }
            if (v.TryGetValue<bool>(out var b)) { o.Append(b ? "true" : "false"); return; }
            if (v.TryGetValue<char>(out var ch)) { WriteString(ch.ToString(), o, ascii); return; }
            if (v.TryGetValue<double>(out var d)) { o.Append(Float(d)); return; }
            if (v.TryGetValue<float>(out var f)) { o.Append(Float(f)); return; }
            if (v.TryGetValue<decimal>(out var m)) { o.Append(m.ToString(CultureInfo.InvariantCulture)); return; }
            if (AsInteger(v) is { } n) { o.Append(n.ToString(CultureInfo.InvariantCulture)); return; }
            throw new EncodeError("value is not JSON serializable");
        }

        static string Float(double d)
        {
            if (!double.IsFinite(d))
            {
                throw new EncodeError("out of range float values are not JSON compliant");
            }
            var text = d.ToString("R", CultureInfo.InvariantCulture);
            return text.AsSpan().IndexOfAny(".eE") >= 0 ? text : text + ".0";
        }

        static void WriteString(string text, StringBuilder o, bool ascii)
        {
            if (Utf8Length(text) < 0)
            {
                throw new EncodeError("string is not valid UTF-16 text");
            }
            o.Append('"');
            foreach (var c in text)
            {
                switch (c)
                {
                    case '"': o.Append("\\\""); break;
                    case '\\': o.Append("\\\\"); break;
                    case '\n': o.Append("\\n"); break;
                    case '\r': o.Append("\\r"); break;
                    case '\t': o.Append("\\t"); break;
                    case '\b': o.Append("\\b"); break;
                    case '\f': o.Append("\\f"); break;
                    default:
                        if (c < 0x20 || (ascii && c > 0x7e))
                        {
                            o.Append("\\u").Append(((int)c).ToString("x4", CultureInfo.InvariantCulture));
                        }
                        else
                        {
                            o.Append(c);
                        }
                        break;
                }
            }
            o.Append('"');
        }
    }

    // --- frames on a stream ----------------------------------------------------------------

    /// <summary>Reads LF-terminated frames from a stream, bounded by the frame limit.</summary>
    public sealed class FrameReader
    {
        readonly Stream input;
        readonly byte[] buffer = new byte[64 * 1024];
        int start;
        int end;

        public FrameReader(Stream input) => this.input = input;

        /// <summary>One frame without its LF, or null at a clean end of input.</summary>
        public byte[]? Read()
        {
            var line = new MemoryStream();
            while (true)
            {
                if (start == end)
                {
                    start = 0;
                    end = input.Read(buffer, 0, buffer.Length);
                    if (end <= 0)
                    {
                        end = 0;
                        if (line.Length == 0) return null;
                        throw new ProtocolError(line.Length > MaxFrameBytes + 1 ? "frame too large" : "unexpected EOF inside a frame");
                    }
                }
                var lf = Array.IndexOf(buffer, (byte)'\n', start, end - start);
                var stop = lf < 0 ? end : lf;
                line.Write(buffer, start, stop - start);
                start = lf < 0 ? end : lf + 1;
                if (lf >= 0) return line.ToArray();
                if (line.Length > MaxFrameBytes + 1) throw new ProtocolError("frame too large");
            }
        }
    }

    // --- time ------------------------------------------------------------------------------

    const long ZeroTime = -62_135_596_800;
    const long UnixEpochTicks = 621_355_968_000_000_000;

    /// <summary>Now, in microseconds since the Unix epoch.</summary>
    internal static long NowMicros() => (DateTime.UtcNow.Ticks - UnixEpochTicks) / 10;

    internal static DateTimeOffset ToTime(long micros)
    {
        var ticks = micros * 10 + UnixEpochTicks;
        if (ticks < DateTime.MinValue.Ticks) ticks = DateTime.MinValue.Ticks;
        if (ticks > DateTime.MaxValue.Ticks) ticks = DateTime.MaxValue.Ticks;
        return new DateTimeOffset(ticks, TimeSpan.Zero);
    }

    /// <summary>
    /// An RFC 3339 time as microseconds since the Unix epoch; null when invalid or the zero time
    /// (Go's "no deadline given").
    /// </summary>
    internal static long? ParseDeadline(JsonNode? node)
    {
        var value = AsString(node);
        if (value is null)
        {
            return null;
        }
        var m = Time.Match(value);
        if (!m.Success)
        {
            return null;
        }
        int N(int g) => int.Parse(m.Groups[g].Value, CultureInfo.InvariantCulture);
        int year = N(1), month = N(2), day = N(3), hour = N(4), minute = N(5), second = N(6);
        var fraction = m.Groups[7].Value;
        var micro = fraction.Length > 0 ? int.Parse((fraction[1..] + "000000")[..6], CultureInfo.InvariantCulture) : 0;
        var zone = m.Groups[8].Value;
        var offset = 0;
        if (zone != "Z" && zone != "z")
        {
            offset = int.Parse(zone.Substring(1, 2), CultureInfo.InvariantCulture) * 60 + int.Parse(zone.Substring(4, 2), CultureInfo.InvariantCulture);
            if (zone[0] == '-') offset = -offset;
        }
        if (year < 1 || month < 1 || month > 12 || day < 1 || day > DaysInMonth(year, month)) return null;
        if (hour > 23 || minute > 59 || second > 59 || Math.Abs(offset) >= 24 * 60) return null;
        var seconds = DaysFromCivil(year, month, day) * 86_400 + hour * 3600 + minute * 60 + second - offset * 60L;
        if (seconds == ZeroTime && micro == 0) return null;
        return seconds * 1_000_000 + micro;
    }

    static int DaysInMonth(int year, int month)
    {
        if (month == 2 && year % 4 == 0 && (year % 100 != 0 || year % 400 == 0)) return 29;
        return new[] { 31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31 }[month - 1];
    }

    // Days since 1970-01-01 in the proleptic Gregorian calendar.
    static long DaysFromCivil(long year, long month, long day)
    {
        if (month <= 2) year -= 1;
        var era = (year >= 0 ? year : year - 399) / 400;
        var yoe = year - era * 400;
        var doy = (153 * (month + (month > 2 ? -3 : 9)) + 2) / 5 + day - 1;
        var doe = yoe * 365 + yoe / 4 - yoe / 100 + doy;
        return era * 146_097 + doe - 719_468;
    }

    /// <summary>The instant at millisecond precision, rounded down, so it is never after the call's own.</summary>
    internal static string FormatTime(long micros)
    {
        var t = ToTime(micros - ((micros % 1000) + 1000) % 1000);
        return t.UtcDateTime.ToString("yyyy-MM-dd'T'HH:mm:ss.fff'Z'", CultureInfo.InvariantCulture);
    }
}
