// Benchmark: recursive fib(40), matching the C/Go/Rust/Lumen workload.
public final class Fib {
    private static long fib(long n) {
        if (n < 2) {
            return n;
        }
        return fib(n - 1) + fib(n - 2);
    }

    public static void main(String[] args) {
        System.out.println(fib(40));
    }
}
