// Benchmark: encode 5,000,000 JSON records to stdout.
import java.io.BufferedWriter;
import java.io.IOException;
import java.io.OutputStreamWriter;

public final class JsonEncode {
    private static final int N = 5_000_000;

    public static void main(String[] args) throws IOException {
        BufferedWriter w = new BufferedWriter(new OutputStreamWriter(System.out), 1 << 20);
        for (long i = 0; i < N; i++) {
            w.write("{\"id\":");
            w.write(Long.toString(i));
            w.write(",\"name\":\"alice\",\"active\":true,\"score\":3.14}\n");
        }
        w.flush();
    }
}
