#!/usr/bin/env bash
# Full contract-matrix pass (Batch 4). Parallel parts first, then the isolation-sensitive parts one at a time.
#   parallel : p1 booking public, p2 queue, p3 payment, p4 user+notification, p5 internal booking, p7 slow (real timers), p9 temp instances
#   alone    : p6 outages (stops one process at a time and restores it), p8 rate limits (exhausts shared per-IP budgets), p0 health
# Then: python3 build_matrix.py
set -uo pipefail
cd "$(dirname "$0")"
OUT=../results; mkdir -p $OUT/matrix; rm -f $OUT/matrix/*.jsonl $OUT/matrix_*.log
date -u +%FT%TZ > ../../../../fix/contract-matrix/pass-start.txt
for p in p1_booking_public p2_queue p3_payment p4_user_notif p5_internal_booking p7_slow p9_temp_instances; do
  PART=${p%%_*} python3 -u $p.py >$OUT/matrix_${p%%_*}.log 2>&1 &
done
wait
PART=p6 python3 -u p6_outage.py >$OUT/matrix_p6.log 2>&1
PART=p8 python3 -u p8_ratelimit.py >$OUT/matrix_p8.log 2>&1
PART=p0 python3 -u p0_health.py >$OUT/matrix_p0.log 2>&1
grep -h '^FAIL' $OUT/matrix_*.log | sort | uniq -c | sort -rn | head -100
