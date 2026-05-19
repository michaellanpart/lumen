// Dynamic-handler HTTP benchmark server (C++).

#include <arpa/inet.h>
#include <netinet/in.h>
#include <netinet/tcp.h>
#include <sys/socket.h>
#include <unistd.h>

#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <string>
#include <thread>

struct Service {
    const char *body;
};

using HandlerFn = const char *(*)(const Service *);

static const char *handle(const Service *svc) {
    return svc->body;
}

static void handle_conn(int fd, HandlerFn handler, const Service *svc) {
    char buf[4096];
    for (;;) {
        ssize_t n = recv(fd, buf, sizeof buf, 0);
        if (n <= 0) break;

        int reqs = 0;
        for (ssize_t i = 3; i < n; ++i) {
            if (buf[i-3] == '\r' && buf[i-2] == '\n' &&
                buf[i-1] == '\r' && buf[i] == '\n') ++reqs;
        }
        if (reqs == 0) reqs = 1;

        for (int i = 0; i < reqs; ++i) {
            const char *body = handler(svc);
            const size_t blen = std::strlen(body);
            char hdr[160];
            int hlen = std::snprintf(hdr, sizeof(hdr),
                "HTTP/1.1 200 OK\r\n"
                "Content-Type: text/plain\r\n"
                "Content-Length: %zu\r\n"
                "Connection: keep-alive\r\n\r\n",
                blen);
            if (hlen <= 0 || static_cast<size_t>(hlen) >= sizeof(hdr)) {
                close(fd);
                return;
            }
            if (send(fd, hdr, static_cast<size_t>(hlen), 0) < 0) {
                close(fd);
                return;
            }
            if (send(fd, body, blen, 0) < 0) {
                close(fd);
                return;
            }
        }
    }
    close(fd);
}

int main(int argc, char **argv) {
    const char *host = argc > 1 ? argv[1] : "127.0.0.1";
    int port = argc > 2 ? std::atoi(argv[2]) : 8080;

    Service svc{"hello\n"};

    int s = ::socket(AF_INET, SOCK_STREAM, 0);
    if (s < 0) {
        std::perror("socket");
        return 1;
    }
    int one = 1;
    setsockopt(s, SOL_SOCKET, SO_REUSEADDR, &one, sizeof one);

    sockaddr_in addr{};
    addr.sin_family = AF_INET;
    addr.sin_port = htons(port);
    inet_pton(AF_INET, host, &addr.sin_addr);
    if (bind(s, (sockaddr*)&addr, sizeof addr) < 0) {
        std::perror("bind");
        return 1;
    }
    if (listen(s, 1024) < 0) {
        std::perror("listen");
        return 1;
    }

    std::fprintf(stderr, "cpp(dynamic): listening on %s:%d\n", host, port);

    for (;;) {
        int c = ::accept(s, nullptr, nullptr);
        if (c < 0) continue;
        int nd = 1;
        setsockopt(c, IPPROTO_TCP, TCP_NODELAY, &nd, sizeof nd);
        std::thread(handle_conn, c, handle, &svc).detach();
    }
}
