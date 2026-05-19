// Dynamic-handler HTTP benchmark server (Rust std-only).

use std::env;
use std::io::{Read, Write};
use std::net::{TcpListener, TcpStream};
use std::thread;

struct Service {
    body: &'static str,
}

type HandlerFn = fn(&Service) -> &'static str;

fn handle(svc: &Service) -> &'static str {
    svc.body
}

fn write_response(stream: &mut TcpStream, body: &str) -> std::io::Result<()> {
    let hdr = format!(
        "HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: {}\r\nConnection: keep-alive\r\n\r\n",
        body.len()
    );
    stream.write_all(hdr.as_bytes())?;
    stream.write_all(body.as_bytes())?;
    Ok(())
}

fn handle_conn(mut stream: TcpStream, handler: HandlerFn, svc: &'static Service) {
    let _ = stream.set_nodelay(true);
    let mut buf = [0u8; 4096];
    loop {
        let n = match stream.read(&mut buf) {
            Ok(0) | Err(_) => return,
            Ok(n) => n,
        };

        let mut reqs = 0usize;
        for i in 3..n {
            if buf[i - 3] == b'\r' && buf[i - 2] == b'\n' && buf[i - 1] == b'\r' && buf[i] == b'\n' {
                reqs += 1;
            }
        }
        if reqs == 0 {
            reqs = 1;
        }

        for _ in 0..reqs {
            let body = handler(svc);
            if write_response(&mut stream, body).is_err() {
                return;
            }
        }
    }
}

fn main() {
    let args: Vec<String> = env::args().collect();
    let host: &str = if args.len() > 1 { &args[1] } else { "127.0.0.1" };
    let port: u16 = if args.len() > 2 {
        args[2].parse().unwrap_or(8080)
    } else {
        8080
    };

    let svc: &'static Service = Box::leak(Box::new(Service { body: "hello\n" }));

    let listener = TcpListener::bind((host, port)).expect("bind");
    eprintln!("rust(dynamic): listening on {}:{}", host, port);

    for stream in listener.incoming() {
        match stream {
            Ok(s) => {
                thread::spawn(move || handle_conn(s, handle, svc));
            }
            Err(_) => continue,
        }
    }
}
