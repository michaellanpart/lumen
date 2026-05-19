// Benchmark: deterministic repeated insertion-sort with checksum output.
public final class SortInts {
    private static final int N = 64;
    private static final int REPS = 50_000;
    private static final long MOD = 2_147_483_647L;

    private static long nextI64(long[] state) {
        state[0] = (state[0] * 1_103_515_245L + 12_345L) % MOD;
        return state[0];
    }

    public static void main(String[] args) {
        long[] a = new long[N];
        long[] state = new long[] {123_456_789L};
        long chk = 0;

        for (int rep = 0; rep < REPS; rep++) {
            for (int i = 0; i < N; i++) {
                a[i] = nextI64(state);
            }

            for (int i = 1; i < N; i++) {
                long key = a[i];
                int j = i - 1;
                while (j >= 0 && a[j] > key) {
                    a[j + 1] = a[j];
                    j--;
                }
                a[j + 1] = key;
            }

            for (int i = 0; i < N; i++) {
                chk = (chk + a[i] + i) % MOD;
            }
        }

        System.out.println(chk);
    }
}
