using System.Security.Cryptography;
using System.Text.Json.Nodes;
using ExtIO.Plugin;
using Webong.Ext.System.Windows;

if (args.Length != 1 || args[0] != "run" || !OperatingSystem.IsWindows() ||
    Environment.ProcessPath is not { } binary)
    return 2;

string revision;
using (var executable = File.OpenRead(binary))
    revision = Convert.ToHexString(SHA256.HashData(executable)).ToLowerInvariant();

var descriptor = new JsonObject
{
    ["apiVersion"] = "ext.io.pc/v1",
    ["identity"] = new JsonObject { ["id"] = "system.surface.windows", ["revision"] = revision, ["version"] = "1.0.0" },
    ["contracts"] = new JsonArray
    {
        new JsonObject
        {
            ["name"] = "ext.system.surface", ["version"] = "v1",
            ["operations"] = new JsonArray
            {
                new JsonObject { ["name"] = "capture", ["surface"] = "observation" },
                new JsonObject { ["name"] = "input", ["surface"] = "action" },
            },
        },
    },
};

new Guest(descriptor, Handle, maxCall: TimeSpan.FromHours(1), redactErrors: true).ServeStdio();
return 0;

static JsonNode? Handle(string contract, string operation, JsonNode? payload)
{
    if (contract != "ext.system.surface" || operation is not ("capture" or "input"))
        throw new RemoteError("unsupported", "unknown surface operation");
    try
    {
        var request = payload as JsonObject ?? throw new ArgumentException("surface request is required");
        var target = request["target"] as JsonObject ?? throw new ArgumentException("target is required");
        var surface = new WindowSurface();
        var selected = SelectWindow(surface, target);
        int timeoutMS = RequiredInt(request, "timeout_ms", 1, 3_600_000);
        using var deadline = new CancellationTokenSource(TimeSpan.FromMilliseconds(timeoutMS));
        if (operation == "capture")
        {
            var options = new CaptureOptions(
                RequiredInt(request, "max_pixels", 1, 268_435_456),
                RequiredInt(request, "max_bytes", 1, 1 << 30),
                TimeSpan.FromMilliseconds(timeoutMS));
            var image = surface.CaptureAsync(selected, options, deadline.Token).GetAwaiter().GetResult();
            return new JsonObject
            {
                ["format"] = image.Format, ["width"] = image.Width, ["height"] = image.Height,
                ["data"] = Convert.ToBase64String(image.Data), ["captured_at"] = image.CapturedAt.ToString("O"),
            };
        }
        var input = request["input"] as JsonObject ?? throw new ArgumentException("input is required");
        int maxRunes = RequiredInt(request, "max_text_runes", 1, 1_000_000);
        SendInput(surface, selected, input, maxRunes, timeoutMS, deadline.Token);
        return new JsonObject();
    }
    catch (SurfaceException error)
    {
        var code = error.Code switch
        {
            SurfaceError.Permission => "denied",
            SurfaceError.Stale => "stale",
            SurfaceError.NotFocused => "not_focused",
            SurfaceError.LimitExceeded => "limit",
            SurfaceError.InputBusy => "busy",
            _ => "unavailable",
        };
        throw new RemoteError(code, error.Message);
    }
    catch (OperationCanceledException) { throw new RemoteError("timeout", "surface operation timed out"); }
    catch (Exception error) when (error is ArgumentException or FormatException or OverflowException or InvalidOperationException)
    {
        throw new RemoteError("invalid", "invalid surface request");
    }
}

static WindowReference SelectWindow(WindowSurface surface, JsonObject target)
{
    var id = RequiredString(target, "id");
    var pid = RequiredInt(target, "pid", 1, int.MaxValue);
    var start = RequiredString(target, "process_start_id");
    var bounds = target["bounds"] as JsonObject ?? throw new ArgumentException("bounds are required");
    var expected = new SurfaceBounds(
        RequiredInt(bounds, "x", int.MinValue, int.MaxValue),
        RequiredInt(bounds, "y", int.MinValue, int.MaxValue),
        RequiredInt(bounds, "width", 1, int.MaxValue),
        RequiredInt(bounds, "height", 1, int.MaxValue));
    var match = surface.ObserveWindows(65_536).Windows.FirstOrDefault(window => window.Id == id);
    if (match is null || match.ProcessId != pid || match.ProcessStartId != start || match.Bounds != expected)
        throw new RemoteError("stale", "selected window identity or geometry changed");
    return match;
}

static void SendInput(WindowSurface surface, WindowReference target, JsonObject input, int maxRunes, int timeoutMS, CancellationToken token)
{
    var kind = RequiredString(input, "kind");
    switch (kind)
    {
        case "pointer-move": surface.Move(target, Point(input["point"] as JsonObject), token); return;
        case "pointer-click":
            var button = input["button"]?.GetValue<string>() ?? "left";
            surface.Click(target, Point(input["point"] as JsonObject), button switch
            {
                "left" => PointerButton.Left, "right" => PointerButton.Right,
                "middle" => PointerButton.Middle, _ => throw new ArgumentException("unsupported button"),
            }, token);
            return;
        case "pointer-drag": surface.Drag(target, Point(input["point"] as JsonObject), Point(input["end"] as JsonObject), token); return;
        case "pointer-scroll": surface.Scroll(target, Point(input["point"] as JsonObject),
            input["delta_x"]?.GetValue<int>() ?? 0, input["delta_y"]?.GetValue<int>() ?? 0, token); return;
        case "keyboard-text":
            surface.TypeText(target, RequiredString(input, "text"), new InputOptions(maxRunes, TimeSpan.FromMilliseconds(timeoutMS)), token);
            return;
        case "keyboard-key":
            var modifiers = (input["modifiers"] as JsonArray)?.Select(value => value?.GetValue<string>()
                ?? throw new ArgumentException("invalid modifier")).ToArray() ?? [];
            surface.PressKey(target, RequiredString(input, "key"), modifiers, token);
            return;
        default: throw new ArgumentException("unsupported input kind");
    }
}

static SurfacePoint Point(JsonObject? value)
{
    if (value is null) throw new ArgumentException("point is required");
    return new SurfacePoint(RequiredInt(value, "x", 0, int.MaxValue), RequiredInt(value, "y", 0, int.MaxValue));
}

static int RequiredInt(JsonObject value, string key, int min, int max)
{
    var result = value[key]?.GetValue<int>() ?? throw new ArgumentException($"{key} is required");
    if (result < min || result > max) throw new ArgumentOutOfRangeException(key);
    return result;
}

static string RequiredString(JsonObject value, string key) =>
    value[key]?.GetValue<string>() is { Length: > 0 } result ? result : throw new ArgumentException($"{key} is required");
