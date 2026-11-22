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
    Throws<DllNotFoundException>(() => _ = new Engine("/does/not/exist/libext_plugin_host.so"), "engine"));

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

// Reverse services (Host.Serve) against the engine's scripted guest (lib/plugin/tests/reverse.c,
// EXT_REVERSE_TEST_GUEST), and the guest registry against the relay guest of the shared registry
// cases (lib/plugin/tests/registry.c, EXT_REGISTRY_TEST_GUEST). scripts/host-cases.sh builds both.
JsonNode GuestDescriptor(string program, params string[] arguments)
{
    var start = new System.Diagnostics.ProcessStartInfo(program) { RedirectStandardOutput = true };
    foreach (var a in arguments)
    {
        start.ArgumentList.Add(a);
    }
    using var p = System.Diagnostics.Process.Start(start)!;
    var text = p.StandardOutput.ReadToEnd();
    p.WaitForExit();
    return JsonNode.Parse(text)!;
}
foreach (var name in new[] { "EXT_REVERSE_TEST_GUEST", "EXT_REGISTRY_TEST_GUEST" })
{
    if (string.IsNullOrEmpty(Environment.GetEnvironmentVariable(name)))
    {
        Console.Error.WriteLine($"skip the reverse and registry tests: set {name}, or run scripts/host-cases.sh");
        if (required)
        {
            failed.Add(name + " is not set");
        }
    }
}
var reverseGuest = Environment.GetEnvironmentVariable("EXT_REVERSE_TEST_GUEST");
if (!string.IsNullOrEmpty(reverseGuest))
{
    var reverseDescriptor = GuestDescriptor(reverseGuest, "--descriptor");
    var handled = new List<JsonNode?>();
    var services = new ReverseServices(
        call => { Check(call.Remaining > TimeSpan.Zero && !call.Canceled, "call"); return call.Request!["operation"]!.GetValue<string>() != "secret"; },
        call =>
        {
            lock (handled) { handled.Add(call.Request); }
            return call.Request!["operation"]!.GetValue<string>() switch
            {
                "sign" => JsonValue.Create("signed"),
                "empty" => null,
                "errok" => throw new RemoteException("policy", "refused for you", 5),
                _ => throw new InvalidOperationException("a private failure"),
            };
        }) { Timeout = TimeSpan.FromMilliseconds(150), MaxPayloadBytes = 64 };
    JsonNode Reply(string mode, bool serve = true)
    {
        using var h = engine.Process(reverseDescriptor, reverseGuest, ["--guest", mode]);
        if (serve)
        {
            h.Serve(services);
        }
        h.Start(TimeSpan.FromSeconds(5));
        return h.Call("reverse.test", "v1", "run", new JsonObject(), TimeSpan.FromSeconds(5))!["reply"]!;
    }
    Test("reverse calls are answered by the hooks", () =>
    {
        Check(Reply("call:sign")["payload"]!.GetValue<string>() == "signed", "sign");
        Check(handled[0]!["surface"]!.GetValue<string>() == "signing" && handled[0]!["parent"]!.GetValue<string>() == "1", "request");
        Check(Reply("call:empty")["payload"] is null, "empty");
        Check(Same(Reply("call:errok")["error"], JsonNode.Parse("""{"code":"policy","message":"refused for you","retryAfterMilliseconds":5}""")), "errok");
        Check(Reply("call:fail")["error"]!["code"]!.GetValue<string>() == "operation_failed", "fail");
    });
    Test("reverse policy and refusals", () =>
    {
        handled.Clear();
        Check(Reply("call:secret")["error"]!["code"]!.GetValue<string>() == "denied", "secret");
        Check(handled.Count == 0, "handled");
        Check(Reply("call:nothere")["error"]!["code"]!.GetValue<string>() == "reverse_undeclared", "undeclared");
        Check(Reply("call:sign", serve: false)["error"]!["code"]!.GetValue<string>() == "reverse_unavailable", "unavailable");
    });
}
var relay = Environment.GetEnvironmentVariable("EXT_REGISTRY_TEST_GUEST");
if (!string.IsNullOrEmpty(relay))
{
    var casesPath = Path.GetFullPath(Path.Combine(fixtures, "registry-cases.json"));
    var cases = Fixture("registry-cases.json");
    Host Relay(Registry registry, string name, bool denyForward = false, bool denyReverse = false)
    {
        var h = engine.Process(GuestDescriptor(relay, "--descriptor", casesPath, name), relay, ["--guest", casesPath, name],
            authorize: _ => !denyForward);
        h.Attach(registry, new ReverseServices(_ => !denyReverse, null));
        h.Start(TimeSpan.FromSeconds(5));
        return h;
    }
    bool Listed(JsonNode? list, string name) => list?.AsArray().Any(n => n!.GetValue<string>() == name) ?? false;
    foreach (var c in cases["cases"]!.AsArray().Select(c => c!.AsObject()))
    {
        Test("registry: " + c["name"]!.GetValue<string>(), () =>
        {
            var registry = new Registry(engine);
            var hosts = new Dictionary<string, Host>();
            try
            {
                foreach (var (name, contractNode) in cases["guests"]!.AsObject())
                {
                    hosts[name] = Relay(registry, name, Listed(c["forwardDeny"], name), Listed(c["reverseDeny"], name));
                    registry.Register(contractNode!.GetValue<string>(), "v1", hosts[name]);
                }
                foreach (var e in cases["edges"]!.AsArray())
                {
                    RegistryEnd End(JsonNode n) => new(n["name"]!.GetValue<string>(), n["version"]!.GetValue<string>(), n["operation"]!.GetValue<string>());
                    registry.Route(End(e!["from"]!), End(e["to"]!));
                }
                var i = 0;
                foreach (var step in c["steps"]!.AsArray().Select(s => s!.AsObject()))
                {
                    if (step["unregister"] is { } who)
                    {
                        registry.Unregister(cases["guests"]![who.GetValue<string>()]!.GetValue<string>(), "v1");
                        i++;
                        continue;
                    }
                    var target = step["call"]!.GetValue<string>();
                    var got = hosts[target].Call(cases["guests"]![target]!.GetValue<string>(), "v1", step["operation"]!.GetValue<string>(),
                        step["payload"]?.DeepClone(), TimeSpan.FromMilliseconds(step["timeoutMs"]?.GetValue<int>() ?? 5000));
                    if (step["result"] is { } want)
                    {
                        Check(Same(got, want), $"step {i}: got {got?.ToJsonString()}");
                    }
                    else
                    {
                        var code = got?["error"]?["code"]?.GetValue<string>() ?? "";
                        Check(Listed(step["resultError"], code), $"step {i}: got {got?.ToJsonString()}");
                    }
                    i++;
                }
            }
            finally
            {
                foreach (var h in hosts.Values)
                {
                    h.Dispose();
                }
            }
        });
    }
    Test("registry configuration", () =>
    {
        var registry = new Registry(engine);
        var b = Relay(registry, "b");
        Check(Throws<HostException>(() => registry.Register("Not A Name", "v1", b), "name").Status == "invalid", "invalid");
        Check(Throws<HostException>(() => registry.Register("registry.other", "v1", b), "other").Status == "unsupported", "unsupported");
        registry.Register("registry.b", "v1", b);
        Check(Throws<HostException>(() => registry.Register("registry.b", "v1", b), "twice").Status == "exists", "exists");
        RegistryEnd from = new("registry.ask", "v1", "echo-b"), to = new("registry.b", "v1", "echo");
        registry.Route(from, to);
        Check(Throws<HostException>(() => registry.Route(from, to), "route twice").Status == "exists", "route exists");
        RegistryEnd other = new("registry.ask", "v1", "other"), bad = new("registry.b", "v1", "Bad Name");
        Check(Throws<HostException>(() => registry.Route(other, bad), "bad").Status == "invalid", "route invalid");
        Check(Throws<HostException>(() => registry.Unregister("registry.x", "v1"), "unknown").Status == "not_found", "not_found");
        registry.Unregister("registry.b", "v1");
        registry.Register("registry.b", "v1", b);
        Throws<ArgumentException>(() => b.Attach(registry, new ReverseServices(_ => true, _ => null)), "attach with a handle");
        b.Dispose();
        // A disposed host has left the registry.
        Check(Throws<HostException>(() => registry.Unregister("registry.b", "v1"), "after dispose").Status == "not_found", "left");
    });
    Test("unreachable registry sources and targets are finalized", () =>
    {
        for (var i = 0; i < 3; i++)
        {
            var registry = new Registry(engine);
            registry.Register("registry.b", "v1", Relay(registry, "b"));
        }
        GC.Collect();
        GC.WaitForPendingFinalizers();
        GC.Collect();
    });
}

Console.WriteLine($"{passed} passed, {failed.Count} failed");
foreach (var f in failed)
{
    Console.WriteLine($"  {f}");
}
return failed.Count == 0 ? 0 : 1;
