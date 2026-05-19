// Minimal HTTP/1.1 "hello\n" server in C using raw BSD sockets.
// Multi-threaded via pthread (one thread per connection, with keep-alive).
// This is roughly the cleanest "no framework" comparison.
//
// Build: cc -O2 -o server server.c -lpthread
// Run:   ./server 127.0.0.1 8080

#include <arpa/inet.h>
#include <errno.h>
#include <netinet/in.h>
#include <netinet/tcp.h>
#include <pthread.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <sys/types.h>
#include <unistd.h>

static const char RESPONSE[] =
    "HTTP/1.1 200 OK\r\n"
    "Content-Type: text/plain\r\n"
    "Content-Length: 6\r\n"
    "Connection: keep-alive\r\n"
    "\r\n"
    "hello\n";

static void *handle(void *arg) {
    int fd = (int)(intptr_t)arg;
    char buf[4096];
    for (;;) {
        ssize_t n = recv(fd, buf, sizeof buf, 0);
        if (n <= 0) break;
        // Find request end (\r\n\r\n) — handle pipelined requests crudely
        // by counting how many request lines arrived. For hello-world load
        // this is plenty.
        int reqs = 0;
        for (ssize_t i = 3; i < n; i++) {
            if (buf[i-3] == '\r' && buf[i-2] == '\n' &&
                buf[i-1] == '\r' && buf[i]   == '\n') reqs++;
        }
        if (reqs == 0) reqs = 1;
        for (int i = 0; i < reqs; i++) {
            if (send(fd, RESPONSE, sizeof RESPONSE - 1, 0) < 0) goto done;
        }
    }
done:
    close(fd);
    return NULL;
}

int main(int argc, char **argv) {
    const char *host = argc > 1 ? argv[1] : "127.0.0.1";
    int port = argc > 2 ? atoi(argv[2]) : 8080;

    int s = socket(AF_INET, SOCK_STREAM, 0);
    if (s < 0) { perror("socket"); return 1; }
    int one = 1;
    setsockopt(s, SOL_SOCKET, SO_REUSEADDR, &one, sizeof one);

    struct sockaddr_in addr = {0};
    addr.sin_family = AF_INET;
    addr.sin_port = htons(port);
    inet_pton(AF_INET, host, &addr.sin_addr);
    if (bind(s, (struct sockaddr*)&addr, sizeof addr) < 0) { perror("bind"); return 1; }
    if (listen(s, 1024) < 0) { perror("listen"); return 1; }

    fprintf(stderr, "c: listening on %s:%d\n", host, port);

    for (;;) {
        int c = accept(s, NULL, NULL);
        if (c < 0) { if (errno == EINTR) continue; perror("accept"); break; }
        int nd = 1;
        setsockopt(c, IPPROTO_TCP, TCP_NODELAY, &nd, sizeof nd);
        pthread_t t;
        if (pthread_create(&t, NULL, handle, (void*)(intptr_t)c) != 0) {
            close(c);
            continue;
        }
        pthread_detach(t);
    }
    return 0;
}
