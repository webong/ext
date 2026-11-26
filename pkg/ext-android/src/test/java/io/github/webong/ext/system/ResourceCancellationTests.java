package io.github.webong.ext.system;

import io.github.webong.ext.system.fixtures.Fixtures;

import java.util.Set;
import java.util.concurrent.CancellationException;

/** The stop rule of ResourceCancellation: Go's context.Canceled is canceled and DeadlineExceeded is timeout. */
public final class ResourceCancellationTests {
    private static void check(boolean value, String name) {
        if (!value) throw new AssertionError(name);
    }

    private static String stopped(ResourceCancellation cancellation) {
        try {
            cancellation.check();
            return null;
        } catch (ResourceCancellationException error) {
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

        ResourceCancellation open = new ResourceCancellation();
        check(stopped(open) == null, "a new cancellation goes on");
        check(stopped(open.withTimeout(60_000)) == null, "a far deadline goes on");

        ResourceCancellation canceled = new ResourceCancellation();
        canceled.cancel();
        check("canceled".equals(stopped(canceled)), "cancel is canceled");

        ResourceCancellation timed = new ResourceCancellation().withTimeout(1);
        expire(1);
        check("timeout".equals(stopped(timed)), "a passed deadline is timeout");
        timed.cancel();
        check("timeout".equals(stopped(timed)), "the first stop is kept");

        ResourceCancellation parent = new ResourceCancellation();
        ResourceCancellation child = parent.withTimeout(60_000);
        parent.cancel();
        check("canceled".equals(stopped(child)), "a child stops with its parent");

        ResourceCancellation shortParent = new ResourceCancellation().withTimeout(1);
        ResourceCancellation longChild = shortParent.withTimeout(60_000);
        expire(1);
        check("timeout".equals(stopped(longChild)), "a child keeps its parent's earlier deadline");

        ResourceCancellation untouched = new ResourceCancellation();
        untouched.withTimeout(1).cancel();
        check(stopped(untouched) == null, "canceling a child leaves its parent");

        ResourceCancellation other = new ResourceCancellation();
        Thread canceler = new Thread(other::cancel);
        canceler.start();
        canceler.join();
        check("canceled".equals(stopped(other)), "a cancel from another thread is seen");

        for (long timeout : new long[] {0, -1}) {
            boolean refused = false;
            try { new ResourceCancellation().withTimeout(timeout); } catch (IllegalArgumentException expected) { refused = true; }
            check(refused, "a timeout of " + timeout + " is refused");
        }
        System.out.println("resource cancellation: ok");
    }

    private ResourceCancellationTests() {}
}
