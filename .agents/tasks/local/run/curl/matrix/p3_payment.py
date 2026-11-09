"""Part 3: payment.yaml (page, page tokens, attempts, status, webhook, local simulator) + internal-payment resolve."""
import os, hashlib; os.environ.setdefault('PART', 'p3')
from mlib import *
BAD = {'X-Visitor-Token': 'not-a-token'}
KEYM = [l.split('=', 1)[1].strip() for l in open(os.path.join(ROOT, 'services/payment/.env')) if l.startswith('MIDTRANS_SERVER_KEY=')][0]
PAGE = '/api/v1/payments/{bookingId}/page'
PTOK = '/api/v1/payments/{bookingId}/page-tokens'
ATT = '/api/v1/payments/{bookingId}/attempts'
STAT = '/api/v1/payments/{bookingId}/status'
WH = '/api/v1/webhooks/midtrans'
SIM = '/api/v1/local-simulator/payments/{attemptId}/scenario'


def webhook(order, status_code, gross, tstatus, sig=None, want=(200, ''), name='webhook'):
    sig = sig or hashlib.sha512((order + status_code + gross + KEYM).encode()).hexdigest()
    return call('POST', 'payment', WH, json_body={'order_id': order, 'status_code': status_code, 'gross_amount': gross, 'transaction_status': tstatus,
                'signature_key': sig, 'transaction_id': 'tx-' + order, 'fraud_status': 'accept'}, want=want, name=name)


def gross_of(order):
    g = [o['gross'] for o in SESS.get(FAKE + '/_sim/orders').json() if o['id'] == order][0]
    return str(g) + '.00'


def pages():
    ev = new_event('P3 pages', 20, 20)
    p = Buyer(ev, 'p1').admit(); p.hold(ev.reg, 1)
    o = Buyer(ev, 'p2').admit()
    call('GET', 'payment', PAGE, {'bookingId': p.bid}, headers=p.vh(), want=(200, ''), name='page ok')
    call('GET', 'payment', PAGE, {'bookingId': p.bid}, want=(401, 'UNAUTHORIZED'), name='page no token')
    call('GET', 'payment', PAGE, {'bookingId': p.bid}, headers=BAD, want=(401, 'UNAUTHORIZED'), name='page garbage token')
    call('GET', 'payment', PAGE, {'bookingId': uid()}, headers=p.vh(), want=(404, 'NOT_FOUND'), name='page unknown booking')
    call('GET', 'payment', PAGE, {'bookingId': p.bid}, headers=o.vh(), want=(404, 'NOT_FOUND'), name='page other visitor')
    call('GET', 'payment', PAGE, {'bookingId': 'nope'}, headers=p.vh(), want=(0, ''), name='page non-uuid (observe)')
    r = call('POST', 'payment', PTOK, {'bookingId': p.bid}, headers=p.vh(), want=(201, ''), name='page-token ok')
    pt = r.d('pageToken')
    call('POST', 'payment', PTOK, {'bookingId': p.bid}, want=(401, 'UNAUTHORIZED'), name='page-token no token')
    call('POST', 'payment', PTOK, {'bookingId': p.bid}, headers=BAD, want=(401, 'UNAUTHORIZED'), name='page-token garbage token')
    call('POST', 'payment', PTOK, {'bookingId': uid()}, headers=p.vh(), want=(404, 'NOT_FOUND'), name='page-token unknown')
    call('POST', 'payment', PTOK, {'bookingId': p.bid}, headers=o.vh(), want=(404, 'NOT_FOUND'), name='page-token other visitor')
    call('POST', 'payment', '/api/v1/payment-page-sessions', json_body={'pageToken': pt}, want=(204, ''), name='page-session exchange')
    call('POST', 'payment', '/api/v1/payment-page-sessions', json_body={'pageToken': pt}, want=(403, 'PAYMENT_PAGE_TOKEN_INVALID'), name='page-session reused token')
    call('POST', 'payment', '/api/v1/payment-page-sessions', json_body={'pageToken': 'garbage'}, want=(403, 'PAYMENT_PAGE_TOKEN_INVALID'), name='page-session garbage token')
    call('POST', 'payment', '/api/v1/payment-page-sessions', headers=BAD, json_body={'pageToken': pt}, want=(401, 'UNAUTHORIZED'), name='page-session garbage visitor token')
    call('POST', 'payment', '/api/v1/payment-page-sessions', data='{x', want=(0, ''), name='page-session bad json (observe)')
    # status
    call('GET', 'payment', STAT, {'bookingId': p.bid}, headers=p.vh(), want=(200, ''), name='status ok')
    call('GET', 'payment', STAT, {'bookingId': p.bid}, want=(401, 'UNAUTHORIZED'), name='status no token')
    call('GET', 'payment', STAT, {'bookingId': p.bid}, headers=BAD, want=(401, 'UNAUTHORIZED'), name='status garbage token')
    call('GET', 'payment', STAT, {'bookingId': uid()}, headers=p.vh(), want=(404, 'NOT_FOUND'), name='status unknown')
    call('GET', 'payment', STAT, {'bookingId': p.bid}, headers=o.vh(), want=(404, 'NOT_FOUND'), name='status other visitor')
    # attempts
    call('POST', 'payment', ATT, {'bookingId': p.bid}, headers=p.vh({'Idempotency-Key': key()}), json_body={'method': 'VA_PERMATA'}, want=(422, 'PAYMENT_METHOD_UNAVAILABLE'), name='attempt disabled method')
    call('POST', 'payment', ATT, {'bookingId': p.bid}, headers={'Idempotency-Key': key()}, json_body={'method': 'QRIS'}, want=(401, 'UNAUTHORIZED'), name='attempt no token')
    call('POST', 'payment', ATT, {'bookingId': p.bid}, headers=dict(BAD, **{'Idempotency-Key': key()}), json_body={'method': 'QRIS'}, want=(401, 'UNAUTHORIZED'), name='attempt garbage token')
    call('POST', 'payment', ATT, {'bookingId': uid()}, headers=p.vh({'Idempotency-Key': key()}), json_body={'method': 'QRIS'}, want=(404, 'NOT_FOUND'), name='attempt unknown booking')
    call('POST', 'payment', ATT, {'bookingId': p.bid}, headers=o.vh({'Idempotency-Key': key()}), json_body={'method': 'QRIS'}, want=(404, 'NOT_FOUND'), name='attempt other visitor')
    call('POST', 'payment', ATT, {'bookingId': p.bid}, headers=p.vh({'Idempotency-Key': key()}), data='{x', want=(0, ''), name='attempt bad json (observe)')
    call('POST', 'payment', ATT, {'bookingId': p.bid}, headers=p.vh(), json_body={'method': 'QRIS'}, want=(0, ''), name='attempt no idempotency key (observe)')
    k = key()
    p.pay(k=k, want=(201, ''), name='attempt ok', quiet=False)
    call('POST', 'payment', ATT, {'bookingId': p.bid}, headers=p.vh({'Idempotency-Key': k}), json_body={'method': 'GOPAY'}, want=(409, 'IDEMPOTENCY_KEY_REUSED'), name='attempt same key different body')
    # webhook
    g = gross_of(p.order)
    webhook(p.order, '200', g, 'settlement', sig='deadbeef', want=(401, 'WEBHOOK_SIGNATURE_INVALID'), name='webhook bad signature')
    webhook(p.order, '200', '1.00', 'settlement', want=(200, ''), name='webhook wrong amount')
    webhook('RB-UNKNOWN-1', '200', '1.00', 'settlement', want=(200, ''), name='webhook unknown order')
    call('POST', 'payment', WH, data='{x', want=(0, ''), name='webhook bad json (observe)')
    call('POST', 'payment', WH, json_body={}, want=(0, ''), name='webhook empty body (observe)')
    gateway(p.order, 'settlement'); assert p.wait_booking('PAID')
    webhook(p.order, '200', g, 'settlement', want=(200, ''), name='webhook duplicate settlement')
    call('POST', 'payment', ATT, {'bookingId': p.bid}, headers=p.vh({'Idempotency-Key': key()}), json_body={'method': 'QRIS'}, want=(409, 'BOOKING_NOT_PAYABLE'), name='attempt on PAID booking')
    call('POST', 'payment', PTOK, {'bookingId': p.bid}, headers=p.vh(), want=(409, 'BOOKING_NOT_PAYABLE'), name='page-token on PAID booking')
    call('GET', 'payment', PAGE, {'bookingId': p.bid}, headers=p.vh(), want=(409, 'BOOKING_NOT_PAYABLE'), name='page on PAID booking')


def simulator():
    ev = new_event('P3 sim', 20, 20)
    s = Buyer(ev, 's1').admit(); s.hold(ev.reg, 1); s.pay()
    o = Buyer(ev, 's2').admit()
    att = pg(f"select id from payment.payment_attempts where booking_id='{s.bid}' order by attempt_no desc limit 1")
    call('POST', 'payment', SIM, {'attemptId': att}, headers=o.vh(), json_body={'scenario': 'SETTLE'}, want=(404, 'NOT_FOUND'), name='sim other visitor')
    call('POST', 'payment', SIM, {'attemptId': uid()}, headers=s.vh(), json_body={'scenario': 'SETTLE'}, want=(404, 'NOT_FOUND'), name='sim unknown attempt')
    call('POST', 'payment', SIM, {'attemptId': att}, json_body={'scenario': 'SETTLE'}, want=(401, 'UNAUTHORIZED'), name='sim no token')
    call('POST', 'payment', SIM, {'attemptId': att}, headers=BAD, json_body={'scenario': 'SETTLE'}, want=(401, 'UNAUTHORIZED'), name='sim garbage token')
    call('POST', 'payment', SIM, {'attemptId': att}, headers=s.vh(), json_body={'scenario': 'NOPE'}, want=(400, 'VALIDATION_ERROR'), name='sim unknown scenario')
    call('POST', 'payment', SIM, {'attemptId': att}, headers=s.vh(), data='{x', want=(400, 'VALIDATION_ERROR'), name='sim bad json')
    call('POST', 'payment', SIM, {'attemptId': att}, headers=s.vh(), json_body={'scenario': 'SETTLE', 'foo': 1}, want=(400, 'VALIDATION_ERROR'), name='sim unknown field')
    call('POST', 'payment', SIM, {'attemptId': 'nope'}, headers=s.vh(), json_body={'scenario': 'SETTLE'}, want=(0, ''), name='sim non-uuid attempt (observe)')
    call('POST', 'payment', SIM, {'attemptId': att}, headers=s.vh(), json_body={'scenario': 'SETTLE'}, want=(202, ''), name='sim SETTLE')
    assert s.wait_booking('PAID')


def resolve():
    # internal-payment.yaml: POST /internal/v1/payments/{bookingId}/resolve  (Booking -> Payment, booking token)
    RS = '/internal/v1/payments/{bookingId}/resolve'
    ev = new_event('P3 resolve', 5, 5)
    s = Buyer(ev, 'r1').admit(); s.hold(ev.reg, 1)
    call('POST', 'payment', RS, {'bookingId': s.bid}, headers=SH(booking_svc_token()), json_body={'mode': 'CHECK'}, want=(200, ''), name='resolve with booking service token')
    call('POST', 'payment', RS, {'bookingId': s.bid}, json_body={'mode': 'CHECK'}, want=(401, ''), name='resolve no token (undocumented)')
    call('POST', 'payment', RS, {'bookingId': s.bid}, headers={'Authorization': 'Bearer garbage'}, json_body={'mode': 'CHECK'}, want=(401, ''), name='resolve garbage token (undocumented)')
    call('POST', 'payment', RS, {'bookingId': s.bid}, headers=SH(queue_svc_token()), json_body={'mode': 'CHECK'}, want=(401, ''), name='resolve wrong-issuer token (undocumented)')
    call('POST', 'payment', RS, {'bookingId': s.bid}, headers=SH(booking_svc_token(iat=int(time.time()) - 4000, exp=int(time.time()) - 3000)), json_body={'mode': 'CHECK'}, want=(401, ''), name='resolve expired token (undocumented)')
    call('POST', 'payment', RS, {'bookingId': s.bid}, headers=SH(booking_svc_token(ttl=3600)), json_body={'mode': 'CHECK'}, want=(401, ''), name='resolve over-long ttl token (undocumented)')
    call('POST', 'payment', RS, {'bookingId': s.bid}, headers=SH(booking_svc_token()), data='{x', want=(0, ''), name='resolve bad json (observe)')
    call('POST', 'payment', RS, {'bookingId': 'nope'}, headers=SH(booking_svc_token()), json_body={'mode': 'CHECK'}, want=(0, ''), name='resolve non-uuid (observe)')
    call('POST', 'payment', RS, {'bookingId': uid()}, headers=SH(booking_svc_token()), json_body={'mode': 'CHECK'}, want=(0, ''), name='resolve unknown booking (observe)')


if __name__ == '__main__':
    run_parts([pages, simulator, resolve])
