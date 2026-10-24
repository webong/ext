import io.github.webong.ext.plugin.Call;
import io.github.webong.ext.plugin.Contract;
import io.github.webong.ext.plugin.Descriptor;
import io.github.webong.ext.plugin.Guest;
import io.github.webong.ext.plugin.Identity;
import io.github.webong.ext.plugin.JsonLine;
import io.github.webong.ext.plugin.Operation;
import io.github.webong.ext.plugin.RemoteError;
import java.util.List;

/**
 * The ext.conformance/v1 fixture every language implements, run as a JSON-line
 * guest process so the shared Go suite can check it.
 */
public final class ConformanceGuest {
    private ConformanceGuest() {}

    public static void main(String[] args) throws Exception {
        Descriptor descriptor = new Descriptor(
                new Identity("ctx/conformance", "fixture-1"),
                List.of(new Contract("ext.conformance", "v1", List.of(
                        new Operation("echo"), new Operation("wait"),
                        new Operation("private-error"), new Operation("public-error")))));
        try (Guest guest = new Guest(descriptor, ConformanceGuest::handle)) {
            JsonLine.serveStdio(guest);
        }
    }

    static byte[] handle(Call call) throws Exception {
        switch (call.operation()) {
            case "wait":
                while (true) {
                    call.checkDeadline();
                    Thread.sleep(1);
                }
            case "private-error":
                throw new IllegalStateException("secret internal detail");
            case "public-error":
                throw new RemoteError("busy", "try later", 10);
            default:
                return call.payload();
        }
    }
}
