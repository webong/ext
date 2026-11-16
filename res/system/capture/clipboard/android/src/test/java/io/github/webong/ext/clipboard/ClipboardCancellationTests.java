package io.github.webong.ext.clipboard;


/** The stop rule of ClipboardCancellation: Go's context.Canceled is canceled and DeadlineExceeded is timeout. */
public final class ClipboardCancellationTests {
    private static void check(boolean value, String name) {
        if (!value) throw new AssertionError(name);
    }

    private static String stopped(ClipboardCancellation cancellation) {
        try {
            cancellation.check();
            return null;
        } catch (ClipboardException error) {
            return error.code;
        }
    }

    private static void expire(long millis) throws InterruptedException {
        Thread.sleep(millis + 5);
    }

    public static void main(String[] arguments) throws Exception {

        ClipboardCancellation open = new ClipboardCancellation();
        check(stopped(open) == null, "a new cancellation goes on");
        check(stopped(open.withTimeout(60_000)) == null, "a far deadline goes on");

        ClipboardCancellation canceled = new ClipboardCancellation();
        canceled.cancel();
        check("canceled".equals(stopped(canceled)), "cancel is canceled");

        ClipboardCancellation timed = new ClipboardCancellation().withTimeout(1);
        expire(1);
        check("timeout".equals(stopped(timed)), "a passed deadline is timeout");
        timed.cancel();
        check("timeout".equals(stopped(timed)), "the first stop is kept");

        ClipboardCancellation parent = new ClipboardCancellation();
        ClipboardCancellation child = parent.withTimeout(60_000);
        parent.cancel();
        check("canceled".equals(stopped(child)), "a child stops with its parent");

        ClipboardCancellation shortParent = new ClipboardCancellation().withTimeout(1);
        ClipboardCancellation longChild = shortParent.withTimeout(60_000);
        expire(1);
        check("timeout".equals(stopped(longChild)), "a child keeps its parent's earlier deadline");

        ClipboardCancellation untouched = new ClipboardCancellation();
        untouched.withTimeout(1).cancel();
        check(stopped(untouched) == null, "canceling a child leaves its parent");

        ClipboardCancellation other = new ClipboardCancellation();
        Thread canceler = new Thread(other::cancel);
        canceler.start();
        canceler.join();
        check("canceled".equals(stopped(other)), "a cancel from another thread is seen");

        for (long timeout : new long[] {0, -1}) {
            boolean refused = false;
            try { new ClipboardCancellation().withTimeout(timeout); } catch (IllegalArgumentException expected) { refused = true; }
            check(refused, "a timeout of " + timeout + " is refused");
        }
        System.out.println("clipboard cancellation: ok");
    }

    private ClipboardCancellationTests() {}
}
