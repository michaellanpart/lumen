#!/usr/bin/env bash
# benchmarks/run_fair_suite.sh — multi-workload, lower-bias perf suite.
#
# Workloads:
#   - HTTP: uses run_unbiased.sh (repeats + order rotation)
#   - JSON encode: repeated wall-clock timing
#   - Math (fib): repeated wall-clock timing
#   - Sorting (ints): repeated wall-clock timing
#
# Fairness controls:
#   - Repeated runs
#   - Rotated language order per repeat
#   - Median and coefficient of variation (cv%)
#   - Deterministic input + checksum validation where applicable

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

REPEATS="${REPEATS:-5}"
HTTP_REPEATS="${HTTP_REPEATS:-3}"
SCENARIOS="${SCENARIOS:-static}"
PIPES="${PIPES:-1,16}"
CONC="${CONC:-64}"
DUR="${DUR:-5s}"
WARM="${WARM:-1s}"
WORKLOAD_WARMUPS="${WORKLOAD_WARMUPS:-1}"

# Quick-mode: QUICK=1 ./benchmarks/run_fair_suite.sh
if [ "${QUICK:-0}" = "1" ]; then
    HTTP_REPEATS=1; REPEATS=1; PIPES=1; DUR=2s; WARM=0.5s; SCENARIOS=static
fi
OUT="${OUT:-benchmarks/results/fair_suite}"
RUN_HTTP="${RUN_HTTP:-1}"

mkdir -p "$OUT"

HAS_JAVA=0
if command -v javac >/dev/null 2>&1 && command -v java >/dev/null 2>&1; then
    HAS_JAVA=1
fi

HAS_CSHARP=0
DOTNET_BIN="dotnet"
if command -v dotnet >/dev/null 2>&1; then
    HAS_CSHARP=1
elif [ -x "/usr/local/share/dotnet/dotnet" ]; then
    HAS_CSHARP=1
    DOTNET_BIN="/usr/local/share/dotnet/dotnet"
fi

rotate_langs() {
    local csv="$1"
    local shift="$2"
    local IFS=','
    read -r -a arr <<< "$csv"
    local n=${#arr[@]}
    shift=$((shift % n))
    local out=()
    for ((i = 0; i < n; i++)); do
        out+=("${arr[$(((i + shift) % n))]}")
    done
    local joined
    joined=$(IFS=,; echo "${out[*]}")
    echo "$joined"
}

median_sorted_stream() {
    awk '{a[NR]=$1} END{if(NR==0){print "0"; exit} if(NR%2==1) printf "%.4f", a[(NR+1)/2]; else printf "%.4f", (a[NR/2]+a[NR/2+1])/2.0}'
}

stats_mean_cv() {
    awk '{s+=$1; ss+=$1*$1; n++} END{if(n==0){print "0.0000 0.00"; exit} m=s/n; v=(ss/n)-(m*m); if(v<0)v=0; sd=sqrt(v); cv=(m>0? (sd*100.0/m):0); printf "%.4f %.2f", m, cv}'
}

run_timed_cmd() {
    local cmd="$1"
    local out_time="$2"
    /usr/bin/time -p sh -c "$cmd >/dev/null" 2> "$out_time"
    awk '/^real /{print $2}' "$out_time"
}

workload_cmd() {
    local workload="$1"
    local lang="$2"
    case "$workload:$lang" in
        json:c) echo "./bin/json-c" ;;
        json:rust) echo "./bin/json-rust" ;;
        json:go) echo "./bin/json-go" ;;
        json:lumen) echo "./bin/json-lumen" ;;
        json:csharp) echo "./bin/json-csharp/json" ;;
        json:java) echo "java -cp ./bin JsonEncode" ;;
        fib:c) echo "./bin/fib-c" ;;
        fib:rust) echo "./bin/fib-rust" ;;
        fib:go) echo "./bin/fib-go" ;;
        fib:java) echo "java -cp ./bin Fib" ;;
        fib:lumen) echo "./bin/fib-lumen" ;;
        fib:csharp) echo "./bin/fib-csharp/fib" ;;
        sort:c) echo "./bin/sort-c" ;;
        sort:rust) echo "./bin/sort-rust" ;;
        sort:go) echo "./bin/sort-go" ;;
        sort:java) echo "java -cp ./bin SortInts" ;;
        sort:lumen) echo "./bin/sort-lumen" ;;
        sort:csharp) echo "./bin/sort-csharp/sort" ;;
        *)
            echo "unsupported workload/lang: $workload/$lang" >&2
            return 1
            ;;
    esac
}

echo "==> Building common toolchain"
go build -o bin/lumen ./cmd/lumen

echo "==> Building JSON workloads"
go build -o bin/json-go ./benchmarks/programs/gojson
cc -O2 -o bin/json-c benchmarks/programs/json_encode.c
rustc -O -o bin/json-rust benchmarks/programs/json_encode.rs 2>/dev/null
./bin/lumen build benchmarks/programs/json_encode.lm -o bin/json-lumen 2>/dev/null
if [ "$HAS_JAVA" = "1" ]; then
    javac -d bin benchmarks/programs/JsonEncode.java
fi
if [ "$HAS_CSHARP" = "1" ]; then
    "$DOTNET_BIN" publish -c Release -o bin/json-csharp benchmarks/programs/csharp/json/json.csproj >/dev/null
fi

echo "==> Building math workloads (fib)"
go build -o bin/fib-go ./benchmarks/programs/gofib
cc -O2 -o bin/fib-c benchmarks/programs/fib.c
rustc -O -o bin/fib-rust benchmarks/programs/fib.rs 2>/dev/null
./bin/lumen build benchmarks/programs/fib.lm -o bin/fib-lumen 2>/dev/null
if [ "$HAS_JAVA" = "1" ]; then
    javac -d bin benchmarks/programs/Fib.java
fi
if [ "$HAS_CSHARP" = "1" ]; then
    "$DOTNET_BIN" publish -c Release -o bin/fib-csharp benchmarks/programs/csharp/fib/fib.csproj >/dev/null
fi

echo "==> Building sort workloads"
go build -o bin/sort-go ./benchmarks/programs/gosort
cc -O2 -o bin/sort-c benchmarks/programs/sort_ints.c
rustc -O -o bin/sort-rust benchmarks/programs/sort_ints.rs 2>/dev/null
./bin/lumen build benchmarks/programs/sort_ints.lm -o bin/sort-lumen 2>/dev/null
if [ "$HAS_JAVA" = "1" ]; then
    javac -d bin benchmarks/programs/SortInts.java
fi
if [ "$HAS_CSHARP" = "1" ]; then
    "$DOTNET_BIN" publish -c Release -o bin/sort-csharp benchmarks/programs/csharp/sort/sort.csproj >/dev/null
fi

FAIR_WORKLOAD_LANGS="c,rust,go,lumen"
FAIR_HTTP_ONLY="${ONLY:-c,cpp,rust,go,lumen}"

if [ "$HAS_JAVA" = "1" ]; then
    FAIR_WORKLOAD_LANGS="$FAIR_WORKLOAD_LANGS,java"
    FAIR_HTTP_ONLY="${FAIR_HTTP_ONLY%,},java"
else
    echo "==> Java toolchain not found (javac/java); running fair suite without java"
fi

if [ "$HAS_CSHARP" = "1" ]; then
    FAIR_WORKLOAD_LANGS="$FAIR_WORKLOAD_LANGS,csharp"
    FAIR_HTTP_ONLY="${FAIR_HTTP_ONLY%,},csharp"
else
    echo "==> dotnet not found; running fair suite without csharp"
fi

echo
printf '%-10s | %-7s | %10s | %10s | %7s\n' workload lang median_s mean_s cv_pct
printf '%s\n' "-----------+---------+------------+------------+--------"

# HTTP workload (throughput-style). Results kept in a nested folder.
if [ "$RUN_HTTP" = "1" ]; then
    HTTP_OUT="$OUT/http"
    mkdir -p "$HTTP_OUT"
    set +e
    SCENARIOS="$SCENARIOS" PIPES="$PIPES" REPEATS="$HTTP_REPEATS" CONC="$CONC" DUR="$DUR" WARM="$WARM" \
    ONLY="$FAIR_HTTP_ONLY" LANGS="$FAIR_HTTP_ONLY" OUT="$HTTP_OUT" ./benchmarks/run_unbiased.sh | tee "$HTTP_OUT/summary.log"
    http_rc=$?
    set -e
    if [ "$http_rc" -ne 0 ]; then
        echo "WARNING: HTTP benchmark stage failed (rc=$http_rc); continuing with json/fib/sort workloads"
    fi
else
    echo "==> Skipping HTTP workload (RUN_HTTP=$RUN_HTTP)"
fi

bench_workload() {
    local workload="$1"
    local langs="$2"
    shift 2

    local case_dir="$OUT/$workload"
    mkdir -p "$case_dir"

    local first_chk=""

    if [ "$WORKLOAD_WARMUPS" -gt 0 ]; then
        for lang in ${langs//,/ }; do
            cmd=$(workload_cmd "$workload" "$lang")
            for _ in $(seq 1 "$WORKLOAD_WARMUPS"); do
                sh -c "$cmd >/dev/null"
            done
        done
    fi

    for r in $(seq 1 "$REPEATS"); do
        local order
        order=$(rotate_langs "$langs" $((r - 1)))
        for lang in ${order//,/ }; do
            local vals_file="$case_dir/$lang.s"
            local chk_file="$case_dir/$lang.chk"
            mkdir -p "$case_dir/r${r}"

            local cmd
            cmd=$(workload_cmd "$workload" "$lang")

            t=$(run_timed_cmd "$cmd" "$case_dir/r${r}/${lang}.time")
            echo "$t" >> "$vals_file"

            # Record output checksum to ensure each impl computed same result.
            sh -c "$cmd" | shasum | awk '{print $1}' > "$case_dir/r${r}/${lang}.sha"
            sha=$(cat "$case_dir/r${r}/${lang}.sha")
            echo "$sha" > "$chk_file"
            if [ -z "$first_chk" ]; then
                first_chk="$sha"
            fi
            if [ "$sha" != "$first_chk" ]; then
                echo "checksum mismatch in workload=$workload repeat=$r lang=$lang" >&2
                exit 1
            fi
        done
    done

    for lang in ${langs//,/ }; do
        vals_file="$case_dir/$lang.s"
        [ -s "$vals_file" ] || continue
        med=$(sort -n "$vals_file" | median_sorted_stream)
        stats=$(stats_mean_cv < "$vals_file")
        mean=$(echo "$stats" | awk '{print $1}')
        cv=$(echo "$stats" | awk '{print $2}')
        printf '%-10s | %-7s | %10s | %10s | %7s\n' "$workload" "$lang" "$med" "$mean" "$cv"
    done
}

bench_workload json "$FAIR_WORKLOAD_LANGS"
bench_workload fib "$FAIR_WORKLOAD_LANGS"
bench_workload sort "$FAIR_WORKLOAD_LANGS"

echo
echo "Artifacts: $OUT"
python3 ./benchmarks/render_fair_report.py --input "$OUT" --output "$OUT/report.html"
echo "Visual report: $OUT/report.html"
