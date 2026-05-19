#!/usr/bin/env bash
# benchmarks/run_json.sh — JSON encode throughput comparison.
# Runs each encoder REPEAT times and reports average wall-clock seconds.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

REPEAT="${REPEAT:-3}"
OUT="${OUT:-benchmarks/results/json}"
mkdir -p "$OUT" bin

echo "==> Building JSON encoders"
go build -o bin/lumen ./cmd/lumen
go build -o bin/json-go ./benchmarks/programs/gojson
cc -O2 -o bin/json-c benchmarks/programs/json_encode.c
rustc -O -o bin/json-rust benchmarks/programs/json_encode.rs 2>/dev/null
./bin/lumen build benchmarks/programs/json_encode.lm -o bin/json-lumen 2>/dev/null

HAS_JAVA=0
if command -v javac >/dev/null 2>&1 && command -v java >/dev/null 2>&1; then
    HAS_JAVA=1
    javac -d bin benchmarks/programs/JsonEncode.java
fi

echo
printf '%-8s | %10s\n' lang avg_sec
printf '%s\n' "---------+-----------"

bench_one() {
    local lang="$1"
    local cmd="$2"
    local times_file="$OUT/$lang.times"
    : > "$times_file"

    for _ in $(seq 1 "$REPEAT"); do
        /usr/bin/time -p sh -c "$cmd > /dev/null" 2> "$OUT/$lang.time.raw"
        awk '/^real /{print $2}' "$OUT/$lang.time.raw" >> "$times_file"
    done

    local avg
    avg=$(awk '{s+=$1; n++} END{if(n>0) printf "%.3f", s/n; else print "0.000"}' "$times_file")
    printf '%-8s | %10s\n' "$lang" "$avg"
}

bench_one c "./bin/json-c"
bench_one rust "./bin/json-rust"
bench_one go "./bin/json-go"
if [ "$HAS_JAVA" = "1" ]; then
    bench_one java "java -cp ./bin JsonEncode"
fi
bench_one lumen "./bin/json-lumen"

echo
echo "Raw timing files in $OUT"
