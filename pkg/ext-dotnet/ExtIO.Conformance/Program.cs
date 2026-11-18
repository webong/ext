// The guests the Go suites run on the .NET guest SDK (ExtIO):
//
//   ExtIO.Conformance conformance <descriptor.json>   pkg/ext-go/engine/plugintest, TestDotnetGuest
//   ExtIO.Conformance frames <frames-descriptor.json> tests/crosslang, TestDotnetFramesGuest
//
// The frames guest (lib/plugin/testdata/v1/frames-descriptor.json, docs/plugin-guest-frames.md)
// reports progress and calls its host. It reads inline, so it answers the operations that need a
// reader of its own (idle, collect, burst) with the public error "unsupported".
using System.Text.Json.Nodes;
using ExtIO.Plugin;

if (args.Length != 2 || args[0] is not ("conformance" or "frames"))
{
    Console.Error.WriteLine("usage: ExtIO.Conformance conformance|frames <descriptor>");
    return 2;
}
Guest.ServeStdio(args[1], args[0] == "conformance" ? Conformance : FramesGuest.Handle, redactErrors: true);
return 0;

static JsonNode? Conformance(string contract, string operation, JsonNode? payload)
{
    switch (operation)
    {
        case "wait":
            Thread.Sleep(TimeSpan.FromSeconds(60));
            break;
        case "private-error":
            throw new InvalidOperationException("secret diagnostic");
        case "public-error":
            throw new RemoteError("busy", "try later", 10);
    }
    return payload;
}

static class FramesGuest
{
    static readonly ContractRef Host = new("host.svc", "v1");

    // How a reverse call came out, in the shape the suite reads.
    static JsonNode Outcome(Func<JsonNode?> call)
    {
        try
        {
            return new JsonObject { ["result"] = call() };
        }
        catch (RemoteError e)
        {
            var error = new JsonObject { ["code"] = e.Code, ["message"] = e.Message };
            if (e.RetryAfterMs is { } r && r != 0) error["retry"] = r;
            return new JsonObject { ["error"] = error };
        }
        catch (FrameError e)
        {
            var other = e.Code is "closed" or "deadline" or "canceled" ? e.Code : "failed";
            return new JsonObject { ["error"] = new JsonObject { ["other"] = other } };
        }
    }

    static ProgressResult? Report(CallInfo call, Progress e)
    {
        try
        {
            return call.Progress(e);
        }
        catch (FrameError)
        {
            return null;
        }
    }

    public static JsonNode? Handle(string contract, string operation, JsonNode? payload)
    {
        var call = Guest.CurrentCall!;
        var p = payload as JsonObject ?? new JsonObject();
        switch (operation)
        {
            case "stream":
                Report(call, Progress.Fraction(1, 3));
                Report(call, Progress.Out("hello"));
                Report(call, Progress.Err("oops"));
                return new JsonObject { ["done"] = true };
            case "flood":
                var count = p["count"] is { } c ? (int)Wire.AsInteger(c)!.Value : 300;
                var dropped = 0;
                for (var k = 0; k < count; k++)
                {
                    if (Report(call, Progress.Log("x")) == ProgressResult.Dropped) dropped++;
                }
                return new JsonObject { ["dropped"] = dropped };
            case "limits":
                var limits = call.ProgressLimits;
                return new JsonObject
                {
                    ["declared"] = limits is not null,
                    ["events"] = limits?.EventsPerSecond ?? 0,
                    ["bytes"] = limits?.BytesPerSecond ?? 0,
                };
            case "split":
                var pieces = Progress.Split(Wire.AsString(p["text"]) ?? "");
                foreach (var piece in pieces) Report(call, Progress.Out(piece));
                return new JsonObject { ["pieces"] = pieces.Count };
            case "plain":
                bool unsupported;
                try
                {
                    call.Progress(Progress.Log("never sent"));
                    unsupported = false;
                }
                catch (FrameError e)
                {
                    unsupported = e.Code == "unsupported";
                }
                return new JsonObject { ["unsupported"] = unsupported, ["limits"] = call.ProgressLimits is not null };
            case "call":
                if (call.Reverse is null) return new JsonObject { ["error"] = new JsonObject { ["other"] = "unsupported" } };
                var inner = p["payload"]?.DeepClone();
                return Outcome(() => call.Reverse.Call(Host, Wire.AsString(p["op"]) ?? "", inner));
            case "twice":
                JsonNode? first, second;
                try
                {
                    first = call.Reverse!.Call(Host, "sign", 1);
                    second = call.Reverse.Call(Host, "sign", 2);
                }
                catch (Exception e) when (e is RemoteError or FrameError)
                {
                    throw new InvalidOperationException("a reverse call failed: " + e.Message);
                }
                return new JsonObject { ["first"] = first, ["second"] = second };
            case "idle":
            case "collect":
            case "burst":
                throw new RemoteError("unsupported", "not supported here");
            default:
                throw new InvalidOperationException("unknown operation");
        }
    }
}
