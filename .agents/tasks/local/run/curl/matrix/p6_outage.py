"""Part 6: outage rows. Stops ONE local process at a time (never Docker), records, and restores it exactly as start.sh starts it
(same binary, cwd and env; pids file updated). Run alone (after the parallel parts)."""
import os, signal; os.environ.setdefault('PART', 'p6')
from mlib import *

PIDS = os.path.join(RUN, 'pids')
LOG = os.path.join(RUN, 'log')
BIN = os.path.join(RUN, 'bin')
keys = KEYS
envs = {
    ('booking', 'api'): {'ADMIN_EMAILS': '@admin.sim.test', 'RATE_LIMIT_PER_IP': '0', 'SERVICE_TOKEN_PRIVATE_KEY_PATH': keys + '/booking-token.pem',
                         'PAYMENT_TOKEN_PUBLIC_KEY_PATH': keys + '/payment-token.pub.pem', 'IMAGE_LOCAL_DIR': RUN + '/images'},
    ('payment', 'api'): {},
    ('queue', 'api'): {'ADMIN_EMAILS': '@admin.sim.test', 'RATE_LIMIT_JOIN_PER_IP': '300'},
}
PORTS = {'booking': 8082, 'payment': 8083, 'queue': 8081}


def pid_of(svc, verb):
    out = subprocess.check_output(['pgrep', '-f', f'{BIN}/{svc} {verb}$|bin/{svc} {verb}$']).decode().split()
    return max(int(x) for x in out)   # newest: an older instance may still be draining after SIGTERM


def stop(svc, verb):
    pid = pid_of(svc, verb)
    os.kill(pid, signal.SIGTERM)
    for _ in range(60):
        try:
            os.kill(pid, 0)
        except ProcessLookupError:
            break
        time.sleep(0.25)
    note(f'stopped {svc} {verb} (old pid {pid})')
    return pid


def start(svc, verb, old):
    env = dict(os.environ); env.update(envs[(svc, verb)])
    lf = open(os.path.join(LOG, f'{svc}-{verb}.log'), 'ab')
    p = subprocess.Popen([os.path.join(BIN, svc), verb], cwd=os.path.join(ROOT, 'services', svc), env=env, stdout=lf, stderr=lf, start_new_session=True)
    txt = open(PIDS).read().split('\n')
    txt = [str(p.pid) if l == str(old) else l for l in txt]
    open(PIDS, 'w').write('\n'.join(txt))
    for _ in range(80):
        try:
            if SESS.get(f'http://localhost:{PORTS[svc]}/health', timeout=1).status_code == 200:
                note(f'restarted {svc} {verb} (pid {p.pid}), health 200')
                return p
        except Exception:
            pass
        time.sleep(0.25)
    raise RuntimeError(f'{svc} did not come back')


def stop_fake():
    pid = int(subprocess.check_output(['pgrep', '-f', 'fake_midtrans.py']).decode().split()[0])
    os.kill(pid, signal.SIGTERM); time.sleep(1)
    note(f'stopped fake_midtrans (pid {pid})')
    return pid


def start_fake(old):
    key_ = [l.split('=', 1)[1].strip() for l in open(os.path.join(ROOT, 'services/payment/.env')) if l.startswith('MIDTRANS_SERVER_KEY=')][0]
    env = dict(os.environ, MIDTRANS_SERVER_KEY=key_)
    lf = open(os.path.join(LOG, 'fake-midtrans.log'), 'ab')
    p = subprocess.Popen(['python3', os.path.join(RUN, 'fake_midtrans.py')], env=env, stdout=lf, stderr=lf, start_new_session=True)
    txt = open(PIDS).read().split('\n')
    open(PIDS, 'w').write('\n'.join(str(p.pid) if l == str(old) else l for l in txt))
    for _ in range(40):
        try:
            if SESS.get(FAKE + '/_sim/orders', timeout=1).status_code == 200:
                note(f'restarted fake_midtrans (pid {p.pid})'); return p
        except Exception:
            pass
        time.sleep(0.25)
    raise RuntimeError('fake midtrans did not come back')


def main():
    A = AH()
    # ---- fixtures made while everything is up
    ev = new_event('P6 outage', 40, 40)
    c = Buyer(ev, 'c').admit(); c.hold(ev.reg, 1)                  # booking to cancel while Payment is down
    s = Buyer(ev, 's').admit(); s.hold(ev.reg, 1); s.pay()         # attempt for the simulator while Booking is down
    att = pg(f"select id from payment.payment_attempts where booking_id='{s.bid}' order by attempt_no desc limit 1")
    p = Buyer(ev, 'p').admit(); p.hold(ev.reg, 1)                  # booking for attempts while the gateway is down
    j = new_event('P6 join', 5, 5, queue=False)                    # published event, queue not yet configured -> Queue must ask Booking
    j2 = new_event('P6 join2', 5, 5)                               # published + configured, never joined
    jb = Buyer(j2, 'j')
    hold_ev = new_event('P6 hold', 5, 5)
    h = Buyer(hold_ev, 'h').admit()
    # ---- O1: Payment API down -> cancel needs Payment to resolve -> 503
    old = stop('payment', 'api')
    try:
        call('POST', 'booking', '/api/v1/bookings/{bookingId}/cancel', {'bookingId': c.bid}, headers=c.vh(), json_body={}, want=(503, 'DEPENDENCY_UNAVAILABLE'), name='cancel while Payment API is down')
        inv = pg(f"select held_quantity from booking.category_inventories where category_id='{ev.reg}'")
        note('held_quantity while down (REG, event fixture):', inv)
    finally:
        start('payment', 'api', old)
    call('POST', 'booking', '/api/v1/bookings/{bookingId}/cancel', {'bookingId': c.bid}, headers=c.vh(), json_body={}, want=(200, ''), name='cancel retry after Payment is back')
    # ---- O2: gateway (fake Midtrans) down -> attempt 503
    oldf = stop_fake()
    try:
        r = call('POST', 'payment', '/api/v1/payments/{bookingId}/attempts', {'bookingId': p.bid}, headers=p.vh({'Idempotency-Key': key()}), json_body={'method': 'QRIS'}, want=(503, 'PAYMENT_PROVIDER_UNAVAILABLE'), name='attempt while gateway is down', timeout=60)
        note('attempt while gateway down ->', r.status, r.code, json.dumps(r.json)[:160])
    finally:
        start_fake(oldf)
    time.sleep(2)
    # ---- O3: Booking API down -> simulator / queue join / queue-settings
    oldb = stop('booking', 'api')
    try:
        call('POST', 'payment', '/api/v1/local-simulator/payments/{attemptId}/scenario', {'attemptId': att}, headers=s.vh(), json_body={'scenario': 'SETTLE'}, want=(503, 'DEPENDENCY_UNAVAILABLE'), name='simulator while Booking API is down')
        call('POST', 'queue', '/api/v1/events/{eventId}/queue-entries', {'eventId': j.id}, headers={'X-Client-Type': 'mobile', 'X-Device-Id': 'p6-j', 'Idempotency-Key': key()}, want=(409, 'QUEUE_NOT_CONFIGURED'), name='join (queue not configured) while Booking API is down')
        call('PUT', 'queue', '/internal/v1/admin/events/{eventId}/queue-settings', {'eventId': j.id}, headers=A, json_body={'activeSlotCapacity': 5}, want=(503, 'DEPENDENCY_UNAVAILABLE'), name='queue-settings PUT while Booking API is down')
        call('PUT', 'queue', '/internal/v1/admin/events/{eventId}/queue-settings', {'eventId': j2.id}, headers=A, json_body={'activeSlotCapacity': 6}, want=(503, 'DEPENDENCY_UNAVAILABLE'), name='queue-settings PUT (already configured) while Booking API is down')
    finally:
        start('booking', 'api', oldb)
    time.sleep(2)
    call('POST', 'payment', '/api/v1/local-simulator/payments/{attemptId}/scenario', {'attemptId': att}, headers=s.vh(), json_body={'scenario': 'SETTLE'}, want=(202, ''), name='simulator after Booking is back')


def join_503_cycles():
    """Queue join answers 503 only while the sale window is not cached yet (it is cached by the queue worker's status publisher
    a moment after the settings exist). Create a fresh event + settings, take Booking down at once and join; retry with a fresh event
    when the cache was already warm. Booking is restored after every attempt."""
    for attempt in range(1, 7):
        j = new_event('P6 join cold %d' % attempt, 5, 5, queue=False)
        call('PUT', 'queue', '/internal/v1/admin/events/{eventId}/queue-settings', {'eventId': j.id}, headers=AH(), json_body={'activeSlotCapacity': 5}, want=(201, ''), name='setup settings', quiet=True)
        pid = pid_of('booking', 'api')
        os.kill(pid, signal.SIGTERM)
        for _ in range(100):
            try:
                SESS.get('http://localhost:8082/health', timeout=0.3)
            except Exception:
                break
            time.sleep(0.05)
        try:
            r = call('POST', 'queue', '/api/v1/events/{eventId}/queue-entries', {'eventId': j.id}, headers={'X-Client-Type': 'mobile', 'X-Device-Id': 'p6-cold-%d' % attempt, 'Idempotency-Key': key()},
                     want=(503, 'DEPENDENCY_UNAVAILABLE'), name='join with cold sale-window cache while Booking API is down (attempt %d)' % attempt)
        finally:
            for _ in range(100):   # let the old instance exit before its replacement starts
                try:
                    os.kill(pid, 0)
                except ProcessLookupError:
                    break
                time.sleep(0.25)
            start('booking', 'api', pid)
        time.sleep(2)
        if r.status == 503:
            break


def concurrent_hold():
    """POST /bookings 503 DEPENDENCY_UNAVAILABLE: replay of a same-key request that is still IN_PROGRESS."""
    ev = new_event('P6 hold-race', 40, 40)
    h = Buyer(ev, 'h').admit()
    k = key()
    body = h.body(h.items(ev.reg, 1))
    out = []
    def one():
        r = call('POST', 'booking', '/api/v1/bookings', headers=h.vh({'Idempotency-Key': k}), json_body=body, want=(503, 'DEPENDENCY_UNAVAILABLE'), name='hold same key concurrently', quiet=True)
        out.append((r.status, r.code))
    ts = [threading.Thread(target=one) for _ in range(25)]
    [t.start() for t in ts]; [t.join() for t in ts]
    note('concurrent same-key holds:', collections.Counter(out) if 'collections' in globals() else out)


if __name__ == '__main__':
    import collections
    concurrent_hold()
    join_503_cycles()
    main()
