// Benchmark: encode 5,000,000 JSON records to stdout.
//
// Record shape: {"id":<i64>,"name":"alice","active":true,"score":3.14}
// Stdout is set to a 1 MiB fully-buffered allocation. Run with `> /dev/null`.
//
// Build: cc -O2 -o json-c json_encode.c
// Run:   ./json-c > /dev/null

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#define N 5000000

int main(void) {
    static char outbuf[1 << 20]; /* 1 MiB */
    setvbuf(stdout, outbuf, _IOFBF, sizeof outbuf);

    static const char prefix[] = "{\"id\":";
    static const char suffix[] = ",\"name\":\"alice\",\"active\":true,\"score\":3.14}\n";
    const size_t prefix_len = sizeof(prefix) - 1;
    const size_t suffix_len = sizeof(suffix) - 1;

    char nbuf[32];
    for (int64_t i = 0; i < N; i++) {
        fwrite(prefix, 1, prefix_len, stdout);
        int n = snprintf(nbuf, sizeof nbuf, "%lld", (long long)i);
        fwrite(nbuf, 1, (size_t)n, stdout);
        fwrite(suffix, 1, suffix_len, stdout);
    }
    return 0;
}
