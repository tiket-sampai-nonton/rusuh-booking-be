"""Shared helpers for the contract-matrix pass (Batch 4). Real HTTP against the running local stack only.
Every call is recorded in results/matrix/<part>.jsonl with the contract row it targets (method, path template,
expected status + errorCode) and what was observed (status, errorCode, X-Request-Id, a masked curl reproduction)."""
import os, sys, json, time, uuid, re, random, threading, base64, subprocess, datetime
import requests
from cryptography.hazmat.primitives import serialization

HERE = os.path.dirname(os.path.abspath(__file__))
RUN = os.path.abspath(os.path.join(HERE, '..', '..'))
ROOT = os.path.abspath(os.path.join(RUN, '..', '..', '..', '..'))
KEYS = os.path.join(ROOT, 'deployments', 'compose', 'keys')
OUTDIR = os.path.join(HERE, '..', 'results', 'matrix')
os.makedirs(OUTDIR, exist_ok=True)
PORT = dict(queue=8081, booking=8082, payment=8083, notification=8084, user=8085)
BASE = {k: f'http://localhost:{v}' for k, v in PORT.items()}
FAKE = 'http://localhost:18089'
MAILPIT = 'http://localhost:8025'
PART = os.environ.get('PART', 'part')
import http.cookiejar
class _NoCookies(http.cookiejar.CookiePolicy):
    return_ok = set_ok = domain_return_ok = path_return_ok = lambda *a, **k: False
    netscape = True; rfc2965 = hide_cookie2 = False
SESS = requests.Session()
SESS.cookies.set_policy(_NoCookies())
_lock = threading.Lock()
_f = open(os.path.join(OUTDIR, PART + '.jsonl'), 'w')
UUID0 = '00000000-0000-0000-0000-000000000000'


def now_iso():
    return datetime.datetime.now(datetime.timezone.utc).strftime('%Y-%m-%dT%H:%M:%SZ')


def mask(v):
    return re.sub(r'(eyJ[A-Za-z0-9_-]{6})[A-Za-z0-9_.-]+', r'\1…', v)


class R:
    """Result of a call."""
    def __init__(self, resp, rec):
        self.resp, self.rec = resp, rec
        self.status = resp.status_code
        try:
            self.json = resp.json()
        except Exception:
            self.json = None
        self.code = ''
        if isinstance(self.json, dict) and isinstance(self.json.get('meta'), dict):
            m = self.json['meta'].get('message') or ''
            mm = re.match(r'^([A-Z][A-Z0-9_]+)(:|$)', m)
            self.code = mm.group(1) if mm else ''
        self.data = (self.json or {}).get('data') if isinstance(self.json, dict) else None
        self.ok = rec.get('ok')

    def d(self, *path, default=None):
        v = self.data
        for k in path:
            if isinstance(v, dict):
                v = v.get(k)
            elif isinstance(v, list) and isinstance(k, int) and k < len(v):
                v = v[k]
            else:
                return default
            if v is None:
                return default
        return v


def call(method, svc, tpl, p=None, *, want=None, name='', headers=None, json_body=None, data=None, files=None,
         params=None, raw=None, timeout=30, cookies=None, row_tpl=None, quiet=False, as_svc=None):
    """want=(status, code) is the contract row this call targets; code '' means 'any / none documented'.
    tpl is the contract path template (with {params}); p fills it."""
    path = tpl
    for k, v in (p or {}).items():
        path = path.replace('{' + k + '}', str(v))
    base = BASE.get(svc, svc)
    url = base + path
    h = dict(headers or {})
    if raw is not None:
        body = raw
    elif json_body is not None:
        body = json.dumps(json_body)
        h.setdefault('Content-Type', 'application/json')
    else:
        body = data
    t0 = time.time()
    try:
        resp = SESS.request(method, url, headers=h, data=body, files=files, params=params, timeout=timeout, cookies=cookies)
    except Exception as e:
        rec = dict(part=PART, name=name, svc=as_svc or svc, method=method, tpl=row_tpl or tpl, want_status=want[0] if want else None,
                   want_code=want[1] if want else None, status=0, code='NETERR', rid='', ok=False, err=str(e)[:200], ts=time.time())
        with _lock:
            _f.write(json.dumps(rec) + '\n'); _f.flush()
        print('NETERR', name, method, url, e)
        raise
    rid = resp.headers.get('X-Request-Id', '')
    # curl reproduction (tokens masked)
    c = ['curl -s -i -X', method, f"'{base}{path}'"]
    for k, v in h.items():
        c.append(f"-H '{k}: {mask(v)}'")
    if json_body is not None:
        c.append("-d '" + mask(body)[:300] + "'")
    elif files:
        c.append('-F file=@<file>')
    if params:
        c[2] = f"'{base}{path}?" + '&'.join(f'{k}={v}' for k, v in params.items()) + "'"
    curl = ' '.join(c)
    rec = dict(part=PART, name=name, svc=as_svc or svc, method=method, tpl=row_tpl or tpl, want_status=None, want_code=None,
               status=resp.status_code, code='', rid=rid, curl=curl, ts=t0)
    r = R(resp, rec)
    rec['code'] = r.code
    ok = True
    if want:
        rec['want_status'], rec['want_code'] = want
        ok = (r.status == want[0]) and (want[1] in ('', None) or r.code == want[1])
    rec['ok'] = ok
    r.ok = ok
    with _lock:
        _f.write(json.dumps(rec) + '\n'); _f.flush()
    if not quiet:
        print(('PASS ' if ok else 'FAIL ') + f"[{PART}] {name or method + ' ' + tpl}: got {r.status} {r.code}" + ('' if ok else f" want {want}"))
    return r


def note(*a):
    print('      ', *a)


# ---------------------------------------------------------------- identities / data
_nik_ctr = [0]
_nik_base = random.randint(1, 28)
_run = int(time.time()) % 5000


def nik():
    """Structurally valid, unique-per-process NIK (province 31, Jakarta Selatan 7401, born DDMMYY)."""
    with _lock:
        _nik_ctr[0] += 1
        n = _nik_ctr[0]
    a = (int(time.time() * 1000) + n * 7919 + os.getpid() * 31) % 100000000
    return '317401%02d%02d%02d%04d' % ((a // 10000) % 28 + 1, (a // 100) % 12 + 1, 60 + a % 39, (n * 37 + a) % 10000)


def uid():
    return str(uuid.uuid4())


def key():
    return ('k-%s-%d-%d' % (PART, time.time_ns(), random.randint(0, 99999)))[:60]


def mail_otp(email):
    for _ in range(40):
        r = SESS.get(f'{MAILPIT}/api/v1/search', params={'query': 'to:' + email}).json()
        if r.get('messages'):
            mid = r['messages'][0]['ID']
            txt = SESS.get(f'{MAILPIT}/api/v1/message/{mid}').json()['Text']
            return re.search(r'\b(\d{6})\b', txt).group(1)
        time.sleep(0.3)
    raise RuntimeError('no OTP mail for ' + email)


def login(email, name='login'):
    """Returns (access, refresh); not recorded as matrix rows' attempts unless names are given (they are rows anyway)."""
    r1 = call('POST', 'user', '/api/v1/auth/otp/request', json_body={'email': email}, want=(202, ''), name=name + ' otp/request', quiet=True)
    code = mail_otp(email)
    r2 = call('POST', 'user', '/api/v1/auth/otp/verify', json_body={'email': email, 'code': code}, want=(200, ''), name=name + ' otp/verify', quiet=True)
    return r2.d('accessToken'), r2.d('refreshToken'), r2


_admin = {}


def admin_token():
    t = _admin.get('t')
    if t and time.time() - _admin['at'] < 600:
        return t
    at, rt, _ = login('admin-%d%d@admin.sim.test' % (os.getpid(), random.randint(0, 99999)), 'admin-login')
    _admin.update(t=at, at=time.time())
    return at


def AH(tok=None):
    return {'Authorization': 'Bearer ' + (tok or admin_token())}


class Event:
    pass


def new_event(name, vip=10, reg=10, extra=None, sales_start='-1 minute', capacity=60, publish=True, queue=True):
    e = Event()
    extra = extra or {}
    def iso(delta):
        out = subprocess.check_output(['date', '-u', '-d', delta, '+%FT%TZ']).decode().strip()
        return out
    body = {
        'name': name, 'slug': '%s-%d' % (PART, time.time_ns()), 'venue': 'GBK Madya', 'city': 'Jakarta', 'genres': ['pop'],
        'startsAt': iso('+40 days'), 'salesStartsAt': iso(sales_start), 'salesEndsAt': iso('+7 days'),
        'maxTicketsPerOrder': 3, 'maxTicketsPerBuyerNik': 3, 'allowMixedCategories': True,
        'description': [{'type': 'paragraph', 'children': [{'text': name}]}],
        'categories': [{'code': 'VIP', 'name': 'VIP', 'priceMinor': 250000000, 'capacity': vip},
                       {'code': 'REG', 'name': 'Regular', 'priceMinor': 90000000, 'capacity': reg}]}
    body.update(extra)
    r = call('POST', 'booking', '/internal/v1/admin/events', headers=AH(), json_body=body, want=(201, ''), name='setup create event', quiet=True)
    e.id = r.d('eventId')
    if publish:
        call('POST', 'booking', '/internal/v1/admin/events/{eventId}/publish', {'eventId': e.id}, headers=AH(), want=(200, ''), name='setup publish', quiet=True)
        if queue:
            call('PUT', 'queue', '/internal/v1/admin/events/{eventId}/queue-settings', {'eventId': e.id}, headers=AH(),
                 json_body={'activeSlotCapacity': capacity}, want=(0, ''), name='setup queue-settings', quiet=True)
    g = call('GET', 'booking', '/internal/v1/admin/events/{eventId}', {'eventId': e.id}, headers=AH(), want=(200, ''), name='setup get event', quiet=True)
    cats = {c['code']: c['categoryId'] for c in (g.d('categories') or [])}
    e.vip, e.reg = cats.get('VIP'), cats.get('REG')
    return e


class Buyer:
    """A mobile visitor: token in body + X-Device-Id (like lib.sh)."""
    def __init__(self, ev, tag, bearer=None):
        self.ev, self.tag, self.bearer = ev, tag, bearer
        self.dev = 'dev-%s-%s-%d' % (PART, tag, random.randint(0, 10**6))
        self.vt = self.entry = self.sid = self.bid = self.adm = None
        self.nikb = nik()

    def vh(self, extra=None):
        h = {'X-Visitor-Token': self.vt or '', 'X-Device-Id': self.dev}
        if self.bearer:
            h['Authorization'] = 'Bearer ' + self.bearer
        h.update(extra or {})
        return h

    def join(self, want=(201, ''), name='setup join', quiet=True, extra_h=None):
        h = {'X-Client-Type': 'mobile', 'X-Device-Id': self.dev, 'Idempotency-Key': key()}
        if self.bearer:
            h['Authorization'] = 'Bearer ' + self.bearer
        h.update(extra_h or {})
        r = call('POST', 'queue', '/api/v1/events/{eventId}/queue-entries', {'eventId': self.ev.id}, headers=h, want=want, name=name, quiet=quiet)
        if r.status in (200, 201):
            self.vt = r.d('visitorToken') or self.vt
            self.entry = r.d('entryId')
        return r

    def status(self):
        r = call('GET', 'queue', '/api/v1/queue-entries/me', headers=self.vh(), params={'eventId': self.ev.id}, want=(0, ''), name='setup me', quiet=True)
        return r.d('status') or ''

    def wait_admitted(self, n=60):
        for _ in range(n):
            if self.status() == 'ADMITTED':
                return True
            time.sleep(0.25)
        return False

    def claim(self, want=(200, ''), name='setup claim', quiet=True):
        r = call('POST', 'queue', '/api/v1/queue-entries/{entryId}/claim', {'entryId': self.entry}, headers=self.vh(), params={'eventId': self.ev.id}, want=want, name=name, quiet=quiet)
        self.adm = r.d('admissionToken')
        return r

    def session(self, want=(201, ''), name='setup session', quiet=True, token=None):
        r = call('POST', 'booking', '/api/v1/booking-sessions', headers=self.vh(), json_body={'admissionToken': token or self.adm}, want=want, name=name, quiet=quiet)
        if r.status == 201:
            self.sid = r.d('sessionId')
        return r

    def admit(self):
        self.join()
        assert self.wait_admitted(), 'not admitted ' + self.tag
        self.claim()
        self.session()
        return self

    def items(self, cat, n):
        return [{'categoryId': cat, 'attendee': {'nik': nik(), 'fullName': 'Pemegang %s-%d' % (self.tag, i)}} for i in range(n)]

    def body(self, items, **kw):
        b = {'sessionId': self.sid, 'buyer': {'nik': self.nikb, 'email': 'buyer-%s-%s@sim.test' % (PART, self.tag),
             'emailConfirmation': 'buyer-%s-%s@sim.test' % (PART, self.tag), 'consent': True}, 'items': items}
        b.update(kw)
        return b

    def hold(self, cat, n, want=(201, ''), name='setup hold', quiet=True, k=None, body=None):
        r = call('POST', 'booking', '/api/v1/bookings', headers=self.vh({'Idempotency-Key': k or key()}),
                 json_body=body or self.body(self.items(cat, n)), want=want, name=name, quiet=quiet)
        if r.status == 201:
            self.bid = r.d('bookingId')
            self.hold_items = r.d('items')
            self.version = r.d('version')
        return r

    def pay(self, method='QRIS', want=(201, ''), name='setup attempt', quiet=True, k=None):
        r = call('POST', 'payment', '/api/v1/payments/{bookingId}/attempts', {'bookingId': self.bid}, headers=self.vh({'Idempotency-Key': k or key()}),
                 json_body={'method': method}, want=want, name=name, quiet=quiet)
        ref = self.booking().d('publicRef')
        self.order = '%s-%s' % (ref, r.d('attemptNo'))
        return r

    def booking(self, quiet=True):
        return call('GET', 'booking', '/api/v1/bookings/{bookingId}', {'bookingId': self.bid}, headers=self.vh(), want=(0, ''), name='setup get booking', quiet=True)

    def bstatus(self):
        return self.booking().d('status') or ''

    def wait_booking(self, st, n=40):
        for _ in range(n):
            if self.bstatus() == st:
                return True
            time.sleep(0.5)
        return False


def gateway(order, status, q=''):
    return SESS.post(f'{FAKE}/_sim/{order}/{status}{q}').text


def pg(sql):
    out = subprocess.check_output(['docker', 'exec', 'telkomsel_db_postgresql', 'psql', '-U', 'root', '-d', 'berebut', '-tA', '-c', sql])
    return out.decode().strip()


def run_parts(parts):
    ts = [threading.Thread(target=p, name=p.__name__) for p in parts]
    for t in ts:
        t.start()
    for t in ts:
        t.join()


# ---------------------------------------------------------------- service tokens (signed exactly as the services do)
def _b64(b):
    return base64.urlsafe_b64encode(b).rstrip(b'=').decode()


def service_token(keyfile, kid, iss, aud, sub='svc', scope='service', ttl=300, iat=None, exp=None):
    k = serialization.load_pem_private_key(open(os.path.join(KEYS, keyfile), 'rb').read(), None)
    iat = iat if iat is not None else int(time.time())
    exp = exp if exp is not None else iat + ttl
    h = _b64(json.dumps({'alg': 'EdDSA', 'typ': 'JWT', 'kid': kid}).encode())
    c = {'iss': iss, 'sub': sub, 'aud': aud, 'iat': iat, 'exp': exp}
    if scope:
        c['scope'] = scope
    p = _b64(json.dumps(c).encode())
    return h + '.' + p + '.' + _b64(k.sign((h + '.' + p).encode()))


def queue_svc_token(**kw):   # Queue -> Booking (verified with queue public key, iss rbt-queue, aud rbt)
    return service_token('queue-token.pem', 'queue-1', 'rbt-queue', 'rbt', sub='queue-service', **kw)


def payment_svc_token(**kw):  # Payment -> Booking (iss rbt-payment, aud rbt-booking)
    return service_token('payment-token.pem', 'payment-1', 'rbt-payment', 'rbt-booking', sub='payment-service', scope=None, **kw)


def booking_svc_token(**kw):  # Booking -> Payment (iss rbt-booking, aud rbt-payment)
    return service_token('booking-token.pem', 'booking-1', 'rbt-booking', 'rbt-payment', sub='booking-service', scope=None, **kw)


def SH(tok):
    return {'Authorization': 'Bearer ' + tok}
