// The .NET guest SDK (ExtIO) against the shared fixtures of lib/plugin/testdata/v1, which the Go
// contract computes and every SDK reproduces (frames, the sender's rate limiter and the wire
// model), and a served session end to end on in-memory streams.
//
//   dotnet run --project pkg/ext-dotnet/ExtIO.Tests [fixtures-dir]
using System.Text;
using System.Text.Json.Nodes;
using ExtIO.Plugin;

var fixtures = args.Length > 0 ? args[0] : Path.Combine(AppContext.BaseDirectory, "../../../../../../lib/plugin/testdata/v1");
JsonNode Fixture(string name) => JsonNode.Parse(File.ReadAllText(Path.Combine(fixtures, name)))!;
string S(JsonNode? n) => n!.GetValue<string>();
long L(JsonNode? n) => n!.GetValue<long>();
bool B(JsonNode? n) => n is not null && n.GetValue<bool>();

var passed = 0;
var failed = new List<string>();
void Test(string name, Action body)
{
    try
    {
        body();
        passed++;
        Console.WriteLine($"ok   {name}");
    }
    catch (Exception e)
    {
        failed.Add(name);
        Console.WriteLine($"FAIL {name}\n  {e}");
    }
}

static void Check(bool condition, string what)
{
    if (!condition) throw new Exception(what);
}

// Collects the first few mismatches so a failure says what diverged.
var mismatches = new List<string>();
var mismatchCount = 0;
void Note(string section, JsonNode kase, string what)
{
    mismatchCount++;
    if (mismatches.Count < 12) mismatches.Add($"{section}: {S(kase["name"])}: {what}");
}
void NoMismatches()
{
    var (count, first) = (mismatchCount, string.Join("\n", mismatches));
    mismatchCount = 0;
    mismatches.Clear();
    Check(count == 0, $"{count} mismatches, the first:\n{first}");
}

static bool Accepts(Action f)
{
    try
    {
        f();
        return true;
    }
    catch (Exception e) when (e is ProtocolError or ArgumentException)
    {
        return false;
    }
}

byte[] U(string s) => Encoding.UTF8.GetBytes(s);

Test("frames are judged as the Go contract judges them", () =>
{
    var root = Fixture("frame-cases.json");
    var total = 0;
    var sections = new Dictionary<string, Action<byte[], string>>
    {
        ["progress"] = (raw, _) => Frames.ValidateProgressFrame(Wire.Decode(raw)),
        ["reverseRequest"] = (raw, _) => Frames.ValidateReverseRequest(Wire.Decode(raw)),
        ["reverseResponse"] = (raw, id) => Frames.ValidateReverseResponse(Wire.Decode(raw), id),
    };
    foreach (var (section, judge) in sections)
    {
        var cases = root[section]!.AsArray();
        var valid = cases.Count(c => B(c!["valid"]));
        Check(cases.Count >= 20 && valid >= 5 && cases.Count - valid >= 5, $"{section} proves little");
        foreach (var kase in cases)
        {
            total++;
            var got = Accepts(() => judge(U(S(kase!["frame"])), kase!["id"] is { } id ? S(id) : ""));
            if (got != B(kase!["valid"])) Note(section, kase!, $"accepted {got}: {S(kase["frame"])}");
        }
    }
    Check(total >= 500, $"only {total} frame cases");
    NoMismatches();
});

Test("the rate limiter reproduces every step", () =>
{
    var cases = Fixture("progress-limiter-cases.json")["cases"]!.AsArray();
    Check(cases.Count >= 10, "too few limiter cases");
    foreach (var kase in cases)
    {
        var limiter = new Frames.Limiter(L(kase!["eventsPerSecond"]), L(kase["bytesPerSecond"]), 0);
        var steps = kase["steps"]!.AsArray();
        for (var i = 0; i < steps.Count; i++)
        {
            var step = steps[i]!;
            var got = limiter.Admit(L(step["textBytes"]), L(step["atMs"]) * 1_000_000);
            var want = B(step["admitted"]);
            var dropped = step["dropped"] is { } d ? (ulong)L(d) : 0;
            if (got.HasValue != want || (want && got != dropped)) Note("limiter", kase, $"step {i}: {got}, want {want} {dropped}");
        }
        if (limiter.Pending() != (ulong)L(kase["pending"])) Note("limiter", kase, "pending count");
    }
    NoMismatches();
});

Test("the wire model is judged as the Go contract judges it", () =>
{
    var root = Fixture("model-cases.json");
    var descriptors = root["descriptors"]!.AsArray();
    Check(descriptors.Count > 100, "too few descriptor cases");
    foreach (var kase in descriptors)
    {
        var got = Accepts(() => Guest.ValidateDescriptor(Wire.Decode(U(S(kase!["descriptor"])))));
        if (got != B(kase!["valid"])) Note("descriptor", kase, $"accepted {got}: {S(kase["descriptor"])}");
    }
    var selected = root["requestDescriptor"]!.AsObject();
    Guest.ValidateDescriptor(selected);
    var requests = root["requests"]!.AsArray();
    Check(requests.Count > 100, "too few request cases");
    foreach (var kase in requests)
    {
        var ran = new List<CallInfo>();
        var guest = new Guest(selected, (_, _, _) =>
        {
            ran.Add(Guest.CurrentCall!);
            return null;
        });
        var link = new Frames.Link(new MemoryStream(), new MemoryStream());
        try
        {
            guest.Invoke(Wire.Decode(U(S(kase!["request"]))), link);
        }
        catch (ProtocolError)
        {
        }
        if ((ran.Count > 0) != B(kase!["valid"]))
        {
            Note("request", kase, $"accepted {ran.Count > 0}: {S(kase["request"])}");
            continue;
        }
        if (ran.Count == 0) continue;
        // The call gets a reporter when its operation declared progress and the request carries a
        // rate; the rate has its defaults filled in.
        var limits = ran[0].ProgressLimits;
        if ((limits is not null) != B(kase["reporter"]))
        {
            Note("request", kase, $"reporter {limits is not null}");
        }
        else if (limits is not null && (limits.EventsPerSecond != L(kase["events"]) || limits.BytesPerSecond != L(kase["bytes"])))
        {
            Note("request", kase, $"rate {limits}");
        }
    }
    NoMismatches();
});

Test("strict JSON refuses what Go refuses", () =>
{
    foreach (var bad in new[] { "{\"a\":1,\"a\":2}", "[1,]", "01", "NaN", "\"\\ud800\"", "\"\\udc00\"", "1 2", "{\"a\":1", "\"\t\"", "-", "1.", "1e" })
    {
        Check(!Accepts(() => Wire.Decode(U(bad))), $"accepted {bad}");
    }
    Check(!Accepts(() => Wire.Decode(new byte[] { 0x22, 0xff, 0x22 })), "accepted invalid UTF-8");
    Check(!Accepts(() => Wire.Decode(U(new string('[', 66) + new string(']', 66)))), "accepted 66 levels");
    Check(Accepts(() => Wire.Decode(U(new string('[', 65) + new string(']', 65)))), "refused 65 levels");
    var v = Wire.Decode(U("{\"n\":123456789012345678901234567890,\"f\":1.50,\"s\":\"\\ud83d\\ude00\"}"))!;
    Check(Encoding.ASCII.GetString(Wire.Encode(v)) == "{\"n\":123456789012345678901234567890,\"f\":1.50,\"s\":\"\\ud83d\\ude00\"}", "numbers keep their text");
    Check(Wire.AsInteger(v["n"]) is not null && Wire.AsInteger(v["f"]) is null, "whole numbers");
});

Test("encoding refuses what JSON cannot carry", () =>
{
    Check(Throws(() => Wire.Encode(JsonValue.Create(double.NaN))), "NaN encoded");
    Check(Throws(() => Wire.Encode(JsonValue.Create("\ud800"))), "lone surrogate encoded");
    Check(Encoding.ASCII.GetString(Wire.Encode(new JsonObject { ["a"] = 1.0, ["b"] = "é\n", ["c"] = new JsonArray(1, true, null) })) ==
        "{\"a\":1.0,\"b\":\"\\u00e9\\n\",\"c\":[1,true,null]}", "compact ASCII");
});

static bool Throws(Action f)
{
    try
    {
        f();
        return false;
    }
    catch (EncodeError)
    {
        return true;
    }
}

Test("remote errors are bounded", () =>
{
    Check(!Accepts(() => _ = new RemoteError("Bad", "x")), "accepted an upper-case code");
    Check(!Accepts(() => _ = new RemoteError("busy", new string('x', 4097))), "accepted a long message");
    Check(!Accepts(() => _ = new RemoteError("busy", "x", -1)), "accepted a negative retry");
    Check(new RemoteError("busy", "x", 5).RetryAfterMs == 5, "retry kept");
});

Test("progress text splits on character boundaries", () =>
{
    var text = new string('a', 2047) + "😀" + new string('b', 3000);
    var pieces = Progress.Split(text);
    Check(pieces.All(p => Encoding.UTF8.GetByteCount(p) <= Progress.MaxText), "a piece is too long");
    Check(string.Concat(pieces) == text, "pieces lost text");
    Check(pieces[0].Length == 2047, "the emoji was cut");
});

string Hello() => "{\"apiVersion\":\"ext.io.pc/v1\",\"id\":\"hello\",\"operation\":\"session.hello\",\"deadline\":\"2099-01-01T00:00:00Z\"}\n";
string Request(string id, string op, string payload) =>
    "{\"apiVersion\":\"ext.io.pc/v1\",\"id\":\"" + id + "\",\"plugin\":{\"id\":\"ctx/conformance\",\"revision\":\"fixture-1\"}," +
    "\"contract\":{\"name\":\"ext.conformance\",\"version\":\"v1\"},\"operation\":\"" + op + "\",\"deadline\":\"2099-01-01T00:00:00Z\",\"payload\":" + payload + "}\n";
var descriptor = (JsonObject)JsonNode.Parse("{\"apiVersion\":\"ext.io.pc/v1\",\"identity\":{\"id\":\"ctx/conformance\",\"revision\":\"fixture-1\"},\"contracts\":[{\"name\":\"ext.conformance\",\"version\":\"v1\",\"operations\":[{\"name\":\"echo\"},{\"name\":\"fail\"},{\"name\":\"busy\"}]}]}")!;

List<JsonNode> Serve(Guest guest, string input)
{
    var output = new MemoryStream();
    guest.Serve(new MemoryStream(U(input)), output);
    return Encoding.UTF8.GetString(output.ToArray()).Split('\n', StringSplitOptions.RemoveEmptyEntries).Select(l => JsonNode.Parse(l)!).ToList();
}

Test("a served session answers hello and calls in order", () =>
{
    var guest = new Guest(descriptor, (_, op, payload) => op switch
    {
        "echo" => payload,
        "busy" => throw new RemoteError("busy", "try later", 250),
        _ => throw new InvalidOperationException("internal detail"),
    });
    var answers = Serve(guest, Hello() + Request("1", "echo", "{\"x\":[1,2.5,\"é\"]}") + Request("2", "busy", "null") + Request("3", "fail", "null") + Request("4", "missing", "null"));
    Check(answers.Count == 5, $"{answers.Count} answers");
    Check(S(answers[0]["payload"]!["identity"]!["id"]) == "ctx/conformance", "hello answers the descriptor");
    Check(Encoding.ASCII.GetString(Wire.Encode(answers[1]["payload"])) == "{\"x\":[1,2.5,\"\\u00e9\"]}", "echo");
    Check(S(answers[2]["error"]!["code"]) == "busy" && L(answers[2]["error"]!["retryAfterMilliseconds"]) == 250, "remote error");
    Check(S(answers[3]["error"]!["code"]) == "provider_error" && S(answers[3]["error"]!["message"]) == "internal detail", "provider error");
    Check(S(answers[4]["error"]!["code"]) == "invalid_request", "unknown operation");

    var redacted = new Guest(descriptor, (_, _, _) => throw new InvalidOperationException("secret"), redactErrors: true);
    var hidden = Serve(redacted, Hello() + Request("1", "fail", "null"));
    Check(S(hidden[1]["error"]!["code"]) == "operation_failed" && !hidden[1].ToJsonString().Contains("secret"), "redacted");
});

Test("a session without hello first is a protocol error", () =>
{
    var guest = new Guest(descriptor, (_, _, p) => p);
    Check(!Accepts(() => Serve(guest, Request("1", "echo", "1"))), "served a request before hello");
    Check(!Accepts(() => Serve(guest, Hello() + "{\"apiVersion\":\"ext.io.pc/v1\",\"id\":\"1\",\"bogus\":1}\n")), "served an unknown field");
    Check(!Accepts(() => Serve(guest, Hello() + "{\"id\":\"1\"")), "served a cut frame");
});

Test("the capability router checks payloads and results", () =>
{
    var d = (JsonObject)JsonNode.Parse("{\"apiVersion\":\"ext.io.pc/v1\",\"identity\":{\"id\":\"p\",\"revision\":\"1\"},\"contracts\":[{\"name\":\"ext.health\",\"version\":\"v1\",\"operations\":[{\"name\":\"check\"}]},{\"name\":\"ext.stream\",\"version\":\"v1\",\"operations\":[{\"name\":\"open\"},{\"name\":\"read\"},{\"name\":\"close\"}]}]}")!;
    var healthy = "healthy";
    var router = new Capability.Router()
        .Health(() => new JsonObject { ["status"] = healthy, ["ready"] = true })
        .Stream(_ => "s1", (id, seq, limit) => new JsonObject { ["items"] = new JsonArray(id, seq, limit), ["done"] = true }, _ => { });
    var guest = new Guest(d, router.Handle);
    string Call(string contract, string op, string payload) =>
        "{\"apiVersion\":\"ext.io.pc/v1\",\"id\":\"1\",\"plugin\":{\"id\":\"p\",\"revision\":\"1\"},\"contract\":{\"name\":\"" + contract +
        "\",\"version\":\"v1\"},\"operation\":\"" + op + "\",\"deadline\":\"2099-01-01T00:00:00Z\",\"payload\":" + payload + "}\n";
    var answers = Serve(guest, Hello() + Call("ext.health", "check", "{}") + Call("ext.stream", "read", "{\"id\":\"s1\",\"sequence\":1,\"limit\":2}") +
        Call("ext.stream", "read", "{\"id\":\"s1\",\"sequence\":0,\"limit\":2}") + Call("ext.stream", "open", "{\"parameters\":null}"));
    Check(answers[1]["payload"]!.ToJsonString() == "{\"status\":\"healthy\",\"ready\":true}", "health");
    Check(answers[2]["payload"]!.ToJsonString() == "{\"items\":[\"s1\",1,2],\"done\":true}", "read");
    Check(S(answers[3]["error"]!["code"]) == "invalid_input", "a bad read is invalid input");
    Check(S(answers[4]["payload"]!["id"]) == "s1", "open");
    healthy = "fine";
    Check(S(Serve(guest, Hello() + Call("ext.health", "check", "{}"))[1]["error"]!["code"]) == "provider_error", "a bad result is the plugin's error");
});

Console.WriteLine($"{passed} passed, {failed.Count} failed");
return failed.Count == 0 ? 0 : 1;
