// Dynamic-handler HTTP benchmark server in Go using raw sockets.

package main

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
)

type Service struct {
	body string
}

type HandlerFn func(*Service) string

func handle(svc *Service) string {
	return svc.body
}

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

func writeResponse(c *net.TCPConn, body string) error {
	hdr := "HTTP/1.1 200 OK\r\n" +
		"Content-Type: text/plain\r\n" +
		"Content-Length: " + strconv.Itoa(len(body)) + "\r\n" +
		"Connection: keep-alive\r\n" +
		"\r\n"
	if _, err := c.Write([]byte(hdr)); err != nil {
		return err
	}
	if _, err := c.Write([]byte(body)); err != nil {
		return err
	}
	return nil
}

func handleConn(c *net.TCPConn, handler HandlerFn, svc *Service) {
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
			if err := writeResponse(c, handler(svc)); err != nil {
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

	svc := &Service{body: "hello\n"}

	l, err := net.Listen("tcp", addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer l.Close()

	fmt.Fprintf(os.Stderr, "go(dynamic): listening on %s\n", addr)
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
		go handleConn(tcpConn, handle, svc)
	}
}
