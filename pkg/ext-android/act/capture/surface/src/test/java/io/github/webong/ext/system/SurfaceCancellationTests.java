package io.github.webong.ext.system;

import io.github.webong.ext.system.fixtures.Fixtures;

import java.util.Set;
import java.util.concurrent.CancellationException;

/** The stop rule of SurfaceCancellation: Go's context.Canceled is canceled and DeadlineExceeded is timeout. */
public final class SurfaceCancellationTests {
    private static void check(boolean value, String name) {
        if (!value) throw new AssertionError(name);
    }

    private static String stopped(SurfaceCancellation cancellation) {
        try {
            cancellation.check();
            return null;
        } catch (SurfaceCancellationException error) {
            check(error instanceof CancellationException, "a stop is a CancellationException");
            return error.code();
        }
    }

    private static void expire(long millis) throws InterruptedException {
        Thread.sleep(millis + 5);
    }

    public static void main(String[] arguments) throws Exception {
        Set<String> codes = Fixtures.codes();
        check(codes.contains("timeout") && codes.contains("canceled"), "timeout and canceled are in codes.json");

        SurfaceCancellation open = new SurfaceCancellation();
        check(stopped(open) == null, "a new cancellation goes on");
        check(stopped(open.withTimeout(60_000)) == null, "a far deadline goes on");

        SurfaceCancellation canceled = new SurfaceCancellation();
        canceled.cancel();
        check("canceled".equals(stopped(canceled)), "cancel is canceled");

        SurfaceCancellation timed = new SurfaceCancellation().withTimeout(1);
        expire(1);
        check("timeout".equals(stopped(timed)), "a passed deadline is timeout");
        timed.cancel();
        check("timeout".equals(stopped(timed)), "the first stop is kept");

        SurfaceCancellation parent = new SurfaceCancellation();
        SurfaceCancellation child = parent.withTimeout(60_000);
        parent.cancel();
        check("canceled".equals(stopped(child)), "a child stops with its parent");

        SurfaceCancellation shortParent = new SurfaceCancellation().withTimeout(1);
        SurfaceCancellation longChild = shortParent.withTimeout(60_000);
        expire(1);
        check("timeout".equals(stopped(longChild)), "a child keeps its parent's earlier deadline");

        SurfaceCancellation untouched = new SurfaceCancellation();
        untouched.withTimeout(1).cancel();
        check(stopped(untouched) == null, "canceling a child leaves its parent");

        SurfaceCancellation other = new SurfaceCancellation();
        Thread canceler = new Thread(other::cancel);
        canceler.start();
        canceler.join();
        check("canceled".equals(stopped(other)), "a cancel from another thread is seen");

        for (long timeout : new long[] {0, -1}) {
            boolean refused = false;
            try { new SurfaceCancellation().withTimeout(timeout); } catch (IllegalArgumentException expected) { refused = true; }
            check(refused, "a timeout of " + timeout + " is refused");
        }
        boolean refused = false;
        try { new SurfaceOptions(1, 1, 0); } catch (IllegalArgumentException expected) { refused = true; }
        check(refused, "a surface options timeout of zero is refused");
        refused = false;
        try { new AccessibilityOptions(1, 1, false, 0); } catch (IllegalArgumentException expected) { refused = true; }
        check(refused, "an accessibility options timeout of zero is refused");
        check(new SurfaceOptions().timeoutMillis() == 5_000, "Go's surface default of five seconds");
        check(new AccessibilityOptions().timeoutMillis() == 5_000, "Go's accessibility default of five seconds");
        check(new SurfaceOptions(1, 1, 250).timeoutMillis() == 250, "a surface timeout is kept");
        System.out.println("surface cancellation: ok");
    }

    private SurfaceCancellationTests() {}
}
