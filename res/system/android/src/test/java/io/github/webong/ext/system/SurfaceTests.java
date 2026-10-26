package io.github.webong.ext.system;

import java.util.List;

public final class SurfaceTests {
    public static void main(String[] arguments) {
        boolean rejected = false;
        try { new SurfaceOptions(0, 1); } catch (IllegalArgumentException expected) { rejected = true; }
        assert rejected;

        SurfaceBounds bounds = new SurfaceBounds(-10, 0, 100, 80);
        DisplayInfo display = new DisplayInfo("display:opaque", bounds, 2, true);
        WindowInfo window = new WindowInfo("window:opaque", 42, "42:1", new SurfaceBounds(1, 2, 30, 40), true);
        SurfaceCollectionStatus status = new SurfaceCollectionStatus("complete", null, "2026-10-26T00:00:00.000Z");
        SurfaceSnapshot snapshot = new SurfaceSnapshot("fixture", "fixture-points", "2026-10-26T00:00:00.000Z",
                List.of(display), List.of(window), status, status);
        assert snapshot.displays().equals(List.of(display));
        assert snapshot.windows().equals(List.of(window));
        assert snapshot.windows().get(0).processStartId().equals("42:1");
    }

    private SurfaceTests() {}
}
