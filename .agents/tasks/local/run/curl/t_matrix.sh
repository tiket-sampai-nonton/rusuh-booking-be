#!/usr/bin/env bash
# Contract-matrix pass (Batch 4): every response documented in docs/contracts/openapi/*.yaml is driven with real HTTP and compared
# (status + errorCode) with the contract. Needs python3 + requests + cryptography. ~15 minutes (real timers and rate-limit waits).
# Parallel parts first; outage (stops one local process at a time and restores it) and rate-limit parts run alone afterwards.
# Outputs: .agents/tasks/fix/contract-matrix/{matrix.tsv,undocumented.tsv,report.md}
set -uo pipefail
cd "$(dirname "$0")/matrix"
./run_matrix.sh
python3 build_matrix.py -v
python3 analyze_logs.py
python3 make_report.py
