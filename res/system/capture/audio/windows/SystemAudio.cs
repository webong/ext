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
/// Local WASAPI endpoint discovery, capture, and playback. Callers own endpoint
/// selection, authorization, encoding, transport, and session policy.
/// </summary>
public static class SystemAudio
{
    public static IReadOnlyList<AudioEndpoint> Endpoints()
    {
        using var enumerator = new MMDeviceEnumerator();
        var result = new List<AudioEndpoint>();
        foreach (var flow in new[] { DataFlow.Render, DataFlow.Capture })
        {
            foreach (var device in enumerator.EnumerateAudioEndPoints(flow, DeviceState.Active))
            {
                using (device)
                {
                    result.Add(new AudioEndpoint(
                        device.ID,
                        device.FriendlyName,
                        flow == DataFlow.Render ? AudioEndpointDirection.Playback : AudioEndpointDirection.Capture));
                }
            }
        }
        return result;
    }

    public static SystemAudioCapture Capture(string endpointId, bool systemAudio = false) =>
        new(endpointId, systemAudio);

    public static SystemAudioPlayback Playback(string endpointId) => new(endpointId);
}

/// <summary>
/// Captures native WASAPI buffers. Samples use <see cref="Format"/> and the
/// callback memory is valid only until the event handler returns.
/// </summary>
public sealed class SystemAudioCapture : IDisposable
{
    private readonly MMDevice device;
    private readonly WasapiCapture capture;
    private bool disposed;

    public WaveFormat Format => capture.WaveFormat;
    public event Action<ReadOnlyMemory<byte>>? Samples;
    public event Action<Exception?>? Stopped;

    internal SystemAudioCapture(string endpointId, bool systemAudio)
    {
        ArgumentException.ThrowIfNullOrWhiteSpace(endpointId);
        using var enumerator = new MMDeviceEnumerator();
        device = enumerator.GetDevice(endpointId);
        try
        {
            var expected = systemAudio ? DataFlow.Render : DataFlow.Capture;
            if (device.DataFlow != expected)
                throw new ArgumentException("Endpoint direction does not match capture mode", nameof(endpointId));
            capture = systemAudio ? new WasapiLoopbackCapture(device) : new WasapiCapture(device);
            capture.DataAvailable += (_, value) => Samples?.Invoke(value.Buffer.AsMemory(0, value.BytesRecorded));
            capture.RecordingStopped += (_, value) => Stopped?.Invoke(value.Exception);
        }
        catch
        {
            device.Dispose();
            throw;
        }
    }

    public void Start()
    {
        ObjectDisposedException.ThrowIf(disposed, this);
        capture.StartRecording();
    }

    public void Dispose()
    {
        if (disposed) return;
        disposed = true;
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

    private readonly MMDevice device;
    private readonly WasapiOut output;
    private readonly BufferedWaveProvider buffer;
    private bool disposed;

    internal SystemAudioPlayback(string endpointId)
    {
        ArgumentException.ThrowIfNullOrWhiteSpace(endpointId);
        using var enumerator = new MMDeviceEnumerator();
        device = enumerator.GetDevice(endpointId);
        WasapiOut? created = null;
        try
        {
            if (device.DataFlow != DataFlow.Render)
                throw new ArgumentException("Playback requires a render endpoint", nameof(endpointId));
            var format = new WaveFormat(SampleRate, BitsPerSample, Channels);
            buffer = new BufferedWaveProvider(format)
            {
                BufferDuration = TimeSpan.FromMilliseconds(250),
                DiscardOnBufferOverflow = false,
            };
            created = new WasapiOut(device, AudioClientShareMode.Shared, true, 60);
            created.Init(buffer);
            created.Play();
            output = created;
        }
        catch
        {
            created?.Dispose();
            device.Dispose();
            throw;
        }
    }

    public void Write(ReadOnlySpan<byte> bytes)
    {
        ObjectDisposedException.ThrowIf(disposed, this);
        if (bytes.IsEmpty || bytes.Length % buffer.WaveFormat.BlockAlign != 0)
            throw new ArgumentException("Playback needs complete PCM frames", nameof(bytes));
        if (bytes.Length > buffer.BufferLength - buffer.BufferedBytes)
            throw new IOException("Playback buffer is full");
        var copy = bytes.ToArray();
        buffer.AddSamples(copy, 0, copy.Length);
    }

    public void Dispose()
    {
        if (disposed) return;
        disposed = true;
        try { output.Stop(); }
        finally
        {
            output.Dispose();
            buffer.ClearBuffer();
            device.Dispose();
        }
    }
}
