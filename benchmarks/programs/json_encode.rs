// Benchmark: encode 5,000,000 JSON records to stdout.
// Hand-rolled, no serde dep: matches the C version byte-for-byte.
// Build: rustc -O -o json-rust json_encode.rs
use std::io::{BufWriter, Write};

const N: i64 = 5_000_000;

fn main() {
    let stdout = std::io::stdout();
    let mut w = BufWriter::with_capacity(1 << 20, stdout.lock());
    for i in 0..N {
        w.write_all(b"{\"id\":").unwrap();
        write!(w, "{}", i).unwrap();
        w.write_all(b",\"name\":\"alice\",\"active\":true,\"score\":3.14}\n")
            .unwrap();
    }
}
