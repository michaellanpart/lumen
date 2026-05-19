// Minimal HTTP/1.1 "hello\n" server in Rust using only std (no cargo deps).
// Multi-threaded via std::thread (one thread per connection, with keep-alive).
//
// Build: rustc -O -o server server.rs
// Run:   ./server 127.0.0.1 8080

use std::env;
use std::io::{Read, Write};
use std::net::TcpListener;
use std::thread;

const RESPONSE: &[u8] = b"HTTP/1.1 200 OK\r\n\
Content-Type: text/plain\r\n\
Content-Length: 6\r\n\
Connection: keep-alive\r\n\
\r\n\
hello\n";

fn handle(mut stream: std::net::TcpStream) {
    let _ = stream.set_nodelay(true);
    let mut buf = [0u8; 4096];
    loop {
        let n = match stream.read(&mut buf) {
            Ok(0) | Err(_) => return,
            Ok(n) => n,
        };
        // Count request boundaries (\r\n\r\n).
        let mut reqs = 0usize;
        for i in 3..n {
            if buf[i-3] == b'\r' && buf[i-2] == b'\n'
                && buf[i-1] == b'\r' && buf[i] == b'\n' { reqs += 1; }
        }
        if reqs == 0 { reqs = 1; }
        for _ in 0..reqs {
            if stream.write_all(RESPONSE).is_err() { return; }
        }
    }
}

fn main() {
    let args: Vec<String> = env::args().collect();
    let host: &str = if args.len() > 1 { &args[1] } else { "127.0.0.1" };
    let port: u16  = if args.len() > 2 { args[2].parse().unwrap_or(8080) } else { 8080 };

    let listener = TcpListener::bind((host, port)).expect("bind");
    eprintln!("rust: listening on {}:{}", host, port);

    for stream in listener.incoming() {
        match stream {
            Ok(s) => { thread::spawn(move || handle(s)); }
            Err(_) => continue,
        }
    }
}
