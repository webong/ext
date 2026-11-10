// The host binding (ExtIO.Host) on the C engine. It needs the engine's shared library
// (EXT_HOST_LIBRARY) and the reference guest twice: EXT_GO_GUEST, the JSON-line Go guest, and
// EXT_GO_SHARED, the same guest as a C ABI library. It runs every case of
// lib/plugin/testdata/v1/host-cases.json on both, then what the cases leave to the binding.
// Without them it says so and exits 0, or fails when EXT_HOST_REQUIRED=1. scripts/host-cases.sh
// builds everything and runs it: dotnet run --project pkg/ext-dotnet/ExtIO.Host.Tests [fixtures-dir].
using System.Text;
using System.Text.Json.Nodes;
using ExtIO.Host;

var fixtures = args.Length > 0 ? args[0] : Path.Combine(AppContext.BaseDirectory, "../../../../../../lib/plugin/testdata/v1");
var required = Environment.GetEnvironmentVariable("EXT_HOST_REQUIRED") == "1";
foreach (var name in new[] { "EXT_HOST_LIBRARY", "EXT_GO_GUEST", "EXT_GO_SHARED" })
{
    if (string.IsNullOrEmpty(Environment.GetEnvironmentVariable(name)))
    {
        Console.Error.WriteLine($"skip ExtIO.Host tests: set {name}, or run scripts/host-cases.sh");
        return required ? 1 : 0;
    }
}

var engine = new Engine();
var guest = Environment.GetEnvironmentVariable("EXT_GO_GUEST")!;
var shared = Environment.GetEnvironmentVariable("EXT_GO_SHARED")!;
JsonNode Fixture(string name) => JsonNode.Parse(File.ReadAllText(Path.Combine(fixtures, name)))!;

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
    if (!condition)
    {
        throw new Exception(what);
    }
}

static T Throws<T>(Action f, string what) where T : Exception
{
    try
    {
        f();
    }
    catch (T e)
    {
        return e;
    }
    catch (Exception e)
    {
        throw new Exception($"{what}: expected {typeof(T).Name}, got {e.GetType().Name}: {e.Message}");
    }
    throw new Exception($"{what}: expected {typeof(T).Name}, nothing was thrown");
}

static bool Same(JsonNode? a, JsonNode? b) => JsonNode.DeepEquals(a, b);

// Every case of host-cases.json, on both reference guests.
var spec = Fixture("host-cases.json");
var descriptor = Fixture(spec["descriptor"]!.GetValue<string>());
var contract = spec["contract"]!;
string contractName = contract["name"]!.GetValue<string>(), contractVersion = contract["version"]!.GetValue<string>();

foreach (var guestKind in spec["guests"]!.AsArray().Select(g => g!.GetValue<string>()))
{
    var path = guestKind == "jsonline" ? guest : shared;
    foreach (var c in spec["cases"]!.AsArray().Select(c => c!.AsObject()))
    {
        Test($"host case {guestKind}/{c["name"]}", () =>
        {
            var d = descriptor.DeepClone();
            if (c["revision"] is JsonNode revision)
            {
                d["identity"]!["revision"] = revision.GetValue<string>();
            }
            Policy? verify = c["verify"]?.GetValue<string>() == "deny" ? _ => false : null;
            Policy? authorize = c["authorize"]?.GetValue<string>() == "deny" ? _ => false : null;
            using var h = guestKind == "jsonline"
                ? engine.Process(d, path, verify: verify, authorize: authorize)
                : engine.SharedLibrary(d, path, verify, authorize);
            string got;
            try
            {
                h.Start(TimeSpan.FromSeconds(5));
                got = "ok";
            }
            catch (HostException e)
            {
                got = e.Status;
            }
            var want = c["start"]?.GetValue<string>() ?? "ok";
            Check(got == want, $"start: got {got}, want {want}");
            if (got != "ok")
            {
                return;
            }
            var steps = c["steps"]?.AsArray() ?? [];
            for (var i = 0; i < steps.Count; i++)
            {
                var step = steps[i]!.AsObject();
                if (step["close"] is not null)
                {
                    h.Close();
                }
                else if (step["state"] is JsonNode state)
                {
                    Check(h.State == state.GetValue<string>(), $"step {i}: state {h.State}, want {state}");
                }
                else
                {
                    RunCall(h, i, step);
                }
            }
        });
    }
}

void RunCall(Host h, int i, JsonObject step)
{
    var where = $"step {i} ({step["call"]})";
    var timeout = TimeSpan.FromMilliseconds(step["timeoutMs"]?.GetValue<int>() ?? 3000);
    string outcome = "result";
    JsonNode? result = null;
    RemoteException? remote = null;
    string? status = null;
    try
    {
        result = h.Call(contractName, contractVersion, step["call"]!.GetValue<string>(), step["payload"], timeout);
    }
    catch (RemoteException e)
    {
        (outcome, remote) = ("error", e);
    }
    catch (HostException e)
    {
        (outcome, status) = ("status", e.Status);
    }
    if (step["fails"] is not null)
    {
        Check(outcome != "result", $"{where} succeeded with {result?.ToJsonString()}");
    }
    else if (step.ContainsKey("result"))
    {
        Check(outcome == "result" && Same(result, step["result"]), $"{where}: got {outcome} {result?.ToJsonString()} {remote?.Message}{status}, want {step["result"]?.ToJsonString()}");
    }
    else if (step["error"] is JsonObject error)
    {
        Check(outcome == "error", $"{where}: got {outcome} {status}");
        Check(remote!.ErrorCode == error["code"]!.GetValue<string>(), $"{where}: code {remote.ErrorCode}");
        Check(remote.Message == error["message"]!.GetValue<string>(), $"{where}: message {remote.Message}");
        Check((remote.RetryAfterMilliseconds ?? 0) == (error["retryAfterMilliseconds"]?.GetValue<long>() ?? 0), $"{where}: retry {remote.RetryAfterMilliseconds}");
    }
    else
    {
        var allowed = step["status"] is JsonArray list ? list.Select(s => s!.GetValue<string>()).ToArray() : [step["status"]!.GetValue<string>()];
        Check(outcome == "status" && allowed.Contains(status), $"{where}: got {outcome} {status}{remote?.Message}, want {step["status"]!.ToJsonString()}");
    }
}

// What the cases leave to the binding: threads, cancellation, policies, envelopes and ownership.
var conformance = Fixture("descriptor.json");
var echo = conformance["contracts"]![0]!;
string echoName = echo["name"]!.GetValue<string>(), echoVersion = echo["version"]!.GetValue<string>();
JsonNode Value(int n) => new JsonObject { ["value"] = n };

Test("a missing library is reported", () =>
    Throws<DllNotFoundException>(() => _ = new Engine("/does/not/exist/libext_host.so"), "engine"));

Test("policies see JSON", () =>
{
    string? seenVerify = null, seenOperation = null;
    JsonNode? seenPayload = null;
    using (var h = engine.Process(conformance, guest,
               verify: d => { seenVerify = d!["identity"]!["id"]!.GetValue<string>(); return true; },
               authorize: r => { seenOperation = r!["operation"]!.GetValue<string>(); seenPayload = r["payload"]?.DeepClone(); return true; }))
    {
        h.Start();
        Check(Same(h.Call(echoName, echoVersion, "echo", Value(1)), Value(1)), "echo");
    }
    Check(seenVerify == "ctx/conformance" && seenOperation == "echo" && Same(seenPayload, Value(1)), "policies");
});

Test("a throwing policy denies and keeps the session", () =>
{
    using var h = engine.Process(conformance, guest, authorize: _ => throw new InvalidOperationException("policy bug"));
    h.Start();
    Check(Throws<HostException>(() => h.Call(echoName, echoVersion, "echo", Value(1)), "call").Status == "denied", "denied");
    Check(h.State == "ready", "state");
});

Test("a policy cannot reenter its host", () =>
{
    string? reentered = null;
    Host? self = null;
    using var h = engine.Process(conformance, guest, authorize: _ =>
    {
        try
        {
            _ = self!.State;
            reentered = "allowed";
        }
        catch (HostException e)
        {
            reentered = e.Status;
        }
        return true;
    });
    self = h;
    h.Start();
    Check(Same(h.Call(echoName, echoVersion, "echo", Value(2)), Value(2)), "echo");
    Check(reentered == "invalid", $"reentered: {reentered}");
});

Test("calls from threads", () =>
{
    using var h = engine.Process(conformance, guest);
    h.Start();
    var results = Enumerable.Range(0, 8).AsParallel().WithDegreeOfParallelism(8)
        .Select(n => h.Call(echoName, echoVersion, "echo", Value(n))!["value"]!.GetValue<int>()).ToArray();
    Check(results.Order().SequenceEqual(Enumerable.Range(0, 8)), "results");
});

Test("a cancellation token cancels the call", () =>
{
    using var h = engine.Process(conformance, guest);
    h.Start();
    using var cts = new CancellationTokenSource(TimeSpan.FromMilliseconds(200));
    var started = Environment.TickCount64;
    var e = Throws<HostException>(() => h.Call(echoName, echoVersion, "wait", null, TimeSpan.FromSeconds(10), cts.Token), "wait");
    Check(e.Status == "canceled", $"status {e.Status}");
    Check(Environment.TickCount64 - started < 3000, "it waited for the timeout");
});

Test("dispose waits for the calls in flight", () =>
{
    var h = engine.Process(conformance, guest);
    h.Start();
    var call = Task.Run(() =>
    {
        try { h.Call(echoName, echoVersion, "wait", null, TimeSpan.FromSeconds(10)); return "result"; }
        catch (HostException e) { return e.Status; }
    });
    Thread.Sleep(200);
    h.Dispose();
    Check(call.Wait(TimeSpan.FromSeconds(5)), "the call did not return");
    // Closing kills the child mid-call: the call fails, as "closed" or as the I/O error it saw.
    Check(call.Result is "closed" or "io", $"call ended with {call.Result}");
    Check(h.Disposed, "disposed");
});

Test("invoke takes envelopes", () =>
{
    using var h = engine.Process(conformance, guest);
    h.Start();
    var request = new JsonObject
    {
        ["apiVersion"] = "ext.plugin/v1", ["id"] = "r1", ["plugin"] = conformance["identity"]!.DeepClone(),
        ["contract"] = new JsonObject { ["name"] = echoName, ["version"] = echoVersion },
        ["operation"] = "echo", ["deadline"] = "2099-01-01T00:00:00Z", ["payload"] = Value(3),
    };
    var response = JsonNode.Parse(h.Invoke(Encoding.UTF8.GetBytes(request.ToJsonString())))!;
    Check(response["id"]!.GetValue<string>() == "r1" && Same(response["payload"], Value(3)), "envelope");
});

Test("relative paths are refused", () =>
{
    Check(Throws<HostException>(() => engine.SharedLibrary(conformance, "relative/libguest.so"), "shared").Status == "invalid", "shared");
    Check(Throws<HostException>(() => engine.Process(conformance, "relative/guest"), "process").Status == "invalid", "process");
});

Test("a disposed host refuses use", () =>
{
    var h = engine.Process(conformance, guest);
    h.Dispose();
    h.Dispose(); // once is enough; again is harmless
    h.Close();
    Check(Throws<HostException>(() => h.Start(), "start").Status == "closed", "closed");
});

Test("stderr is kept", () =>
{
    using var h = engine.Process(conformance, guest);
    h.Start();
    Check(h.Stderr() is not null, "stderr");
});

Test("drain waits for the calls and closes", () =>
{
    using var h = engine.Process(conformance, guest);
    h.Start();
    h.Call(echoName, echoVersion, "echo", Value(4));
    h.Drain(TimeSpan.FromSeconds(1));
    Check(h.State == "closed", h.State);
});

Test("unreachable hosts are finalized", () =>
{
    for (var i = 0; i < 3; i++)
    {
        var h = engine.Process(conformance, guest);
        h.Start();
        h.Call(echoName, echoVersion, "echo", Value(5));
    }
    GC.Collect();
    GC.WaitForPendingFinalizers();
});

Console.WriteLine($"{passed} passed, {failed.Count} failed");
foreach (var f in failed)
{
    Console.WriteLine($"  {f}");
}
return failed.Count == 0 ? 0 : 1;
