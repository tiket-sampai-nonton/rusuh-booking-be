"""Part 8: rate-limit rows on the real stack. Run ALONE and last: it exhausts shared per-IP budgets (queue join, user verify / refresh+logout)
for about a minute each, and waits for them to reopen. Per-visitor limits (me/heartbeat/claim) are isolated to the visitors created here."""
import collections, os; os.environ.setdefault('PART', 'p8')
from mlib import *
from concurrent.futures import ThreadPoolExecutor
BAD = {'X-Visitor-Token': 'not-a-token'}


def burst(n, fn, workers=32):
    with ThreadPoolExecutor(workers) as ex:
        return list(ex.map(fn, range(n)))


def queue_visitor_limits():
    ev = new_event('P8 visitor limits', 5, 5, capacity=1)
    a = Buyer(ev, 'a'); a.join(); a.wait_admitted()
    # me: 120 / min per visitor
    rs = burst(140, lambda i: call('GET', 'queue', '/api/v1/queue-entries/me', params={'eventId': ev.id}, headers=a.vh(), want=(429, 'RATE_LIMITED'), name='me burst', quiet=True))
    note('me burst:', collections.Counter((r.status, r.code) for r in rs))
    r429 = [r for r in rs if r.status == 429]
    if r429:
        note('429 Retry-After:', r429[0].resp.headers.get('Retry-After'))
    # claim: which limiter? burst on a visitor's own (garbage) entry id
    b = Buyer(ev, 'b'); b.join()
    rs = burst(140, lambda i: call('POST', 'queue', '/api/v1/queue-entries/{entryId}/claim', {'entryId': b.entry}, params={'eventId': ev.id}, headers=b.vh(), want=(429, 'RATE_LIMITED'), name='claim burst', quiet=True))
    note('claim burst:', collections.Counter((r.status, r.code) for r in rs))
    # heartbeat: 20 / min per visitor
    c = Buyer(ev, 'c'); c.join()
    rs = burst(30, lambda i: call('POST', 'queue', '/api/v1/queue-entries/me/heartbeat', params={'eventId': ev.id}, headers=c.vh(), want=(429, 'RATE_LIMITED'), name='heartbeat burst', quiet=True), workers=8)
    note('heartbeat burst:', collections.Counter((r.status, r.code) for r in rs))


def queue_join_ip():
    ev = new_event('P8 join ip', 60, 60, capacity=100)
    hdr = lambda i: {'X-Client-Type': 'mobile', 'X-Device-Id': 'p8-rl-%d-%d' % (os.getpid(), i)}
    rs = burst(340, lambda i: call('POST', 'queue', '/api/v1/events/{eventId}/queue-entries', {'eventId': ev.id}, headers=hdr(i), want=(429, 'RATE_LIMITED'), name='join burst', quiet=True), workers=48)
    note('join burst:', collections.Counter((r.status, r.code) for r in rs))
    r429 = [r for r in rs if r.status == 429]
    if r429:
        note('429 Retry-After:', r429[0].resp.headers.get('Retry-After'))
    time.sleep(62)


def user_ip_limits():
    # verify: 60 / min per IP (wrong codes for random emails; never matches a real challenge)
    rs = burst(80, lambda i: call('POST', 'user', '/api/v1/auth/otp/verify', json_body={'email': 'rl-%d-%d@sim.test' % (os.getpid(), i), 'code': '000000'}, want=(429, 'RATE_LIMITED'), name='verify burst', quiet=True), workers=16)
    note('verify burst:', collections.Counter((r.status, r.code) for r in rs))
    time.sleep(62)
    # refresh + logout share the per-IP session limiter (300 / min)
    rs = burst(330, lambda i: call('POST', 'user', '/api/v1/auth/refresh', json_body={'refreshToken': 'garbage-%d' % i}, want=(429, 'RATE_LIMITED'), name='refresh burst', quiet=True), workers=32)
    note('refresh burst:', collections.Counter((r.status, r.code) for r in rs))
    rs = burst(20, lambda i: call('POST', 'user', '/api/v1/auth/logout', json_body={'refreshToken': 'garbage-%d' % i}, want=(429, 'RATE_LIMITED'), name='logout burst (same bucket)', quiet=True), workers=8)
    note('logout burst (same minute):', collections.Counter((r.status, r.code) for r in rs))
    time.sleep(62)
    rs = burst(330, lambda i: call('POST', 'user', '/api/v1/auth/logout', json_body={'refreshToken': 'garbage-%d' % i}, want=(429, 'RATE_LIMITED'), name='logout burst', quiet=True), workers=32)
    note('logout burst:', collections.Counter((r.status, r.code) for r in rs))
    time.sleep(62)


if __name__ == '__main__':
    import collections
    queue_visitor_limits()
    queue_join_ip()
    user_ip_limits()
