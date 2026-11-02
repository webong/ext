package io.github.webong.ext.system.android;

import android.hardware.display.DisplayManager;
import android.hardware.display.VirtualDisplay;
import android.media.AudioAttributes;
import android.media.AudioFormat;
import android.media.AudioPlaybackCaptureConfiguration;
import android.media.AudioRecord;
import android.media.MediaRecorder;
import android.media.projection.MediaProjection;
import android.os.Build;
import android.os.Handler;
import android.os.Looper;
import android.view.Surface;
import java.util.ArrayList;
import java.util.List;

/**
 * Owns a fresh MediaProjection granted by Android. The caller starts the
 * required foreground service, supplies an encoder or ImageReader surface,
 * reads audio on worker threads, and owns encoding, transport, and policy.
 */
public final class SystemMediaProjection implements AutoCloseable {
    private final MediaProjection projection;
    private final Runnable onStopped;
    private final List<AudioRecord> audio = new ArrayList<>();
    private final MediaProjection.Callback callback = new MediaProjection.Callback() {
        @Override public void onStop() { finish(false); }
    };
    private VirtualDisplay display;
    private boolean started;
    private boolean closed;

    public SystemMediaProjection(MediaProjection projection, Runnable onStopped) {
        checkMainThread();
        if (projection == null || onStopped == null) {
            throw new IllegalArgumentException("projection and stop callback are required");
        }
        this.projection = projection;
        this.onStopped = onStopped;
        projection.registerCallback(callback, new Handler(Looper.getMainLooper()));
    }

    public void start(Surface surface, int width, int height, int densityDpi) {
        checkMainThread();
        if (closed || started) throw new IllegalStateException("projection is closed or already used");
        validateDisplay(surface, width, height, densityDpi);
        started = true; // Android projection tokens are single-use, including failed starts.
        try {
            display = projection.createVirtualDisplay(
                    "Ext system capture", width, height, densityDpi,
                    DisplayManager.VIRTUAL_DISPLAY_FLAG_AUTO_MIRROR, surface, null, null);
            if (display == null) throw new IllegalStateException("virtual display is unavailable");
        } catch (RuntimeException exception) {
            close();
            throw exception;
        }
    }

    public void resize(Surface surface, int width, int height, int densityDpi) {
        checkMainThread();
        if (closed || display == null) throw new IllegalStateException("projection is not running");
        validateDisplay(surface, width, height, densityDpi);
        display.resize(width, height, densityDpi);
        display.setSurface(surface);
    }

    /**
     * Starts PCM16 mono 48 kHz capture. Microphone capture requires the caller
     * to hold RECORD_AUDIO and the appropriate foreground-service type. System
     * playback capture requires Android 10 and capturable source applications.
     */
    public AudioRecord openAudio(boolean microphone) {
        checkMainThread();
        if (closed || !started) throw new IllegalStateException("projection is not running");
        if (audio.size() >= 2) throw new IllegalStateException("at most two audio sources are supported");

        AudioFormat format = new AudioFormat.Builder()
                .setEncoding(AudioFormat.ENCODING_PCM_16BIT)
                .setSampleRate(48_000)
                .setChannelMask(AudioFormat.CHANNEL_IN_MONO)
                .build();
        int minimum = AudioRecord.getMinBufferSize(
                48_000, AudioFormat.CHANNEL_IN_MONO, AudioFormat.ENCODING_PCM_16BIT);
        if (minimum <= 0 || minimum > 192_000) {
            throw new IllegalStateException("capture format or bounded buffer unavailable");
        }
        AudioRecord.Builder builder = new AudioRecord.Builder()
                .setAudioFormat(format)
                .setBufferSizeInBytes(Math.max(minimum, 9_600));
        if (microphone) {
            builder.setAudioSource(MediaRecorder.AudioSource.MIC);
        } else {
            configurePlaybackCapture(builder);
        }
        AudioRecord recorder = builder.build();
        try {
            if (recorder.getState() != AudioRecord.STATE_INITIALIZED) {
                throw new IllegalStateException("native audio capture initialization failed");
            }
            recorder.startRecording();
            audio.add(recorder);
            return recorder;
        } catch (RuntimeException exception) {
            recorder.release();
            throw exception;
        }
    }

    @Override public void close() { finish(true); }

    private void finish(boolean stopProjection) {
        checkMainThread();
        if (closed) return;
        closed = true;
        for (AudioRecord recorder : audio) {
            try { recorder.stop(); } catch (RuntimeException ignored) { }
            recorder.release();
        }
        audio.clear();
        if (display != null) display.release();
        display = null;
        projection.unregisterCallback(callback);
        if (stopProjection) projection.stop();
        onStopped.run();
    }

    private void configurePlaybackCapture(AudioRecord.Builder builder) {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.Q) {
            throw new IllegalStateException("system playback capture requires Android 10");
        }
        Api29.configurePlaybackCapture(builder, projection);
    }

    /** Keeps API 29 classes out of the outer class verifier on Android 7-9. */
    private static final class Api29 {
        private Api29() { }

        static void configurePlaybackCapture(AudioRecord.Builder builder, MediaProjection projection) {
            builder.setAudioPlaybackCaptureConfig(
                    new AudioPlaybackCaptureConfiguration.Builder(projection)
                            .addMatchingUsage(AudioAttributes.USAGE_MEDIA)
                            .addMatchingUsage(AudioAttributes.USAGE_GAME)
                            .build());
        }
    }

    private static void validateDisplay(Surface surface, int width, int height, int densityDpi) {
        if (surface == null || width < 1 || width > 8_192 || height < 1 || height > 8_192
                || densityDpi <= 0) {
            throw new IllegalArgumentException("invalid projection surface or dimensions");
        }
    }

    private static void checkMainThread() {
        if (Looper.myLooper() != Looper.getMainLooper()) {
            throw new IllegalStateException("projection lifecycle must run on the Android main thread");
        }
    }
}
