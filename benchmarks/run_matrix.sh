#!/usr/bin/env bash
# benchmarks/run_matrix.sh — Sweep HTTP scenarios and pipeline depths.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

SCENARIOS="${SCENARIOS:-static,dynamic}"
PIPES="${PIPES:-1,16,64}"
CONC="${CONC:-64}"
DUR="${DUR:-10s}"
WARM="${WARM:-1s}"
ONLY="${ONLY:-c,cpp,rust,go,lumen}"

mkdir -p benchmarks/results/matrix

echo "==> Matrix run"
echo "scenarios=$SCENARIOS pipes=$PIPES conc=$CONC dur=$DUR warm=$WARM only=$ONLY"

for scenario in ${SCENARIOS//,/ }; do
    for pipe in ${PIPES//,/ }; do
        echo
        echo "### scenario=$scenario pipeline=$pipe"
        log="benchmarks/results/matrix/${scenario}_pipe${pipe}.log"
        SCENARIO="$scenario" PIPE="$pipe" CONC="$CONC" DUR="$DUR" WARM="$WARM" ONLY="$ONLY" \
            ./benchmarks/run.sh | tee "$log"
    done
done

echo
echo "Matrix logs: benchmarks/results/matrix"
