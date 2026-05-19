// Dynamic-handler HTTP benchmark server in Java.
import com.sun.net.httpserver.HttpExchange;
import com.sun.net.httpserver.HttpHandler;
import com.sun.net.httpserver.HttpServer;
import java.io.IOException;
import java.io.OutputStream;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;

public final class ServerDynamic {
    private static final class Service {
        final byte[] body;

        Service(String body) {
            this.body = body.getBytes(StandardCharsets.UTF_8);
        }
    }

    private interface HandlerFn {
        byte[] handle(Service svc);
    }

    private static byte[] handle(Service svc) {
        return svc.body;
    }

    public static void main(String[] args) throws IOException {
        String host = args.length > 0 ? args[0] : "127.0.0.1";
        int port = args.length > 1 ? Integer.parseInt(args[1]) : 8080;

        Service svc = new Service("hello\n");
        HandlerFn fn = ServerDynamic::handle;

        HttpServer server = HttpServer.create(new InetSocketAddress(host, port), 1024);
        ExecutorService pool = Executors.newFixedThreadPool(Math.max(4, Runtime.getRuntime().availableProcessors() * 2));
        server.setExecutor(pool);
        server.createContext("/", new DynamicHandler(fn, svc));
        server.start();
        System.err.println("java(dynamic): listening on " + host + ":" + port);
    }

    private static final class DynamicHandler implements HttpHandler {
        private final HandlerFn fn;
        private final Service svc;

        DynamicHandler(HandlerFn fn, Service svc) {
            this.fn = fn;
            this.svc = svc;
        }

        @Override
        public void handle(HttpExchange ex) throws IOException {
            byte[] body = fn.handle(svc);
            ex.getResponseHeaders().set("Content-Type", "text/plain");
            ex.sendResponseHeaders(200, body.length);
            try (OutputStream os = ex.getResponseBody()) {
                os.write(body);
            }
        }
    }
}
