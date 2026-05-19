// Dynamic-handler HTTP benchmark server (C).
// Matches the baseline server architecture but routes each request through
// a handler callback with shared service state.

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

typedef const char *(*handler_fn)(const void *);

typedef struct {
    const char *body;
} service_t;

typedef struct {
    int fd;
    handler_fn handler;
    const void *svc;
} conn_t;

static const char *handle(const void *svc_any) {
    const service_t *svc = (const service_t *)svc_any;
    return svc->body;
}

static void *handle_conn(void *arg) {
    conn_t *ctx = (conn_t *)arg;
    int fd = ctx->fd;
    handler_fn handler = ctx->handler;
    const void *svc = ctx->svc;
    free(ctx);

    char buf[4096];
    for (;;) {
        ssize_t n = recv(fd, buf, sizeof buf, 0);
        if (n <= 0) break;
        int reqs = 0;
        for (ssize_t i = 3; i < n; i++) {
            if (buf[i-3] == '\r' && buf[i-2] == '\n' &&
                buf[i-1] == '\r' && buf[i] == '\n') reqs++;
        }
        if (reqs == 0) reqs = 1;

        for (int i = 0; i < reqs; i++) {
            const char *body = handler(svc);
            const size_t blen = strlen(body);
            char hdr[160];
            int hlen = snprintf(hdr, sizeof(hdr),
                "HTTP/1.1 200 OK\r\n"
                "Content-Type: text/plain\r\n"
                "Content-Length: %zu\r\n"
                "Connection: keep-alive\r\n\r\n",
                blen);
            if (hlen <= 0 || (size_t)hlen >= sizeof(hdr)) goto done;
            if (send(fd, hdr, (size_t)hlen, 0) < 0) goto done;
            if (send(fd, body, blen, 0) < 0) goto done;
        }
    }

done:
    close(fd);
    return NULL;
}

int main(int argc, char **argv) {
    const char *host = argc > 1 ? argv[1] : "127.0.0.1";
    int port = argc > 2 ? atoi(argv[2]) : 8080;

    service_t svc = {.body = "hello\n"};

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

    fprintf(stderr, "c(dynamic): listening on %s:%d\n", host, port);

    for (;;) {
        int c = accept(s, NULL, NULL);
        if (c < 0) { if (errno == EINTR) continue; perror("accept"); break; }
        int nd = 1;
        setsockopt(c, IPPROTO_TCP, TCP_NODELAY, &nd, sizeof nd);

        conn_t *ctx = (conn_t *)malloc(sizeof(conn_t));
        if (!ctx) { close(c); continue; }
        ctx->fd = c;
        ctx->handler = handle;
        ctx->svc = &svc;

        pthread_t t;
        if (pthread_create(&t, NULL, handle_conn, ctx) != 0) {
            free(ctx);
            close(c);
            continue;
        }
        pthread_detach(t);
    }
    return 0;
}
