#!/usr/bin/env bash
# benchmarks/run.sh — Run the API-server shootout.
#
# Builds all servers, launches them one at a time, hits each with
# the in-tree load generator, and samples RSS / CPU via `ps`.
#
# Usage:
#   ./benchmarks/run.sh             # default: 64 conns, 10s
#   CONC=128 DUR=15s ./benchmarks/run.sh
#   ONLY=lumen,go ./benchmarks/run.sh
set -euo pipefail
set +m  # disable job-control monitor messages ("Terminated: 15" etc.)

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

CONC="${CONC:-64}"
PIPE="${PIPE:-16}"
DUR="${DUR:-10s}"
WARM="${WARM:-1s}"
ADDR="${ADDR:-127.0.0.1:8080}"
ONLY="${ONLY:-c,cpp,rust,go,java,csharp,lumen}"
LANGS="${LANGS:-c,cpp,rust,go,java,csharp,lumen}"
SCENARIO="${SCENARIO:-static}"
OUT="$ROOT/benchmarks/results/$SCENARIO"
mkdir -p "$OUT" "$ROOT/bin"

HAS_JAVA=0
if command -v javac >/dev/null 2>&1 && command -v java >/dev/null 2>&1; then
    HAS_JAVA=1
fi

HAS_CSHARP=0
if command -v dotnet >/dev/null 2>&1; then
    HAS_CSHARP=1
fi

if [ "$HAS_JAVA" != "1" ]; then
    ONLY=",$ONLY,"
    ONLY="${ONLY//,java,/,}"
    ONLY="${ONLY#,}"
    ONLY="${ONLY%,}"

    LANGS=",$LANGS,"
    LANGS="${LANGS//,java,/,}"
    LANGS="${LANGS#,}"
    LANGS="${LANGS%,}"
    echo "==> Java toolchain not found (javac/java); skipping java benchmark target"
fi

if [ "$HAS_CSHARP" != "1" ]; then
    ONLY=",$ONLY,"
    ONLY="${ONLY//,csharp,/,}"
    ONLY="${ONLY#,}"
    ONLY="${ONLY%,}"

    LANGS=",$LANGS,"
    LANGS="${LANGS//,csharp,/,}"
    LANGS="${LANGS#,}"
    LANGS="${LANGS%,}"
    echo "==> dotnet not found; skipping csharp benchmark target"
fi

echo "==> Building toolchain & servers (scenario=$SCENARIO)"
go build -o bin/lumen        ./cmd/lumen
go build -o bin/loadgen      ./benchmarks/loadgen

if [ "$SCENARIO" = "static" ]; then
    go build -o bin/server-go    ./benchmarks/servers/go
    cc  -O2 -o bin/server-c      benchmarks/servers/c/server.c -lpthread
    c++ -O2 -std=c++17 -o bin/server-cpp benchmarks/servers/cpp/server.cpp
    rustc -O -o bin/server-rust benchmarks/servers/rust/server.rs 2>/dev/null
    ./bin/lumen build benchmarks/servers/lumen/server.lm -o bin/server-lumen 2>/dev/null
    if [ "$HAS_JAVA" = "1" ]; then
        javac -d bin benchmarks/servers/java/ServerStatic.java
    fi
    if [ "$HAS_CSHARP" = "1" ]; then
        dotnet publish -c Release -o bin/server-csharp benchmarks/servers/csharp/static/static.csproj >/dev/null
    fi
elif [ "$SCENARIO" = "dynamic" ]; then
    go build -o bin/server-go    ./benchmarks/servers/go_dynamic
    cc  -O2 -o bin/server-c      benchmarks/servers/c/server_dynamic.c -lpthread
    c++ -O2 -std=c++17 -o bin/server-cpp benchmarks/servers/cpp/server_dynamic.cpp
    rustc -O -o bin/server-rust benchmarks/servers/rust/server_dynamic.rs 2>/dev/null
    ./bin/lumen build benchmarks/servers/lumen/server_dynamic.lm -o bin/server-lumen 2>/dev/null
    if [ "$HAS_JAVA" = "1" ]; then
        javac -d bin benchmarks/servers/java/ServerDynamic.java
    fi
    if [ "$HAS_CSHARP" = "1" ]; then
        dotnet publish -c Release -o bin/server-csharp benchmarks/servers/csharp/dynamic/dynamic.csproj >/dev/null
    fi
else
    echo "unknown SCENARIO=$SCENARIO (expected: static|dynamic)" >&2
    exit 1
fi

echo
echo "Scenario: $SCENARIO | conc=$CONC pipe=$PIPE dur=$DUR warm=$WARM"
printf '%-7s | %12s | %10s | %6s | %9s | %9s | %9s | %8s | %9s\n' \
    lang rps requests errors p50_us p90_us p99_us peak_MB cpu_pct%
printf '%s\n' "--------+--------------+------------+--------+-----------+-----------+-----------+----------+----------"

run_one() {
    local lang="$1" cmd="$2"
    [[ ",$ONLY," == *",$lang,"* ]] || return 0

    # Make absolutely sure nothing is already on the port.
    lsof -nP -iTCP:"${ADDR##*:}" -sTCP:LISTEN -t 2>/dev/null | xargs -I {} kill -9 {} 2>/dev/null || true
    sleep 0.2

    # Start the server, capture PID
    eval "$cmd" >"$OUT/$lang.stdout" 2>"$OUT/$lang.stderr" &
    local launch_pid=$!
    # Wait until the port responds
    local up=0
    for _ in $(seq 1 50); do
        if ! kill -0 "$launch_pid" 2>/dev/null; then break; fi
        if curl -fs -o /dev/null --max-time 0.2 "http://$ADDR/" 2>/dev/null; then up=1; break; fi
        sleep 0.1
    done
    if [ "$up" != "1" ]; then
        printf '%-7s | %s\n' "$lang" "FAILED TO START (see $OUT/$lang.stderr)"
        kill "$launch_pid" 2>/dev/null || true
        wait "$launch_pid" 2>/dev/null || true
        return 0
    fi
    # Find the *real* listener PID (may differ from launch_pid if bash forked
    # a subshell, e.g. when eval is used with redirections).
    local pid
    pid=$(lsof -nP -iTCP:"${ADDR##*:}" -sTCP:LISTEN -t 2>/dev/null | head -1)
    [ -z "$pid" ] && pid="$launch_pid"

    # Sample RSS + CPU in the background for the full bench window.
    local samples="$OUT/$lang.samples"
    : > "$samples"
    (
        while kill -0 "$pid" 2>/dev/null; do
            # On macOS `ps -o rss=,%cpu= -p PID` -> "rss_kb cpu"
            ps -o rss=,%cpu= -p "$pid" 2>/dev/null >> "$samples" || true
            sleep 0.2
        done
    ) &
    local sampler=$!

    # Drive load.
    ./bin/loadgen -addr "$ADDR" -c "$CONC" -d "$DUR" -warm "$WARM" -pipeline "$PIPE" \
        > "$OUT/$lang.json"

    # Stop sampler then server. Force-kill if needed (detached pthreads etc.).
    kill "$sampler" 2>/dev/null || true
    wait "$sampler" 2>/dev/null || true
    kill -TERM "$pid" "$launch_pid" 2>/dev/null || true
    for _ in 1 2 3 4 5; do kill -0 "$pid" 2>/dev/null || break; sleep 0.1; done
    kill -KILL "$pid" "$launch_pid" 2>/dev/null || true
    wait "$launch_pid" 2>/dev/null || true
    # Also clean any rogue children listening on the port.
    lsof -nP -iTCP:"${ADDR##*:}" -sTCP:LISTEN -t 2>/dev/null | xargs -I {} kill -9 {} 2>/dev/null || true
    sleep 0.3

    # Extract metrics
    local rps reqs p50 p90 p99 errs
    rps=$( grep '^rps='      "$OUT/$lang.json" | cut -d= -f2)
    reqs=$(grep '^requests=' "$OUT/$lang.json" | cut -d= -f2)
    p50=$( grep '^p50_us='   "$OUT/$lang.json" | cut -d= -f2)
    p90=$( grep '^p90_us='   "$OUT/$lang.json" | cut -d= -f2)
    p99=$( grep '^p99_us='   "$OUT/$lang.json" | cut -d= -f2)
    errs=$(grep '^errors='   "$OUT/$lang.json" | cut -d= -f2)

    local peak_kb peak_mb cpu_avg
    peak_kb=$(awk '{if($1>m)m=$1} END{print m+0}' "$samples")
    peak_mb=$(awk -v k="$peak_kb" 'BEGIN{printf "%.1f", k/1024.0}')
    cpu_avg=$(awk '{s+=$2; n++} END{if(n>0) printf "%.1f", s/n; else print 0}' "$samples")

    printf '%-7s | %12.0f | %10s | %6s | %9s | %9s | %9s | %8s | %9s\n' \
        "$lang" "${rps:-0}" "${reqs:-0}" "${errs:-0}" "${p50:-0}" "${p90:-0}" "${p99:-0}" "$peak_mb" "$cpu_avg"
}

for lang in ${LANGS//,/ }; do
    case "$lang" in
        c)     run_one c     "./bin/server-c    127.0.0.1 8080" ;;
        cpp)   run_one cpp   "./bin/server-cpp  127.0.0.1 8080" ;;
        rust)  run_one rust  "./bin/server-rust 127.0.0.1 8080" ;;
        go)    run_one go    "./bin/server-go   127.0.0.1:8080" ;;
        lumen) run_one lumen "./bin/server-lumen" ;;
        java)
            if [ "$SCENARIO" = "static" ]; then
                run_one java "java -cp ./bin ServerStatic 127.0.0.1 8080"
            else
                run_one java "java -cp ./bin ServerDynamic 127.0.0.1 8080"
            fi
            ;;
        csharp)
            if [ "$SCENARIO" = "static" ]; then
                run_one csharp "./bin/server-csharp/static 127.0.0.1 8080"
            else
                run_one csharp "./bin/server-csharp/dynamic 127.0.0.1 8080"
            fi
            ;;
        *) echo "unknown language in LANGS: $lang" >&2; exit 1 ;;
    esac
done

echo
echo "Raw JSON results in $OUT/"
