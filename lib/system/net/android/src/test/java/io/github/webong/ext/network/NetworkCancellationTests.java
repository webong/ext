package io.github.webong.ext.network;

import io.github.webong.ext.system.fixtures.Fixtures;

import java.util.Set;
import java.util.concurrent.CancellationException;

/** The stop rule of NetworkCancellation: Go's context.Canceled is canceled and DeadlineExceeded is timeout. */
public final class NetworkCancellationTests {
    private static void check(boolean value, String name) {
        if (!value) throw new AssertionError(name);
    }

    private static String stopped(NetworkCancellation cancellation) {
        try {
            cancellation.check();
            return null;
        } catch (NetworkCancellationException error) {
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

        NetworkCancellation open = new NetworkCancellation();
        check(stopped(open) == null, "a new cancellation goes on");
        check(stopped(open.withTimeout(60_000)) == null, "a far deadline goes on");

        NetworkCancellation canceled = new NetworkCancellation();
        canceled.cancel();
        check("canceled".equals(stopped(canceled)), "cancel is canceled");

        NetworkCancellation timed = new NetworkCancellation().withTimeout(1);
        expire(1);
        check("timeout".equals(stopped(timed)), "a passed deadline is timeout");
        timed.cancel();
        check("timeout".equals(stopped(timed)), "the first stop is kept");

        NetworkCancellation parent = new NetworkCancellation();
        NetworkCancellation child = parent.withTimeout(60_000);
        parent.cancel();
        check("canceled".equals(stopped(child)), "a child stops with its parent");

        NetworkCancellation shortParent = new NetworkCancellation().withTimeout(1);
        NetworkCancellation longChild = shortParent.withTimeout(60_000);
        expire(1);
        check("timeout".equals(stopped(longChild)), "a child keeps its parent's earlier deadline");

        NetworkCancellation untouched = new NetworkCancellation();
        untouched.withTimeout(1).cancel();
        check(stopped(untouched) == null, "canceling a child leaves its parent");

        NetworkCancellation other = new NetworkCancellation();
        Thread canceler = new Thread(other::cancel);
        canceler.start();
        canceler.join();
        check("canceled".equals(stopped(other)), "a cancel from another thread is seen");

        for (long timeout : new long[] {0, -1}) {
            boolean refused = false;
            try { new NetworkCancellation().withTimeout(timeout); } catch (IllegalArgumentException expected) { refused = true; }
            check(refused, "a timeout of " + timeout + " is refused");
        }
        boolean refused = false;
        try { new NetworkOptions(1, 1, 1, 0); } catch (IllegalArgumentException expected) { refused = true; }
        check(refused, "a network options timeout of zero is refused");
        check(new NetworkOptions().timeoutMillis() == 5_000, "Go's network default of five seconds");
        check(new NetworkOptions(1, 1, 1, 250).timeoutMillis() == 250, "a network timeout is kept");
        System.out.println("network cancellation: ok");
    }

    private NetworkCancellationTests() {}
}
