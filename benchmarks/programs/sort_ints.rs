// Benchmark: deterministic repeated insertion-sort with checksum output.

const N: usize = 64;
const REPS: usize = 50_000;

fn next_i64(s: &mut i64) -> i64 {
    // 31-bit LCG: keeps values positive and avoids signed-overflow UB.
    *s = ((*s * 1_103_515_245) + 12_345) % 2_147_483_647;
    *s
}

fn main() {
    let mut a = [0i64; N];
    let mut state = 123_456_789i64;
    let mut chk = 0i64;

    for _ in 0..REPS {
        for v in &mut a {
            *v = next_i64(&mut state);
        }

        for i in 1..N {
            let key = a[i];
            let mut j = i;
            while j > 0 && a[j - 1] > key {
                a[j] = a[j - 1];
                j -= 1;
            }
            a[j] = key;
        }

        for (i, v) in a.iter().enumerate() {
            chk = (chk + *v + i as i64) % 2_147_483_647;
        }
    }

    println!("{}", chk);
}
