"""Part 7: rows that need real server-authoritative time (session TTL 90 s min, admission token 60 s, claim window 180 s,
OTP per-email limit 5/hour with 60 s cooldown). Parts run in parallel; ~6 minutes."""
import os; os.environ.setdefault('PART', 'p7')
from mlib import *


def patch_ttl(ev):
    call('PATCH', 'booking', '/internal/v1/admin/events/{eventId}', {'eventId': ev.id}, headers=AH(), json_body={'selectionTtlSeconds': 90, 'paymentTtlSeconds': 120}, want=(200, ''), name='setup ttl', quiet=True)


def session_expiry():
    ev = new_event('P7 session ttl', 5, 5); patch_ttl(ev)
    a = Buyer(ev, 'a').admit()
    r = call('GET', 'booking', '/api/v1/booking-sessions/{sessionId}', {'sessionId': a.sid}, headers=a.vh(), want=(200, ''), name='session before expiry')
    note('session expiresAt', r.d('expiresAt'), 'serverTime', r.d('serverTime'))
    time.sleep(100)
    call('GET', 'booking', '/api/v1/booking-sessions/{sessionId}', {'sessionId': a.sid}, headers=a.vh(), want=(410, 'SESSION_EXPIRED'), name='session get after expiry')
    call('POST', 'booking', '/api/v1/bookings', headers=a.vh({'Idempotency-Key': key()}), json_body=a.body(a.items(ev.reg, 1)), want=(410, 'SESSION_EXPIRED'), name='hold after session expiry')
    call('GET', 'booking', '/api/v1/booking-sessions/current', params={'eventId': ev.id}, headers=a.vh(), want=(0, ''), name='session current after expiry (observe)')
    call('POST', 'booking', '/api/v1/booking-sessions/{sessionId}/attendee-checks', {'sessionId': a.sid}, headers=a.vh(), json_body={'nik': nik()}, want=(0, ''), name='attendee-check after expiry (observe)')
    call('POST', 'booking', '/api/v1/booking-sessions/{sessionId}/cancel', {'sessionId': a.sid}, headers=a.vh(), want=(0, ''), name='session cancel after expiry (observe)')


def admission_token_expiry():
    ev = new_event('P7 admission ttl', 5, 5)
    b = Buyer(ev, 'b'); b.join(); b.wait_admitted(); b.claim()
    time.sleep(75)   # admission token TTL is 60 s (+5 s verifier leeway)
    call('POST', 'booking', '/api/v1/booking-sessions', headers=b.vh(), json_body={'admissionToken': b.adm}, want=(410, 'SESSION_EXPIRED'), name='create session with expired admission token')


def turn_missed():
    ev = new_event('P7 turn missed', 5, 5)
    # (1) admitted, never claimed: claim window (180 s) passes while heartbeats keep the entry alive
    c = Buyer(ev, 'c'); c.join(); c.wait_admitted()
    me = call('GET', 'queue', '/api/v1/queue-entries/me', params={'eventId': ev.id}, headers=c.vh(), want=(200, ''), name='me admitted')
    note('claimDeadline', me.d('claimDeadline'))
    # (2) claim just before the deadline, then create the session after the deadline
    d = Buyer(ev, 'd'); d.join(); d.wait_admitted()
    dl = call('GET', 'queue', '/api/v1/queue-entries/me', params={'eventId': ev.id}, headers=d.vh(), want=(200, ''), name='me admitted d').d('claimDeadline')
    import datetime as dt
    dls = dt.datetime.fromisoformat(dl.replace('Z', '+00:00')).timestamp()
    t_claim, t_end = dls - 4, dls + 4
    while time.time() < t_claim - 12:
        call('POST', 'queue', '/api/v1/queue-entries/me/heartbeat', params={'eventId': ev.id}, headers=d.vh(), want=(200, ''), name='setup heartbeat d', quiet=True)
        call('POST', 'queue', '/api/v1/queue-entries/me/heartbeat', params={'eventId': ev.id}, headers=c.vh(), want=(200, ''), name='setup heartbeat c', quiet=True)
        time.sleep(10)
    call('POST', 'queue', '/api/v1/queue-entries/me/heartbeat', params={'eventId': ev.id}, headers=c.vh(), want=(200, ''), name='setup heartbeat c', quiet=True)
    while time.time() < t_claim:
        time.sleep(0.2)
    d.claim(want=(200, ''), name='claim d 4 s before the claim deadline')
    note('claim d got admission token:', d.adm is not None)
    while time.time() < t_end:
        time.sleep(0.2)
    if d.adm:
        call('POST', 'booking', '/api/v1/booking-sessions', headers=d.vh(), json_body={'admissionToken': d.adm}, want=(410, 'TURN_MISSED'), name='create session 4 s after the claim deadline')
    # (1) continued: claim after the window
    while time.time() < dls + 8:
        time.sleep(1)
    call('POST', 'queue', '/api/v1/queue-entries/{entryId}/claim', {'entryId': c.entry}, params={'eventId': ev.id}, headers=c.vh(), want=(410, 'TURN_MISSED'), name='claim after the claim window')


def otp_limits():
    # per-email limit 5/hour is a fixed clock-hour window: wait for a fresh bucket if too close to the hour boundary
    left = 3600 - int(time.time()) % 3600
    if left < 340:
        note(f'waiting {left}s for a fresh hour bucket'); time.sleep(left + 2)
    e = 'otplimit-%d%d@sim.test' % (os.getpid(), random.randint(0, 99999))
    for i in range(6):
        r = call('POST', 'user', '/api/v1/auth/otp/request', json_body={'email': e}, want=(202, '') if i < 5 else (429, 'RATE_LIMITED'), name='otp request #%d (61 s apart)' % (i + 1))
        if i < 5:
            time.sleep(61)


def otp_expired():
    e = 'otpexp-%d%d@sim.test' % (os.getpid(), random.randint(0, 99999))
    call('POST', 'user', '/api/v1/auth/otp/request', json_body={'email': e}, want=(202, ''), name='otp request for expiry test')
    code = mail_otp(e)
    time.sleep(5 * 60 + 8)
    call('POST', 'user', '/api/v1/auth/otp/verify', json_body={'email': e, 'code': code}, want=(401, 'OTP_EXPIRED'), name='otp verify after TTL (correct code)')


if __name__ == '__main__':
    run_parts([session_expiry, admission_token_expiry, turn_missed, otp_limits, otp_expired])
