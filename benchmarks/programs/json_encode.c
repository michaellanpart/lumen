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

#define N 5000000

int main(void) {
    static char buf[1 << 20]; /* 1 MiB */
    setvbuf(stdout, buf, _IOFBF, sizeof buf);
    for (int64_t i = 0; i < N; i++) {
        printf("{\"id\":%lld,\"name\":\"alice\",\"active\":true,\"score\":3.14}\n",
               (long long)i);
    }
    return 0;
}
