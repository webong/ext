using System.Diagnostics;
using System.Runtime.InteropServices;
using NAudio.CoreAudioApi;
using NAudio.Wave;

namespace Webong.Ext.System.Windows;

public enum AudioEndpointDirection
{
    Capture,
    Playback,
}

public sealed record AudioEndpoint(string Id, string Name, AudioEndpointDirection Direction);

/// <summary>
/// An argument the audio library refuses, with the stable code <c>invalid</c> of
/// res/system/testdata/codes.json: an empty endpoint id, or an endpoint of the other direction.
/// With <see cref="AudioException"/> it is the library's one coded error family: every error the
/// library raises is one of the two and carries a <c>Code</c>.
/// </summary>
public class AudioArgumentException : ArgumentException
{
    /// <summary>The stable error code of res/system/testdata/codes.json.</summary>
    public string Code { get; }

    public AudioArgumentException(string code, string message, string? paramName = null)
        : base(message, paramName)
    {
        Code = code;
    }
}

/// <summary>
/// A playback packet that breaks the packet rule of res/system/testdata/audio/v1. It is an
/// <see cref="ArgumentException"/>, as Write threw before, and carries the rule's stable code.
/// </summary>
public sealed class AudioPacketException : AudioArgumentException
{
    /// <summary>The code is "invalid" (no data, or not whole frames) or "too_large" (over the packet ceiling).</summary>
    public AudioPacketException(string code, string message, string? paramName = null)
        : base(code, message, paramName)
    {
    }
}

/// <summary>
/// A failure of the audio service, an endpoint or a stream, with the stable code of
/// res/system/testdata/codes.json that Swift <c>SystemAudioPlaybackError.code</c> uses for the same
/// failure: <c>busy</c> (the playback buffer is full), <c>canceled</c> (the sink or capture is
/// closed, or the caller's token was canceled), <c>timeout</c> (a listing ran past its timeout),
/// <c>io</c> (a WASAPI call failed), <c>unavailable</c> (the endpoint is unknown or not active) or
/// <c>unsupported</c> (this session has no audio service; see <see cref="SystemAudio.Status"/>).
/// </summary>
public sealed class AudioException : Exception
{
    /// <summary>The stable error code of res/system/testdata/codes.json.</summary>
    public string Code { get; }

    public AudioException(string code, string message, Exception? inner = null)
        : base(message, inner)
    {
        Code = code;
    }
}

/// <summary>
/// Whether this session can list and open audio endpoints, in the collection-status vocabulary every
/// res/system package shares (testdata/capability/v1, docs/adr-architecture.md rule 2), as Go's
/// <c>audio.Status</c>. <c>complete</c>: the audio service answered; it may have no endpoint, which
/// is an empty <see cref="SystemAudio.Endpoints"/>. <c>unsupported</c> with <c>unsupported</c>:
/// there is no audio service. <c>unavailable</c>: the service is there but failed to answer. The
/// constructor refuses a state and reason the capability fixture does not allow, with code <c>invalid</c>.
/// </summary>
public sealed record AudioStatus
{
    public string State { get; }
    public string? Reason { get; }

    public AudioStatus(string state, string? reason = null)
    {
        if (!AudioCapability.Allowed(state, reason))
            throw new AudioArgumentException("invalid", "audio status does not take that reason", nameof(reason));
        State = state;
        Reason = reason;
    }
}

/// <summary>
/// The time bound and cancellation of one call (docs/adr-architecture.md rule 4), as Go's context
/// deadline: it stops at its deadline and when the caller's token is canceled. The deadline is the
/// last clock tick inside the timeout, so the clock's resolution never lets a call run past it.
/// </summary>
internal readonly struct AudioBound
{
    /// <summary>The default timeout, as Go's observers: five seconds.</summary>
    internal static readonly TimeSpan DefaultTimeout = TimeSpan.FromSeconds(5);
    /// <summary>The longest timeout or capture a caller may select: one hour.</summary>
    internal static readonly TimeSpan MaximumTimeout = TimeSpan.FromHours(1);

    private readonly long deadline;
    private readonly CancellationToken token;

    internal AudioBound(TimeSpan timeout, CancellationToken token)
        : this(Stopwatch.GetTimestamp() + Math.Max((long)Math.Ceiling(timeout.TotalSeconds * Stopwatch.Frequency), 1) - 1, token)
    {
    }

    /// <summary>A bound ending at a Stopwatch timestamp, so the tests can make one that has passed.</summary>
    internal AudioBound(long deadline, CancellationToken token)
    {
        this.deadline = deadline;
        this.token = token;
    }

    /// <summary>A timeout is a bound when it is above zero and at most an hour.</summary>
    internal static bool Valid(TimeSpan timeout) => timeout > TimeSpan.Zero && timeout <= MaximumTimeout;

    /// <summary>The checked timeout: the default for null, else <c>invalid</c> unless it is a bound.</summary>
    internal static TimeSpan Timeout(TimeSpan? timeout, string paramName)
    {
        var value = timeout ?? DefaultTimeout;
        if (!Valid(value))
            throw new AudioArgumentException("invalid", "a timeout is above zero and at most an hour", paramName);
        return value;
    }

    /// <summary><c>canceled</c> when the token is (judged first, as Go reports the parent context), else <c>timeout</c> past the deadline, else null.</summary>
    internal string? Stop => token.IsCancellationRequested ? "canceled"
        : Stopwatch.GetTimestamp() >= deadline ? "timeout" : null;

    /// <summary>Throws the <see cref="AudioException"/> of a bound that has stopped.</summary>
    internal void Check()
    {
        switch (Stop)
        {
            case "canceled":
                throw new AudioException("canceled", "the audio call was canceled");
            case "timeout":
                throw new AudioException("timeout", "the audio call ran past its timeout");
        }
    }
}

/// <summary>
/// The bounds of one capture (docs/adr-architecture.md rule 4), as Go's
/// <c>media.SampleOptions</c>: it runs for at most <see cref="MaxDuration"/> and delivers at most
/// <see cref="MaxBytes"/>. The duration is above zero and at most an hour; the byte limit is at
/// least one and at most 1 GiB. Others are refused with code <c>invalid</c>.
/// </summary>
public sealed record AudioCaptureLimits
{
    /// <summary>Five minutes and 64 MiB: the bounds of a capture that selects none.</summary>
    public static readonly AudioCaptureLimits Default = new(TimeSpan.FromMinutes(5), 64L * 1024 * 1024);
    /// <summary>The largest byte limit: 1 GiB, as Go's <c>MaxTotalBytes</c> ceiling.</summary>
    public const long MaximumBytes = 1L << 30;

    public TimeSpan MaxDuration { get; }
    public long MaxBytes { get; }

    public AudioCaptureLimits(TimeSpan maxDuration, long maxBytes)
    {
        if (!AudioBound.Valid(maxDuration))
            throw new AudioArgumentException("invalid", "a capture lasts above zero and at most an hour", nameof(maxDuration));
        if (maxBytes < 1 || maxBytes > MaximumBytes)
            throw new AudioArgumentException("invalid", "a capture byte limit is at least one byte and at most 1 GiB", nameof(maxBytes));
        MaxDuration = maxDuration;
        MaxBytes = maxBytes;
    }
}

/// <summary>
/// Why a capture stopped, as Go's <c>media.SampleManifest.StopReason</c>, and how many bytes it
/// delivered. <see cref="Reason"/> is <c>duration</c> (it ran for its maximum duration),
/// <c>byte_limit</c> (it delivered its byte limit), <c>canceled</c> (the token given to Start was
/// canceled), <c>closed</c> (it was disposed) or <c>capture_failed</c> (WASAPI stopped it, with
/// <see cref="Error"/> the exception NAudio reported). The first two are complete captures.
/// </summary>
public sealed record AudioCaptureStop(string Reason, long Bytes, Exception? Error);

/// <summary>
/// What a capture may still deliver, judged on each WASAPI buffer: the byte limit is reached on a
/// frame boundary, and the first reason to stop is the one reported.
/// </summary>
internal sealed class AudioCaptureBudget
{
    private readonly object gate = new();
    private readonly long maxBytes;
    private long delivered;
    private string? reason;

    internal AudioCaptureBudget(long maxBytes)
    {
        this.maxBytes = maxBytes;
    }

    internal long Delivered
    {
        get { lock (gate) return delivered; }
    }

    /// <summary>The reason given to the first <see cref="Stop"/>, or null.</summary>
    internal string? Reason
    {
        get { lock (gate) return reason; }
    }

    /// <summary>
    /// How many of <paramref name="recorded"/> bytes to deliver: all of them while they fit, else
    /// the whole frames that fit, after which the budget has stopped with <c>byte_limit</c>. Nothing
    /// once it has stopped for any reason.
    /// </summary>
    internal int Take(int recorded, int blockAlign)
    {
        lock (gate)
        {
            if (reason is not null || recorded <= 0) return 0;
            var room = maxBytes - delivered;
            var take = recorded;
            if (recorded >= room)
            {
                take = (int)(room - room % Math.Max(blockAlign, 1));
                reason = "byte_limit";
            }
            delivered += take;
            return take;
        }
    }

    /// <summary>Records why the capture stops; true for the first reason only.</summary>
    internal bool Stop(string why)
    {
        lock (gate)
        {
            if (reason is not null) return false;
            reason = why;
            return true;
        }
    }
}

/// <summary>The state and reason pairs of testdata/capability/v1.</summary>
internal static class AudioCapability
{
    internal static bool Allowed(string? state, string? reason) => state switch
    {
        "complete" or "partial" => reason is null,
        "permission-denied" => reason is null or "permission",
        "unsupported" => reason is "unsupported" or "tool_missing",
        "unavailable" => reason is null or "permission" or "no_device" or "tool_missing",
        _ => false,
    };
}

/// <summary>
/// Local WASAPI endpoint discovery, capture, and playback. Callers own endpoint
/// selection, authorization, encoding, transport, and session policy.
/// </summary>
public static class SystemAudio
{
    // HRESULTs that say what is missing rather than that a call failed.
    internal const int ENotFound = unchecked((int)0x80070490);
    internal const int DeviceInvalidated = unchecked((int)0x88890004); // AUDCLNT_E_DEVICE_INVALIDATED
    internal const int ServiceNotRunning = unchecked((int)0x88890010); // AUDCLNT_E_SERVICE_NOT_RUNNING

    /// <summary>
    /// Whether this session has an audio service (<see cref="AudioStatus"/>). It lists the
    /// endpoints once and opens none; no endpoint at all is still <c>complete</c>.
    /// </summary>
    public static AudioStatus Status() => Status(() => Endpoints());

    internal static AudioStatus Status(Action probe)
    {
        try
        {
            probe();
            return new AudioStatus("complete");
        }
        catch (AudioException e) when (e.Code == "unsupported")
        {
            return new AudioStatus("unsupported", "unsupported");
        }
        catch (AudioException)
        {
            return new AudioStatus("unavailable");
        }
    }

    /// <summary>
    /// The active endpoints; an empty list when there are none. Throws <see cref="AudioException"/>
    /// <c>unsupported</c> when this session has no audio service, or <c>io</c>. The listing is bounded
    /// by <paramref name="timeout"/> (five seconds when null, as Go; above zero and at most an hour,
    /// else <see cref="AudioArgumentException"/> <c>invalid</c>) and by
    /// <paramref name="cancellationToken"/>: it throws <c>timeout</c> or <c>canceled</c>. The bound is
    /// judged between WASAPI calls; one call that blocks is not interrupted.
    /// </summary>
    public static IReadOnlyList<AudioEndpoint> Endpoints(TimeSpan? timeout = null, CancellationToken cancellationToken = default) =>
        Endpoints(new AudioBound(AudioBound.Timeout(timeout, nameof(timeout)), cancellationToken));

    internal static IReadOnlyList<AudioEndpoint> Endpoints(AudioBound bound)
    {
        bound.Check();
        using var enumerator = Enumerator();
        var result = new List<AudioEndpoint>();
        try
        {
            foreach (var flow in new[] { DataFlow.Render, DataFlow.Capture })
            {
                bound.Check();
                foreach (var device in enumerator.EnumerateAudioEndPoints(flow, DeviceState.Active))
                {
                    using (device)
                    {
                        bound.Check();
                        result.Add(new AudioEndpoint(
                            device.ID,
                            device.FriendlyName,
                            flow == DataFlow.Render ? AudioEndpointDirection.Playback : AudioEndpointDirection.Capture));
                    }
                }
            }
        }
        catch (COMException e)
        {
            throw Native(e, "cannot list audio endpoints");
        }
        bound.Check();
        return result;
    }

    /// <summary>
    /// A capture of one endpoint, bounded by <paramref name="limits"/>
    /// (<see cref="AudioCaptureLimits.Default"/> when null): see <see cref="SystemAudioCapture"/>.
    /// </summary>
    public static SystemAudioCapture Capture(string endpointId, bool systemAudio = false, AudioCaptureLimits? limits = null) =>
        new(endpointId, systemAudio, limits ?? AudioCaptureLimits.Default);

    public static SystemAudioPlayback Playback(string endpointId) => new(endpointId);

    /// <summary>The device enumerator; failing to make one means there is no audio service.</summary>
    internal static MMDeviceEnumerator Enumerator()
    {
        try
        {
            return new MMDeviceEnumerator();
        }
        catch (Exception e) when (e is COMException or PlatformNotSupportedException or TypeLoadException)
        {
            throw new AudioException("unsupported", "this session has no audio service", e);
        }
    }

    /// <summary>
    /// The active endpoint with this id, of the expected flow. An empty id or the other flow is
    /// invalid; an id with no endpoint, or one that is not active, is unavailable.
    /// </summary>
    internal static MMDevice Device(string? endpointId, DataFlow expected, string mismatch)
    {
        CheckEndpointId(endpointId);
        using var enumerator = Enumerator();
        MMDevice device;
        try
        {
            device = enumerator.GetDevice(endpointId);
        }
        catch (COMException e)
        {
            throw Native(e, "cannot open the audio endpoint");
        }
        try
        {
            CheckState(device.State);
            CheckDirection(device.DataFlow, expected, mismatch);
            return device;
        }
        catch (COMException e)
        {
            device.Dispose();
            throw Native(e, "cannot open the audio endpoint");
        }
        catch
        {
            device.Dispose();
            throw;
        }
    }

    internal static void CheckEndpointId(string? endpointId)
    {
        if (string.IsNullOrWhiteSpace(endpointId))
            throw new AudioArgumentException("invalid", "an endpoint id is required", nameof(endpointId));
    }

    internal static void CheckState(DeviceState state)
    {
        if (state != DeviceState.Active)
            throw new AudioException("unavailable", "the audio endpoint is not active");
    }

    internal static void CheckDirection(DataFlow actual, DataFlow expected, string message)
    {
        if (actual != expected)
            throw new AudioArgumentException("invalid", message, "endpointId");
    }

    /// <summary>
    /// A failed WASAPI or MMDevice call: unavailable when the endpoint is unknown or gone,
    /// unsupported when the audio service is not running, else io.
    /// </summary>
    internal static AudioException Native(COMException e, string message) => e.HResult switch
    {
        ENotFound or DeviceInvalidated => new AudioException("unavailable", "the audio endpoint is unavailable", e),
        ServiceNotRunning => new AudioException("unsupported", "this session has no audio service", e),
        _ => new AudioException("io", message, e),
    };
}

/// <summary>
/// Captures native WASAPI buffers. Samples use <see cref="Format"/> and the
/// callback memory is valid only until the event handler returns.
/// </summary>
/// <remarks>
/// A capture is bounded (docs/adr-architecture.md rule 4) by its <see cref="Limits"/>: it stops by
/// itself after <see cref="AudioCaptureLimits.MaxDuration"/>, or once it has delivered
/// <see cref="AudioCaptureLimits.MaxBytes"/> (the last buffer is cut to the whole frames that fit),
/// and when the token given to <see cref="Start"/> is canceled. <see cref="Stopped"/> then says why
/// (<see cref="AudioCaptureStop"/>). It is raised once, on the capture thread.
/// </remarks>
public sealed class SystemAudioCapture : IDisposable
{
    private readonly MMDevice device;
    private readonly WasapiCapture capture;
    private readonly AudioCaptureBudget budget;
    private readonly object gate = new();
    private bool started;
    private bool disposed;
    private Timer? timer;
    private CancellationTokenRegistration registration;

    public WaveFormat Format => capture.WaveFormat;
    public AudioCaptureLimits Limits { get; }
    public event Action<ReadOnlyMemory<byte>>? Samples;
    public event Action<AudioCaptureStop>? Stopped;

    internal SystemAudioCapture(string endpointId, bool systemAudio, AudioCaptureLimits limits)
    {
        Limits = limits;
        budget = new AudioCaptureBudget(limits.MaxBytes);
        device = SystemAudio.Device(endpointId, systemAudio ? DataFlow.Render : DataFlow.Capture,
            "Endpoint direction does not match capture mode");
        try
        {
            capture = systemAudio ? new WasapiLoopbackCapture(device) : new WasapiCapture(device);
            capture.DataAvailable += (_, value) => Deliver(value.Buffer, value.BytesRecorded);
            capture.RecordingStopped += (_, value) => Finished(value.Exception);
        }
        catch (COMException e)
        {
            device.Dispose();
            throw SystemAudio.Native(e, "cannot open audio capture");
        }
        catch
        {
            device.Dispose();
            throw;
        }
    }

    /// <summary>
    /// Starts capture, which stops by itself at its <see cref="Limits"/> or when
    /// <paramref name="cancellationToken"/> is canceled. <see cref="AudioException"/>
    /// <c>canceled</c> after Dispose or with a token already canceled, <c>busy</c> when it was
    /// started before (a capture runs once), or <c>io</c>.
    /// </summary>
    public void Start(CancellationToken cancellationToken = default)
    {
        lock (gate)
        {
            if (disposed) throw new AudioException("canceled", "audio capture is closed");
            if (cancellationToken.IsCancellationRequested) throw new AudioException("canceled", "audio capture was canceled");
            if (started) throw new AudioException("busy", "audio capture runs once");
            started = true;
            try
            {
                capture.StartRecording();
            }
            catch (COMException e)
            {
                throw SystemAudio.Native(e, "cannot start audio capture");
            }
            timer = new Timer(_ => StopFor("duration"), null, Limits.MaxDuration, Timeout.InfiniteTimeSpan);
            registration = cancellationToken.Register(() => StopFor("canceled"));
        }
    }

    /// <summary>Delivers what the budget allows of one WASAPI buffer, and stops at the byte limit.</summary>
    private void Deliver(byte[] buffer, int recorded)
    {
        var take = budget.Take(recorded, capture.WaveFormat.BlockAlign);
        if (take > 0) Samples?.Invoke(buffer.AsMemory(0, take));
        if (budget.Reason == "byte_limit") StopRecording();
    }

    /// <summary>Stops the capture for this reason, unless it already stops for another.</summary>
    private void StopFor(string reason)
    {
        if (budget.Stop(reason)) StopRecording();
    }

    private void StopRecording()
    {
        lock (gate)
        {
            if (disposed) return;
            try { capture.StopRecording(); }
            catch (Exception e) when (e is COMException or InvalidOperationException) { }
        }
    }

    private void Finished(Exception? error)
    {
        budget.Stop(error is null ? "closed" : "capture_failed");
        lock (gate)
        {
            timer?.Dispose();
            timer = null;
        }
        registration.Dispose();
        Stopped?.Invoke(new AudioCaptureStop(budget.Reason!, budget.Delivered, error));
    }

    public void Dispose()
    {
        budget.Stop("closed");
        lock (gate)
        {
            if (disposed) return;
            disposed = true;
            timer?.Dispose();
            timer = null;
        }
        registration.Dispose();
        try { capture.StopRecording(); }
        finally
        {
            capture.Dispose();
            device.Dispose();
        }
    }
}

/// <summary>
/// Bounded PCM16 little-endian stereo 48 kHz playback. Write reports
/// backpressure instead of silently accumulating delayed audio.
/// </summary>
public sealed class SystemAudioPlayback : IDisposable
{
    public const int SampleRate = 48_000;
    public const int Channels = 2;
    public const int BitsPerSample = 16;
    /// <summary>One frame: two channels of 16-bit samples.</summary>
    public const int BytesPerFrame = Channels * BitsPerSample / 8;
    /// <summary>The largest packet Write takes: twenty milliseconds.</summary>
    public const int MaxPacketBytes = 3_840;

    private readonly MMDevice? device;
    private readonly WasapiOut? output;
    private readonly BufferedWaveProvider buffer;
    private readonly object gate = new();
    private bool disposed;

    internal SystemAudioPlayback(string endpointId)
    {
        device = SystemAudio.Device(endpointId, DataFlow.Render, "Playback requires a render endpoint");
        WasapiOut? created = null;
        try
        {
            buffer = Buffer();
            created = new WasapiOut(device, AudioClientShareMode.Shared, true, 60);
            created.Init(buffer);
            created.Play();
            output = created;
        }
        catch (COMException e)
        {
            created?.Dispose();
            device.Dispose();
            throw SystemAudio.Native(e, "cannot open audio playback");
        }
        catch
        {
            created?.Dispose();
            device.Dispose();
            throw;
        }
    }

    /// <summary>A sink with no device: the buffer is never drained, so Write's own checks can be tested.</summary>
    internal SystemAudioPlayback(BufferedWaveProvider buffer)
    {
        this.buffer = buffer;
    }

    /// <summary>The bounded buffer every sink plays from: 250 ms that refuses to overflow.</summary>
    internal static BufferedWaveProvider Buffer() => new(new WaveFormat(SampleRate, BitsPerSample, Channels))
    {
        BufferDuration = TimeSpan.FromMilliseconds(250),
        DiscardOnBufferOverflow = false,
    };

    /// <summary>
    /// The packet rule of testdata/audio/v1: a packet is at least one frame and whole frames ("invalid"),
    /// and at most maxBytes ("too_large"); the frames are judged first. Null when it is accepted.
    /// </summary>
    internal static string? PacketCode(int length, int maxBytes)
    {
        if (length <= 0 || length % BytesPerFrame != 0) return "invalid";
        return length > maxBytes ? "too_large" : null;
    }

    /// <summary>Throws the <see cref="AudioPacketException"/> of a packet that breaks the rule.</summary>
    internal static void CheckPacket(int length, int maxBytes, string? paramName = null)
    {
        switch (PacketCode(length, maxBytes))
        {
            case "invalid":
                throw new AudioPacketException("invalid", "Playback needs complete PCM frames", paramName);
            case "too_large":
                throw new AudioPacketException("too_large", "Playback packet is over the packet ceiling", paramName);
        }
    }

    /// <summary>
    /// Queues one packet. <see cref="AudioPacketException"/> for a packet that breaks the rule;
    /// <see cref="AudioException"/> <c>busy</c> when the buffer has no room for it, and <c>canceled</c>
    /// after Dispose.
    /// </summary>
    public void Write(ReadOnlySpan<byte> bytes)
    {
        lock (gate)
        {
            if (disposed) throw new AudioException("canceled", "audio playback is closed");
            CheckPacket(bytes.Length, MaxPacketBytes, nameof(bytes));
            if (bytes.Length > buffer.BufferLength - buffer.BufferedBytes)
                throw new AudioException("busy", "the playback buffer is full");
            var copy = bytes.ToArray();
            buffer.AddSamples(copy, 0, copy.Length);
        }
    }

    public void Dispose()
    {
        lock (gate)
        {
            if (disposed) return;
            disposed = true;
        }
        try { output?.Stop(); }
        finally
        {
            output?.Dispose();
            buffer.ClearBuffer();
            device?.Dispose();
        }
    }
}
