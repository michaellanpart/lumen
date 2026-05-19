// Minimal HTTP/1.1 "hello\n" server in Go using raw net sockets.
//
// It intentionally mirrors the C/C++/Rust benchmark servers so each
// implementation emits the same bytes on the wire.
package main

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
)

var response = []byte(
	"HTTP/1.1 200 OK\r\n" +
		"Content-Type: text/plain\r\n" +
		"Content-Length: 6\r\n" +
		"Connection: keep-alive\r\n" +
		"\r\n" +
		"hello\n",
)

func countRequests(buf []byte, n int) int {
	reqs := 0
	for i := 3; i < n; i++ {
		if buf[i-3] == '\r' && buf[i-2] == '\n' && buf[i-1] == '\r' && buf[i] == '\n' {
			reqs++
		}
	}
	if reqs == 0 {
		return 1
	}
	return reqs
}

func handleConn(c *net.TCPConn) {
	defer c.Close()
	_ = c.SetNoDelay(true)

	buf := make([]byte, 4096)
	for {
		n, err := c.Read(buf)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return
			}
			return
		}
		for i := 0; i < countRequests(buf, n); i++ {
			if _, err := c.Write(response); err != nil {
				return
			}
		}
	}
}

func main() {
	addr := "127.0.0.1:8080"
	if len(os.Args) > 1 {
		addr = os.Args[1]
	}

	l, err := net.Listen("tcp", addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer l.Close()

	fmt.Fprintf(os.Stderr, "go: listening on %s\n", addr)
	for {
		conn, err := l.Accept()
		if err != nil {
			continue
		}
		tcpConn, ok := conn.(*net.TCPConn)
		if !ok {
			_ = conn.Close()
			continue
		}
		go handleConn(tcpConn)
	}
}
