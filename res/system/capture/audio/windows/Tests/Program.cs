// Ext.Audio.Windows on WASAPI: argument checks, the shared playback fixtures (Fixtures.cs), endpoint
// listing, and, on the endpoints this machine has, capture and playback format handling and
// playback backpressure. The error codes and the capability report are tested without a device
// where the path can be reached without one. A hosted runner usually has no audio endpoint (and may
// have no audio service, which SystemAudio.Status reports), so what needs one says why and is
// skipped, or fails when EXT_AUDIO_REQUIRED=1. Nothing is played aloud: playback writes silence.
// Run it with dotnet run --project res/system/capture/audio/windows/Tests.
using System.Runtime.InteropServices;
using NAudio.Wave;
using Webong.Ext.System.Windows;

var required = Environment.GetEnvironmentVariable("EXT_AUDIO_REQUIRED") == "1";
var passed = 0;
var skipped = 0;
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

void Skip(string name, string why)
{
    if (required)
    {
        failed.Add(name);
        Console.WriteLine($"FAIL {name}\n  {why} and EXT_AUDIO_REQUIRED=1");
        return;
    }
    skipped++;
    Console.WriteLine($"skip {name}: {why}");
}

static void Check(bool condition, string what)
{
    if (!condition) throw new Exception(what);
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
    throw new Exception($"{what}: no {typeof(T).Name}");
}

static void Code<T>(string code, Action f, string what) where T : Exception
{
    var e = Throws<T>(f, what);
    var got = e switch
    {
        AudioArgumentException a => a.Code,
        AudioException a => a.Code,
        _ => null,
    };
    if (got != code) throw new Exception($"{what}: code {got} ({e.Message}), want {code}");
}

Test("an endpoint id is required", () =>
{
    foreach (var id in new[] { "", " ", "\t", null })
    {
        var what = id is null ? "null" : id.Length.ToString();
        Code<AudioArgumentException>("invalid", () => SystemAudio.Capture(id!).Dispose(), $"capture {what}");
        Code<AudioArgumentException>("invalid", () => SystemAudio.Capture(id!, systemAudio: true).Dispose(), $"loopback {what}");
        Code<AudioArgumentException>("invalid", () => SystemAudio.Playback(id!).Dispose(), $"playback {what}");
    }
});

Test("the error codes are the Swift playback codes and in codes.json", () =>
{
    var codes = Fixtures.Codes();
    // Swift SystemAudioPlaybackError.code: invalidPacket, packetTooLarge, bufferFull, closed, nativeFailure.
    foreach (var code in new[] { "invalid", "too_large", "busy", "canceled", "io", "unavailable", "unsupported" })
        Check(codes.Contains(code), $"{code} is not in testdata/codes.json");
    Check(new AudioArgumentException("invalid", "x") is ArgumentException, "an argument error is an ArgumentException");
    Check(new AudioPacketException("invalid", "x") is AudioArgumentException, "a packet error is an argument error");
});

Test("a direction mismatch is invalid and an endpoint that is not active unavailable", () =>
{
    Code<AudioArgumentException>("invalid", () => SystemAudio.CheckDirection(NAudio.CoreAudioApi.DataFlow.Capture,
        NAudio.CoreAudioApi.DataFlow.Render, "playback"), "capture endpoint for playback");
    SystemAudio.CheckDirection(NAudio.CoreAudioApi.DataFlow.Render, NAudio.CoreAudioApi.DataFlow.Render, "same");
    foreach (var state in new[] { NAudio.CoreAudioApi.DeviceState.Disabled, NAudio.CoreAudioApi.DeviceState.NotPresent,
        NAudio.CoreAudioApi.DeviceState.Unplugged })
        Code<AudioException>("unavailable", () => SystemAudio.CheckState(state), $"state {state}");
    SystemAudio.CheckState(NAudio.CoreAudioApi.DeviceState.Active);
});

Test("WASAPI failures are io, an unknown endpoint unavailable, no service unsupported", () =>
{
    (int HResult, string Code)[] cases =
    {
        (SystemAudio.ENotFound, "unavailable"),
        (SystemAudio.DeviceInvalidated, "unavailable"),
        (SystemAudio.ServiceNotRunning, "unsupported"),
        (unchecked((int)0x88890008), "io"), // AUDCLNT_E_UNSUPPORTED_FORMAT
        (unchecked((int)0x80004005), "io"), // E_FAIL
    };
    foreach (var (hresult, code) in cases)
    {
        var inner = new COMException("native", hresult);
        var e = SystemAudio.Native(inner, "cannot open audio playback");
        Check(e.Code == code, $"0x{hresult:x8} is {e.Code}, want {code}");
        Check(ReferenceEquals(e.InnerException, inner), "the COMException is not the inner exception");
    }
});

Test("the capability report", () =>
{
    var none = SystemAudio.Status(() => throw new AudioException("unsupported", "no service"));
    Check(none.State == "unsupported" && none.Reason == "unsupported", $"no service: {none}");
    var failed = SystemAudio.Status(() => throw new AudioException("io", "failed"));
    Check(failed.State == "unavailable" && failed.Reason is null, $"a failed listing: {failed}");
    var ok = SystemAudio.Status(() => { });
    Check(ok.State == "complete" && ok.Reason is null, $"a listing: {ok}");
    var real = SystemAudio.Status();
    Check(real.State is "complete" or "unsupported" or "unavailable", $"this session: {real}");
    if (!OperatingSystem.IsWindows()) Check(real.State == "unsupported", $"not Windows: {real}");
});

Test("a full buffer is busy and a closed sink canceled, without a device", () =>
{
    var output = new SystemAudioPlayback(SystemAudioPlayback.Buffer());
    var packet = new byte[SystemAudioPlayback.MaxPacketBytes];
    // 250 ms is 48 000 bytes: twelve full packets fit, so the thirteenth has no room.
    for (var i = 0; i < 12; i++) output.Write(packet);
    Code<AudioException>("busy", () => output.Write(packet), "a packet over the buffer");
    Code<AudioPacketException>("invalid", () => output.Write(new byte[3]), "a partial frame still comes first");
    output.Dispose();
    output.Dispose();
    Code<AudioException>("canceled", () => output.Write(new byte[4]), "a write after dispose");
    Code<AudioException>("canceled", () => output.Write(new byte[3]), "a closed sink is canceled before the packet is judged");
});

Test("playback is PCM16 stereo 48 kHz", () =>
{
    Check(SystemAudioPlayback.SampleRate == 48_000, "sample rate");
    Check(SystemAudioPlayback.Channels == 2, "channels");
    Check(SystemAudioPlayback.BitsPerSample == 16, "bits per sample");
    var format = new WaveFormat(SystemAudioPlayback.SampleRate, SystemAudioPlayback.BitsPerSample, SystemAudioPlayback.Channels);
    Check(format.Encoding == WaveFormatEncoding.Pcm && format.BlockAlign == 4 && format.AverageBytesPerSecond == 192_000,
        $"format {format}");
});

// The shared fixtures (res/system/testdata/audio/v1, the playback section) need no device.
Fixtures.Playback(Test);

Test("Write's packet errors are ArgumentExceptions with a code", () =>
{
    var e = Throws<AudioPacketException>(() => SystemAudioPlayback.CheckPacket(3, SystemAudioPlayback.MaxPacketBytes, "bytes"), "a partial frame");
    Check(e.Code == "invalid" && e.ParamName == "bytes" && e is ArgumentException, $"{e.Code} {e.ParamName}");
    e = Throws<AudioPacketException>(() => SystemAudioPlayback.CheckPacket(SystemAudioPlayback.MaxPacketBytes + 4, SystemAudioPlayback.MaxPacketBytes), "a large packet");
    Check(e.Code == "too_large", e.Code);
});

Fixtures.Capability(Test);

IReadOnlyList<AudioEndpoint>? endpoints = null;
string? noAudio = null;
var status = SystemAudio.Status();
if (status.State != "complete")
{
    noAudio = $"no audio endpoints can be listed (status {status.State} ({status.Reason}))";
}
else
{
    endpoints = SystemAudio.Endpoints();
}

if (endpoints is null)
{
    Skip("endpoints are listed", noAudio!);
}
else
{
    Test("endpoints are listed", () =>
    {
        Console.WriteLine($"     {endpoints.Count} active endpoints");
        foreach (var e in endpoints)
        {
            Console.WriteLine($"     {e.Direction}: {e.Name} ({e.Id})");
            Check(!string.IsNullOrWhiteSpace(e.Id), "an endpoint without an id");
            Check(e.Name is not null, "an endpoint without a name");
            Check(Enum.IsDefined(e.Direction), $"direction {e.Direction}");
        }
        Check(endpoints.Select(e => e.Id).Distinct().Count() == endpoints.Count, "an endpoint is listed twice");
        // The Windows endpoint id names its flow: {0.0.0.00000000} is render, {0.0.1.00000000} capture.
        foreach (var e in endpoints)
        {
            var flow = e.Direction == AudioEndpointDirection.Playback ? "{0.0.0." : "{0.0.1.";
            Check(e.Id.StartsWith(flow, StringComparison.OrdinalIgnoreCase), $"{e.Id} is not a {e.Direction} id");
        }
    });

    Test("an unknown endpoint is refused", () =>
    {
        const string unknown = "{0.0.0.00000000}.{00000000-0000-0000-0000-000000000000}";
        Code<AudioException>("unavailable", () => SystemAudio.Playback(unknown).Dispose(), "playback");
        Code<AudioException>("unavailable", () => SystemAudio.Capture(unknown).Dispose(), "capture");
    });
}

var playback = endpoints?.FirstOrDefault(e => e.Direction == AudioEndpointDirection.Playback);
var capture = endpoints?.FirstOrDefault(e => e.Direction == AudioEndpointDirection.Capture);
var noPlayback = noAudio ?? "this machine has no active playback endpoint";
var noCapture = noAudio ?? "this machine has no active capture endpoint";

if (playback is null)
{
    Skip("playback takes whole frames and reports backpressure", noPlayback);
    Skip("capture and playback refuse the other direction", noPlayback);
    Skip("loopback captures a playback endpoint", noPlayback);
}
else
{
    Test("playback takes whole frames and reports backpressure", () =>
    {
        using var output = SystemAudio.Playback(playback.Id);
        Check(Throws<AudioPacketException>(() => output.Write(ReadOnlySpan<byte>.Empty), "empty").Code == "invalid", "the code of empty");
        Check(Throws<AudioPacketException>(() => output.Write(new byte[3]), "a partial frame").Code == "invalid", "the code of a partial frame");
        Check(Throws<AudioPacketException>(() => output.Write(new byte[6]), "a frame and a half").Code == "invalid", "the code of a frame and a half");
        output.Write(new byte[4 * 480]); // 10 ms of silence
        // A packet is at most 20 ms (3 840 bytes): larger is refused as too large.
        Check(Throws<AudioPacketException>(() => output.Write(new byte[SystemAudioPlayback.MaxPacketBytes + SystemAudioPlayback.BytesPerFrame]), "more than a packet").Code == "too_large", "the code of more than a packet");
        // The buffer holds 250 ms (48 000 bytes): what does not fit is refused, not queued.
        Code<AudioException>("busy", () =>
        {
            for (var i = 0; i < 20; i++) output.Write(new byte[SystemAudioPlayback.MaxPacketBytes]);
        }, "more than the buffer");
        output.Dispose();
        output.Dispose();
        Code<AudioException>("canceled", () => output.Write(new byte[4]), "a write after dispose");
    });

    Test("capture and playback refuse the other direction", () =>
    {
        Code<AudioArgumentException>("invalid", () => SystemAudio.Capture(playback.Id).Dispose(), "microphone capture of a playback endpoint");
        if (capture is not null)
        {
            Code<AudioArgumentException>("invalid", () => SystemAudio.Playback(capture.Id).Dispose(), "playback to a capture endpoint");
            Code<AudioArgumentException>("invalid", () => SystemAudio.Capture(capture.Id, systemAudio: true).Dispose(), "loopback of a capture endpoint");
        }
    });

    Test("loopback captures a playback endpoint", () =>
    {
        using var loopback = SystemAudio.Capture(playback.Id, systemAudio: true);
        CheckFormat(loopback.Format);
        loopback.Start();
        loopback.Dispose();
        loopback.Dispose();
        Code<AudioException>("canceled", () => loopback.Start(), "a start after dispose");
    });
}

if (capture is null)
{
    Skip("capture reports its format and stops", noCapture);
}
else
{
    Test("capture reports its format and stops", () =>
    {
        using var input = SystemAudio.Capture(capture.Id);
        CheckFormat(input.Format);
        Exception? stopped = null;
        var done = new ManualResetEventSlim();
        input.Stopped += e => { stopped = e; done.Set(); };
        input.Samples += samples => Check(samples.Length % input.Format.BlockAlign == 0, "a partial frame was captured");
        input.Start();
        Thread.Sleep(100);
        input.Dispose();
        Check(done.Wait(TimeSpan.FromSeconds(5)), "capture did not report that it stopped");
        Check(stopped is null, $"capture stopped with {stopped}");
    });
}

Console.WriteLine($"{passed} passed, {skipped} skipped, {failed.Count} failed");
foreach (var name in failed) Console.WriteLine($"  {name}");
return failed.Count == 0 ? 0 : 1;

static void CheckFormat(WaveFormat format)
{
    Check(format.SampleRate > 0 && format.Channels > 0 && format.BitsPerSample > 0, $"format {format}");
    Check(format.BlockAlign == format.Channels * format.BitsPerSample / 8, $"block align {format.BlockAlign} for {format}");
}
