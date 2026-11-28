package io.github.webong.ext.system.share;

import io.github.webong.ext.system.fixtures.Fixtures;

import java.util.Set;

/** The stop rule of ShareCancellation: Go's context.Canceled is canceled and DeadlineExceeded is timeout. */
public final class ShareCancellationTests {
    private static void check(boolean value, String name) {
        if (!value) throw new AssertionError(name);
    }

    private static String stopped(ShareCancellation cancellation) {
        try {
            cancellation.check();
            return null;
        } catch (ShareException error) {
            return error.kind();
        }
    }

    private static void expire(long millis) throws InterruptedException {
        Thread.sleep(millis + 5);
    }

    public static void main(String[] arguments) throws Exception {
        Set<String> codes = Fixtures.codes();
        check(codes.contains("timeout") && codes.contains("canceled"), "timeout and canceled are in codes.json");

        ShareCancellation open = new ShareCancellation();
        check(stopped(open) == null, "a new cancellation goes on");
        check(stopped(open.withTimeout(60_000)) == null, "a far deadline goes on");

        ShareCancellation canceled = new ShareCancellation();
        canceled.cancel();
        check("canceled".equals(stopped(canceled)), "cancel is canceled");

        ShareCancellation timed = new ShareCancellation().withTimeout(1);
        expire(1);
        check("timeout".equals(stopped(timed)), "a passed deadline is timeout");
        timed.cancel();
        check("timeout".equals(stopped(timed)), "the first stop is kept");

        ShareCancellation parent = new ShareCancellation();
        ShareCancellation child = parent.withTimeout(60_000);
        parent.cancel();
        check("canceled".equals(stopped(child)), "a child stops with its parent");

        ShareCancellation shortParent = new ShareCancellation().withTimeout(1);
        ShareCancellation longChild = shortParent.withTimeout(60_000);
        expire(1);
        check("timeout".equals(stopped(longChild)), "a child keeps its parent's earlier deadline");

        ShareCancellation untouched = new ShareCancellation();
        untouched.withTimeout(1).cancel();
        check(stopped(untouched) == null, "canceling a child leaves its parent");

        ShareCancellation other = new ShareCancellation();
        Thread canceler = new Thread(other::cancel);
        canceler.start();
        canceler.join();
        check("canceled".equals(stopped(other)), "a cancel from another thread is seen");

        for (long timeout : new long[] {0, -1}) {
            boolean refused = false;
            try { new ShareCancellation().withTimeout(timeout); } catch (IllegalArgumentException expected) { refused = true; }
            check(refused, "a timeout of " + timeout + " is refused");
        }
        System.out.println("share cancellation: ok");
    }

    private ShareCancellationTests() {}
}
