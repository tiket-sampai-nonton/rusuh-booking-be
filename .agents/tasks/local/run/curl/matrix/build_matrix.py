#!/usr/bin/env python3
"""Join the regenerated contract matrix with the recorded calls (results/matrix/*.jsonl) -> matrix.tsv + summary.json."""
import os, sys, json, glob, subprocess, collections
HERE = os.path.dirname(os.path.abspath(__file__))
OUT = os.path.abspath(os.path.join(HERE, '../../../../fix/contract-matrix'))
rows = [l.rstrip('\n').split('\t') for l in subprocess.check_output([sys.executable, os.path.join(HERE, '..', 'gen_matrix.py')]).decode().splitlines()]
FILESVC = {'booking.yaml': 'booking', 'internal-booking.yaml': 'booking', 'internal-payment.yaml': 'payment', 'notification.yaml': 'notification',
           'payment.yaml': 'payment', 'queue.yaml': 'queue', 'user.yaml': 'user'}
recs = []
for f in sorted(glob.glob(os.path.join(HERE, '..', 'results', 'matrix', '*.jsonl'))):
    for l in open(f):
        try:
            r = json.loads(l)
        except Exception:
            continue
        r['file'] = os.path.basename(f)
        recs.append(r)
# manual verdicts for rows that cannot be (or were not) driven by curl: key = rowid -> (verdict, reason)
MAN = {}
mp = os.path.join(OUT, 'manual.tsv')
if os.path.exists(mp):
    for l in open(mp):
        if l.strip() and not l.startswith('#'):
            p = l.rstrip('\n').split('\t')
            MAN[p[0]] = (p[1], p[2])
out, summary = [], collections.defaultdict(lambda: collections.Counter())
for (rid, f, m, tpl, st, code, desc) in rows:
    svc = FILESVC[f]
    hits = [r for r in recs if r['method'] == m and r['tpl'] == tpl and r['svc'] == svc and str(r['status']) == st and (code == '' or r['code'] == code)]
    # rows documented without a code: also require that no code is promised (any code accepted)
    intended = [r for r in recs if r['method'] == m and r['tpl'] == tpl and r['svc'] == svc and r.get('want_status') == int(st)
                and (r.get('want_code') or '') == code and not r['name'].startswith(('setup', 'login', 'admin-login'))]
    if hits:
        h = hits[0]
        verdict, ev = 'PASS', f"{h['name']} [{h['file'].replace('.jsonl','')}] rid={h['rid']}"
    elif rid in MAN:
        verdict, ev = MAN[rid]
        ev = ev
    elif intended:
        seen = sorted({(r['status'], r['code']) for r in intended})
        verdict = 'MISMATCH'
        ev = 'observed ' + '; '.join(f"{s} {c}" for s, c in seen) + ' via ' + intended[0]['name'] + f" rid={intended[0]['rid']}"
    else:
        verdict, ev = 'NOT_EXERCISED', ''
    out.append((rid, f, m, tpl, st, code, verdict, ev, desc))
    summary[f][verdict.split(':')[0]] += 1
with open(os.path.join(OUT, 'matrix.tsv'), 'w') as fh:
    fh.write('id\tcontract\tmethod\tpath\tstatus\terrorCode\tresult\tevidence\n')
    for r in out:
        fh.write('\t'.join(r[:8]) + '\n')
json.dump({k: dict(v) for k, v in summary.items()}, open(os.path.join(OUT, 'summary.json'), 'w'), indent=1)
tot = collections.Counter(r[6].split(':')[0] for r in out)
print('rows', len(out), dict(tot))
for f, c in sorted(summary.items()):
    print(f'{f:24s}', dict(c))
if '-v' in sys.argv:
    for r in out:
        if r[6] != 'PASS':
            print(r[0], r[1], r[2], r[3], r[4], r[5], '=>', r[6], r[7][:140])
