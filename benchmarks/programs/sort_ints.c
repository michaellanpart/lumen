// Benchmark: deterministic repeated insertion-sort with checksum output.

#include <inttypes.h>
#include <stdint.h>
#include <stdio.h>

#define N 64
#define REPS 50000

static int64_t next_i64(int64_t *s) {
    // 31-bit LCG: keeps values positive and avoids signed-overflow UB.
    *s = ((*s * INT64_C(1103515245)) + INT64_C(12345)) % INT64_C(2147483647);
    return *s;
}

int main(void) {
    int64_t a[N] = {0};
    int64_t state = INT64_C(123456789);
    int64_t chk = 0;

    for (int rep = 0; rep < REPS; rep++) {
        for (int i = 0; i < N; i++) {
            a[i] = next_i64(&state);
        }

        for (int i = 1; i < N; i++) {
            int64_t key = a[i];
            int j = i - 1;
            while (j >= 0 && a[j] > key) {
                a[j + 1] = a[j];
                j--;
            }
            a[j + 1] = key;
        }

        for (int i = 0; i < N; i++) {
            chk = (chk + a[i] + i) % INT64_C(2147483647);
        }
    }

    printf("%" PRId64 "\n", chk);
    return 0;
}
