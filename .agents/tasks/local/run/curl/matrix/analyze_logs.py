#!/usr/bin/env python3
"""Read the service request logs written after the pass started, join `request` and `request failed` lines by requestId, and
(1) cross-check that every recorded requestId really appears in the logs with the recorded status,
(2) list responses the code returned that the contract does not document (method + route + status [+ errorCode])."""
import os, sys, json, glob, collections, datetime, subprocess
HERE = os.path.dirname(os.path.abspath(__file__))
LOG = os.path.join(HERE, '..', '..', 'log')
OUT = os.path.abspath(os.path.join(HERE, '../../../../fix/contract-matrix'))
start = open(os.path.join(OUT, 'pass-start.txt')).read().strip()
st_local = (datetime.datetime.fromisoformat(start.replace('Z', '+00:00')) + datetime.timedelta(hours=7)).strftime('%Y-%m-%dT%H:%M:%S')
FILESVC = {'booking.yaml': 'booking', 'internal-booking.yaml': 'booking', 'internal-payment.yaml': 'payment', 'notification.yaml': 'notification',
           'payment.yaml': 'payment', 'queue.yaml': 'queue', 'user.yaml': 'user'}
rows = [l.split('\t') for l in subprocess.check_output([sys.executable, os.path.join(HERE, '..', 'gen_matrix.py')]).decode().splitlines()]
doc = collections.defaultdict(set)      # (svc, METHOD, route) -> {(status, code)}
for rid, f, m, tpl, st, code, desc in rows:
    doc[(FILESVC[f], m, tpl)].add((int(st), code))
reqs = {}                                # requestId -> dict
fails = {}
for svc in ['booking', 'queue', 'payment', 'notification', 'user']:
    p = os.path.join(LOG, f'{svc}-api.log')
    with open(p, errors='replace') as fh:
        for line in fh:
            if not line.startswith('{'):
                continue
            if line[9:28] < st_local[:19] and '"time":"' + st_local[:10] in line[:20]:
                pass
            try:
                d = json.loads(line)
            except Exception:
                continue
            t = d.get('time', '')[:19]
            if t < st_local:
                continue
            if d.get('msg') == 'request':
                d['svc'] = svc; reqs[d['requestId']] = d
            elif d.get('msg') == 'request failed':
                fails[d.get('requestId')] = d.get('errorCode', '')
tot = len(reqs)
# (1) cross-check recorded rids
rec_total = rec_found = rec_status_ok = 0
bad = []
for f in glob.glob(os.path.join(HERE, '..', 'results', 'matrix', '*.jsonl')):
    for l in open(f):
        r = json.loads(l)
        if not r.get('rid') or r['name'].startswith(('setup', 'login', 'admin-login')):
            continue
        rec_total += 1
        d = reqs.get(r['rid'])
        if d:
            rec_found += 1
            if d['status'] == r['status']:
                rec_status_ok += 1
            else:
                bad.append((r['name'], r['rid'], r['status'], d['status']))
# (2) undocumented
cnt = collections.Counter(); ex = {}; unmatched = collections.Counter()
for rid, d in reqs.items():
    route = d.get('route') or ''
    if route in ('/health', '/metrics'):
        continue
    if route == '/internal/v1/allocations/{rest}':   # the mux pattern label of the release route
        route = '/internal/v1/allocations/{allocationId}:release'
    if route == 'unmatched':                       # 401 from the visitor middleware, which runs before routing
        unmatched[(d['svc'], d.get('method'), d['status'], fails.get(rid, ''))] += 1
        continue
    code = fails.get(rid, '')
    key = (d['svc'], d.get('method'), route, d['status'], code)
    cnt[key] += 1; ex.setdefault(key, rid)
und_status, und_code = [], []
for (svc, m, route, status, code), n in sorted(cnt.items()):
    docs = doc.get((svc, m, route))
    if status < 400 and status in (200, 201, 202, 204) and docs and any(s == status for s, _ in docs):
        continue
    if docs is None:
        und_status.append((svc, m, route, status, code, n, ex[(svc, m, route, status, code)], 'route not in the contract'))
    elif not any(s == status for s, _ in docs):
        und_status.append((svc, m, route, status, code, n, ex[(svc, m, route, status, code)], 'status not documented for this route'))
    else:
        okc = {c for s, c in docs if s == status}
        if '' not in okc and code not in okc:
            und_code.append((svc, m, route, status, code, n, ex[(svc, m, route, status, code)], 'status documented, errorCode differs (documented: %s)' % ','.join(sorted(okc))))
with open(os.path.join(OUT, 'undocumented.tsv'), 'w') as fh:
    fh.write('service\tmethod\troute\tstatus\terrorCode\tcount\texampleRequestId\tnote\n')
    for r in und_status + und_code:
        fh.write('\t'.join(map(str, r)) + '\n')
print(f'log requests after {start}: {tot}; recorded rids {rec_total}, found in logs {rec_found}, status equal {rec_status_ok}')
for b in bad[:20]:
    print('  rid/status mismatch:', b)
print('requests logged with route=unmatched (visitor middleware 401 before routing):', dict(unmatched))
print('undocumented (status/route):', len(und_status), ' (errorCode only):', len(und_code))
for r in und_status + und_code:
    print(' ', *r[:6], r[7])
json.dump(dict(unmatched={'|'.join(map(str,k)): v for k, v in unmatched.items()}, requests=tot, recorded=rec_total, found=rec_found, status_equal=rec_status_ok), open(os.path.join(OUT, 'log-crosscheck.json'), 'w'))
