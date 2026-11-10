// Ext.Audio.Windows on WASAPI: argument checks, endpoint listing, and, on the endpoints this
// machine has, capture and playback format handling and playback backpressure. A hosted runner
// usually has no audio endpoint (and may have no audio service), so what needs one says why and is
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

Test("an endpoint id is required", () =>
{
    foreach (var id in new[] { "", " ", "\t" })
    {
        Throws<ArgumentException>(() => SystemAudio.Capture(id).Dispose(), $"capture {id.Length}");
        Throws<ArgumentException>(() => SystemAudio.Capture(id, systemAudio: true).Dispose(), $"loopback {id.Length}");
        Throws<ArgumentException>(() => SystemAudio.Playback(id).Dispose(), $"playback {id.Length}");
    }
    Throws<ArgumentNullException>(() => SystemAudio.Capture(null!).Dispose(), "capture null");
    Throws<ArgumentNullException>(() => SystemAudio.Playback(null!).Dispose(), "playback null");
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

IReadOnlyList<AudioEndpoint>? endpoints = null;
string? noAudio = null;
try
{
    endpoints = SystemAudio.Endpoints();
}
catch (COMException e)
{
    // No audio service (Audiosrv) or no MMDevice API in this session. The library has no capability
    // report or error code of its own, so this is NAudio's COMException (docs/adr-architecture.md,
    // rules 2 and 3).
    noAudio = $"no audio endpoints can be listed (0x{e.HResult:x8} {e.Message.Trim()})";
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
        Throws<Exception>(() => SystemAudio.Playback(unknown).Dispose(), "playback");
        Throws<Exception>(() => SystemAudio.Capture(unknown).Dispose(), "capture");
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
        Throws<ArgumentException>(() => output.Write(ReadOnlySpan<byte>.Empty), "empty");
        Throws<ArgumentException>(() => output.Write(new byte[3]), "a partial frame");
        Throws<ArgumentException>(() => output.Write(new byte[6]), "a frame and a half");
        output.Write(new byte[4 * 480]); // 10 ms of silence
        // The buffer holds 250 ms (48 000 bytes): more than that at once is refused, not queued.
        Throws<IOException>(() => output.Write(new byte[48_000 + 4]), "more than the buffer");
        output.Dispose();
        output.Dispose();
        Throws<ObjectDisposedException>(() => output.Write(new byte[4]), "a write after dispose");
    });

    Test("capture and playback refuse the other direction", () =>
    {
        Throws<ArgumentException>(() => SystemAudio.Capture(playback.Id).Dispose(), "microphone capture of a playback endpoint");
        if (capture is not null)
        {
            Throws<ArgumentException>(() => SystemAudio.Playback(capture.Id).Dispose(), "playback to a capture endpoint");
            Throws<ArgumentException>(() => SystemAudio.Capture(capture.Id, systemAudio: true).Dispose(), "loopback of a capture endpoint");
        }
    });

    Test("loopback captures a playback endpoint", () =>
    {
        using var loopback = SystemAudio.Capture(playback.Id, systemAudio: true);
        CheckFormat(loopback.Format);
        loopback.Start();
        loopback.Dispose();
        loopback.Dispose();
        Throws<ObjectDisposedException>(() => loopback.Start(), "a start after dispose");
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
