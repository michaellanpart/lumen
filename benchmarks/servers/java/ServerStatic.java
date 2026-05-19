// Minimal HTTP static server for benchmark parity.
import com.sun.net.httpserver.HttpExchange;
import com.sun.net.httpserver.HttpHandler;
import com.sun.net.httpserver.HttpServer;
import java.io.IOException;
import java.io.OutputStream;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;

public final class ServerStatic {
    private static final byte[] BODY = "hello\n".getBytes(StandardCharsets.UTF_8);

    public static void main(String[] args) throws IOException {
        String host = args.length > 0 ? args[0] : "127.0.0.1";
        int port = args.length > 1 ? Integer.parseInt(args[1]) : 8080;

        HttpServer server = HttpServer.create(new InetSocketAddress(host, port), 1024);
        ExecutorService pool = Executors.newFixedThreadPool(Math.max(4, Runtime.getRuntime().availableProcessors() * 2));
        server.setExecutor(pool);
        server.createContext("/", new HelloHandler());
        server.start();
        System.err.println("java(static): listening on " + host + ":" + port);
    }

    private static final class HelloHandler implements HttpHandler {
        @Override
        public void handle(HttpExchange ex) throws IOException {
            ex.getResponseHeaders().set("Content-Type", "text/plain");
            ex.sendResponseHeaders(200, BODY.length);
            try (OutputStream os = ex.getResponseBody()) {
                os.write(BODY);
            }
        }
    }
}
