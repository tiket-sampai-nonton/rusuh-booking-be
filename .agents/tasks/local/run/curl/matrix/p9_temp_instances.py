"""Part 9: rows that need a condition the shared stack must not be put in. Uses TEMPORARY extra instances of the same binaries
on spare ports (18081-18085), started with the same env as start.sh plus one override each, then killed. Only already-running
infra is used; the temporary instances reach Postgres / RabbitMQ through small local TCP forwarders that this script kills to
simulate a dependency outage (no container is touched).
  - booking RATE_LIMIT_PER_IP=5  -> 429 on GET /booking-sessions/current and POST .../attendee-checks (the main one runs with the limiter off)
  - /health 503 for every service: kill the Postgres forwarder
  - user OTP request 503 DEPENDENCY_UNAVAILABLE: kill the RabbitMQ forwarder (the OTP is published to the broker)"""
import os, signal, socket, threading, select; os.environ.setdefault('PART', 'p9')
from mlib import *

BIN = os.path.join(RUN, 'bin')
procs = []


class Forwarder:
    def __init__(self, listen, target):
        self.target = target
        self.srv = socket.socket(); self.srv.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        self.srv.bind(('127.0.0.1', listen)); self.srv.listen(64)
        self.conns = []; self.alive = True
        threading.Thread(target=self.loop, daemon=True).start()

    def loop(self):
        while self.alive:
            try:
                c, _ = self.srv.accept()
            except OSError:
                return
            if not self.alive:   # accept() may still return one connection after kill(); refuse it
                c.close(); return
            try:
                u = socket.create_connection(('127.0.0.1', self.target), timeout=3)
            except OSError:
                c.close(); continue
            self.conns += [c, u]
            threading.Thread(target=self.pipe, args=(c, u), daemon=True).start()
            threading.Thread(target=self.pipe, args=(u, c), daemon=True).start()

    def pipe(self, a, b):
        try:
            while True:
                d = a.recv(65536)
                if not d:
                    break
                b.sendall(d)
        except OSError:
            pass
        finally:
            for s in (a, b):
                try: s.close()
                except OSError: pass

    def kill(self):
        self.alive = False
        try: self.srv.shutdown(socket.SHUT_RDWR)   # wakes a blocked accept() so the listener really goes away
        except OSError: pass
        try: self.srv.close()
        except OSError: pass
        for s in self.conns:
            try: s.shutdown(socket.SHUT_RDWR)
            except OSError: pass
            try: s.close()
            except OSError: pass


def spawn(svc, port, env_extra, tag):
    env = dict(os.environ)
    base = {'booking': {'ADMIN_EMAILS': '@admin.sim.test', 'RATE_LIMIT_PER_IP': '0', 'SERVICE_TOKEN_PRIVATE_KEY_PATH': KEYS + '/booking-token.pem',
                        'PAYMENT_TOKEN_PUBLIC_KEY_PATH': KEYS + '/payment-token.pub.pem', 'IMAGE_LOCAL_DIR': RUN + '/images'},
            'queue': {'ADMIN_EMAILS': '@admin.sim.test', 'RATE_LIMIT_JOIN_PER_IP': '300'}, 'payment': {}, 'notification': {'EMAIL_PROVIDER': 'mailpit'},
            'user': {'OTP_MAX_PER_IP_HOUR': '1000'}}[svc]
    env.update(base); env.update(env_extra); env['APP_PORT'] = str(port)
    lf = open(os.path.join(RUN, 'log', f'tmp-{svc}-{tag}.log'), 'wb')
    p = subprocess.Popen([os.path.join(BIN, svc), 'api'], cwd=os.path.join(ROOT, 'services', svc), env=env, stdout=lf, stderr=lf, start_new_session=True)
    procs.append(p)
    for _ in range(80):
        try:
            if SESS.get(f'http://localhost:{port}/health', timeout=1).status_code == 200:
                note(f'temp {svc} api on :{port} up (pid {p.pid}, override {env_extra})'); return p
        except Exception:
            pass
        time.sleep(0.25)
    raise RuntimeError(f'temp {svc} did not start; see log/tmp-{svc}-{tag}.log')


def cleanup():
    for p in procs:
        try: os.killpg(p.pid, signal.SIGTERM)
        except Exception: pass
    time.sleep(1)
    for p in procs:
        try: os.killpg(p.pid, signal.SIGKILL)
        except Exception: pass


def booking_ratelimit():
    ev = new_event('P9 ratelimit', 10, 10)
    a = Buyer(ev, 'a').admit()
    spawn('booking', 18082, {'RATE_LIMIT_PER_IP': '5'}, 'rl')
    T = 'http://localhost:18082'
    for i in range(9):
        call('GET', T, '/api/v1/booking-sessions/current', params={'eventId': ev.id}, headers=a.vh(), want=(200, '') if i < 5 else (429, 'RATE_LIMITED'), name='temp booking: current #%d' % (i + 1), row_tpl='/api/v1/booking-sessions/current', as_svc='booking')
    time.sleep(61)   # fresh window for the second route
    for i in range(9):
        call('POST', T, '/api/v1/booking-sessions/{sessionId}/attendee-checks', {'sessionId': a.sid}, headers=a.vh(), json_body={'nik': nik()}, want=(200, '') if i < 5 else (429, 'RATE_LIMITED'), name='temp booking: attendee-check #%d' % (i + 1), row_tpl='/api/v1/booking-sessions/{sessionId}/attendee-checks', as_svc='booking')


def health_503():
    pgf = Forwarder(25432, 5432)
    mqf = Forwarder(25672, 5672)
    pgenv = {'POSTGRES_PORT': '25432'}
    spawn('booking', 18082, dict(pgenv, RABBITMQ_URL='amqp://guest:guest@localhost:25672/'), 'h')
    spawn('payment', 18083, pgenv, 'h')
    spawn('queue', 18081, pgenv, 'h')
    spawn('notification', 18084, pgenv, 'h')
    spawn('user', 18085, dict(pgenv, RABBITMQ_URL='amqp://guest:guest@localhost:25672/'), 'h')
    ports = {'queue': 18081, 'booking': 18082, 'payment': 18083, 'notification': 18084, 'user': 18085}
    for svc, port in ports.items():
        r = SESS.get(f'http://localhost:{port}/health'); note(svc, 'health before:', r.status_code)
    # user: broker unreachable -> OTP request 503 (user's health does not check the broker)
    mqf.kill(); time.sleep(1)
    em = 'p9-otp-%d%d@sim.test' % (os.getpid(), random.randint(0, 9999))
    r = call('POST', 'http://localhost:18085', '/api/v1/auth/otp/request', json_body={'email': em}, want=(503, 'DEPENDENCY_UNAVAILABLE'), name='temp user: otp request with broker unreachable', timeout=40, row_tpl='/api/v1/auth/otp/request', as_svc='user')
    note('otp request with broker down:', r.status, r.code)
    pgf.kill(); time.sleep(2)
    for svc, port in ports.items():
        for _ in range(12):   # health may be cached for a few seconds
            r = call('GET', f'http://localhost:{port}', '/health', want=(503, ''), name=f'temp {svc}: health with Postgres unreachable', timeout=15, quiet=True, as_svc=svc)
            if r.status == 503:
                break
            time.sleep(2)
        print(('PASS' if r.ok else 'FAIL'), f'[{PART}] temp {svc}: health with Postgres unreachable: got {r.status}')
        note(svc, 'health ->', r.status, json.dumps(r.json)[:200])


if __name__ == '__main__':
    try:
        booking_ratelimit()
        cleanup(); procs.clear(); time.sleep(1)
        health_503()
    finally:
        cleanup()
