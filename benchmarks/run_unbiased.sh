#!/usr/bin/env bash
# benchmarks/run_unbiased.sh — lower-bias benchmark harness.
#
# What this does to reduce bias:
# 1) Repeats each case N times.
# 2) Rotates language execution order each repeat.
# 3) Stores per-repeat artifacts for auditing.
# 4) Reports median and coefficient of variation (cv%).

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

SCENARIOS="${SCENARIOS:-static,dynamic}"
PIPES="${PIPES:-1,16,64}"
REPEATS="${REPEATS:-5}"
CONC="${CONC:-64}"
DUR="${DUR:-10s}"
WARM="${WARM:-1s}"
ONLY="${ONLY:-c,cpp,rust,go,lumen}"
LANGS_BASE="${LANGS:-c,cpp,rust,go,lumen}"
OUT="${OUT:-benchmarks/results/unbiased}"

mkdir -p "$OUT"

rotate_langs() {
    local csv="$1"
    local shift="$2"
    local IFS=','
    read -r -a arr <<< "$csv"
    local n=${#arr[@]}
    if [ "$n" -eq 0 ]; then
        echo "$csv"
        return
    fi
    shift=$((shift % n))
    local out=()
    for ((i = 0; i < n; i++)); do
        out+=("${arr[$(((i + shift) % n))]}")
    done
    local joined
    joined=$(IFS=,; echo "${out[*]}")
    echo "$joined"
}

median() {
    awk '{a[NR]=$1} END{if(NR==0){print "0"; exit} if(NR%2==1) printf "%.2f", a[(NR+1)/2]; else printf "%.2f", (a[NR/2]+a[NR/2+1])/2.0}'
}

stats_mean_cv() {
    awk '{s+=$1; ss+=$1*$1; n++} END{if(n==0){print "0.00 0.00"; exit} m=s/n; v=(ss/n)-(m*m); if(v<0)v=0; sd=sqrt(v); cv=(m>0? (sd*100.0/m):0); printf "%.2f %.2f", m, cv}'
}

echo "==> Unbiased benchmark run"
echo "scenarios=$SCENARIOS pipes=$PIPES repeats=$REPEATS conc=$CONC dur=$DUR warm=$WARM"

echo
printf '%-8s | %-4s | %-7s | %11s | %11s | %8s\n' scenario pipe lang median_rps mean_rps cv_pct
printf '%s\n' "---------+------+---------+-------------+-------------+---------"

for scenario in ${SCENARIOS//,/ }; do
    for pipe in ${PIPES//,/ }; do
        case_dir="$OUT/${scenario}_pipe${pipe}"
        mkdir -p "$case_dir"

        for r in $(seq 1 "$REPEATS"); do
            langs_run=$(rotate_langs "$LANGS_BASE" $((r - 1)))
            run_dir="$case_dir/r${r}"
            mkdir -p "$run_dir"

            echo
            echo "### scenario=$scenario pipe=$pipe repeat=$r langs=$langs_run"

            SCENARIO="$scenario" PIPE="$pipe" CONC="$CONC" DUR="$DUR" WARM="$WARM" \
            ONLY="$ONLY" LANGS="$langs_run" ./benchmarks/run.sh | tee "$run_dir/run.log"

            for lang in ${ONLY//,/ }; do
                cp -f "benchmarks/results/$scenario/$lang.json" "$run_dir/$lang.json" 2>/dev/null || true
                cp -f "benchmarks/results/$scenario/$lang.samples" "$run_dir/$lang.samples" 2>/dev/null || true
            done
        done

        for lang in ${ONLY//,/ }; do
            vals_file="$case_dir/$lang.rps"
            : > "$vals_file"
            for r in $(seq 1 "$REPEATS"); do
                f="$case_dir/r${r}/$lang.json"
                [ -f "$f" ] || continue
                grep '^rps=' "$f" | cut -d= -f2 >> "$vals_file" || true
            done

            if [ ! -s "$vals_file" ]; then
                continue
            fi

            med=$(sort -n "$vals_file" | median)
            stats_line=$(stats_mean_cv < "$vals_file")
            mean=$(echo "$stats_line" | awk '{print $1}')
            cv=$(echo "$stats_line" | awk '{print $2}')
            printf '%-8s | %-4s | %-7s | %11s | %11s | %8s\n' "$scenario" "$pipe" "$lang" "$med" "$mean" "$cv"
        done
    done
done

echo
echo "Unbiased artifacts: $OUT"
