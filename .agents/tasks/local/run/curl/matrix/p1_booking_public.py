"""Part 1: booking.yaml public routes (events, sessions, attendee-checks, holds, edits, cancel, tickets, history)."""
import os; os.environ.setdefault('PART', 'p1')
from mlib import *
BAD = {'X-Visitor-Token': 'not-a-token'}


def events_and_facets():
    for tpl in ('/api/v1/events', '/api/v1/events/facets'):
        call('GET', 'booking', tpl, want=(200, ''), name=f'GET {tpl} ok')
        call('GET', 'booking', tpl, params={'startDate': '2026-13-45'}, want=(400, 'VALIDATION_ERROR'), name=f'{tpl} bad date')
        call('GET', 'booking', tpl, params={'startDate': '2026-12-31', 'endDate': '2026-01-01'}, want=(400, 'VALIDATION_ERROR'), name=f'{tpl} reversed range')
        call('GET', 'booking', tpl, params={'genres': ','.join('g%d' % i for i in range(25))}, want=(400, 'VALIDATION_ERROR'), name=f'{tpl} >20 genres')
        call('GET', 'booking', tpl, params={'genres': 'x' * 70}, want=(400, 'VALIDATION_ERROR'), name=f'{tpl} genre token >64')
        call('GET', 'booking', tpl, params={'status': 'NOPE', 'timeframe': 'NOPE', 'sort': 'NOPE'}, want=(200, ''), name=f'{tpl} unknown enums fail-soft', )
    call('GET', 'booking', '/api/v1/events', headers=BAD, want=(401, 'UNAUTHORIZED'), name='events garbage visitor token')
    ev = new_event('P1 detail')
    call('GET', 'booking', '/api/v1/events/{eventId}', {'eventId': ev.id}, want=(200, ''), name='event detail')
    call('GET', 'booking', '/api/v1/events/{eventId}', {'eventId': uid()}, want=(404, 'NOT_FOUND'), name='event detail unknown')
    d = new_event('P1 draft', publish=False)
    call('GET', 'booking', '/api/v1/events/{eventId}', {'eventId': d.id}, want=(404, 'NOT_FOUND'), name='event detail draft')
    call('GET', 'booking', '/api/v1/events/{eventId}', {'eventId': 'not-a-uuid'}, want=(400, 'VALIDATION_ERROR'), name='event detail non-uuid')
    call('GET', 'booking', '/api/v1/events/{eventId}', {'eventId': ev.id}, headers=BAD, want=(401, 'UNAUTHORIZED'), name='event detail garbage visitor token')


def sessions():
    ev = new_event('P1 sessions', 10, 10)
    b = Buyer(ev, 's1'); b.join(); b.wait_admitted(); b.claim()
    call('POST', 'booking', '/api/v1/booking-sessions', headers=BAD, json_body={'admissionToken': b.adm}, want=(401, 'UNAUTHORIZED'), name='create session garbage visitor token')
    nt = Buyer(ev, 's0'); nt.join(); nt.wait_admitted(); nt.claim()
    r = call('POST', 'booking', '/api/v1/booking-sessions', headers={}, json_body={'admissionToken': nt.adm}, want=(401, 'UNAUTHORIZED'), name='create session without visitor token')
    call('POST', 'booking', '/api/v1/booking-sessions', headers=b.vh(), json_body={'admissionToken': ''}, want=(400, 'VALIDATION_ERROR'), name='create session empty admission token')
    call('POST', 'booking', '/api/v1/booking-sessions', headers=b.vh(), data='{not json', want=(400, 'VALIDATION_ERROR'), name='create session bad json')
    call('POST', 'booking', '/api/v1/booking-sessions', headers=b.vh(), json_body={'admissionToken': 'garbage'}, want=(403, 'ADMISSION_TOKEN_INVALID'), name='create session garbage admission token')
    b.session(want=(201, ''), name='create session', quiet=False)
    call('POST', 'booking', '/api/v1/booking-sessions', headers=b.vh(), json_body={'admissionToken': b.adm}, want=(403, 'ADMISSION_TOKEN_INVALID'), name='create session reused admission token')
    # reads
    call('GET', 'booking', '/api/v1/booking-sessions/current', params={'eventId': ev.id}, headers=b.vh(), want=(200, ''), name='session current')
    call('GET', 'booking', '/api/v1/booking-sessions/current', params={'eventId': ev.id}, want=(401, 'UNAUTHORIZED'), name='session current no token')
    call('GET', 'booking', '/api/v1/booking-sessions/current', params={'eventId': ev.id}, headers=BAD, want=(401, 'UNAUTHORIZED'), name='session current garbage token')
    call('GET', 'booking', '/api/v1/booking-sessions/current', params={'eventId': 'x'}, headers=b.vh(), want=(400, 'VALIDATION_ERROR'), name='session current bad eventId')
    call('GET', 'booking', '/api/v1/booking-sessions/current', headers=b.vh(), want=(400, 'VALIDATION_ERROR'), name='session current missing eventId')
    other = new_event('P1 sessions other', 3, 3)
    call('GET', 'booking', '/api/v1/booking-sessions/current', params={'eventId': other.id}, headers=b.vh(), want=(404, 'NOT_FOUND'), name='session current none for this event')
    call('GET', 'booking', '/api/v1/booking-sessions/{sessionId}', {'sessionId': b.sid}, headers=b.vh(), want=(200, ''), name='session get')
    call('GET', 'booking', '/api/v1/booking-sessions/{sessionId}', {'sessionId': b.sid}, want=(401, 'UNAUTHORIZED'), name='session get no token')
    call('GET', 'booking', '/api/v1/booking-sessions/{sessionId}', {'sessionId': b.sid}, headers=BAD, want=(401, 'UNAUTHORIZED'), name='session get garbage token')
    call('GET', 'booking', '/api/v1/booking-sessions/{sessionId}', {'sessionId': 'nope'}, headers=b.vh(), want=(400, 'VALIDATION_ERROR'), name='session get non-uuid')
    call('GET', 'booking', '/api/v1/booking-sessions/{sessionId}', {'sessionId': uid()}, headers=b.vh(), want=(404, 'NOT_FOUND'), name='session get unknown')
    o = Buyer(ev, 's2').admit()   # another valid visitor
    call('GET', 'booking', '/api/v1/booking-sessions/{sessionId}', {'sessionId': b.sid}, headers=o.vh(), want=(403, 'VISITOR_MISMATCH'), name='session get other visitor')
    # attendee checks
    call('POST', 'booking', '/api/v1/booking-sessions/{sessionId}/attendee-checks', {'sessionId': b.sid}, headers=b.vh(), json_body={'nik': nik()}, want=(200, ''), name='attendee-check ok')
    call('POST', 'booking', '/api/v1/booking-sessions/{sessionId}/attendee-checks', {'sessionId': b.sid}, headers=b.vh(), json_body={'nik': '1234'}, want=(422, 'NIK_INVALID'), name='attendee-check bad nik')
    call('POST', 'booking', '/api/v1/booking-sessions/{sessionId}/attendee-checks', {'sessionId': b.sid}, headers=b.vh(), data='{x', want=(400, 'VALIDATION_ERROR'), name='attendee-check bad json')
    call('POST', 'booking', '/api/v1/booking-sessions/{sessionId}/attendee-checks', {'sessionId': b.sid}, json_body={'nik': nik()}, want=(401, 'UNAUTHORIZED'), name='attendee-check no token')
    call('POST', 'booking', '/api/v1/booking-sessions/{sessionId}/attendee-checks', {'sessionId': b.sid}, headers=BAD, json_body={'nik': nik()}, want=(401, 'UNAUTHORIZED'), name='attendee-check garbage token')
    call('POST', 'booking', '/api/v1/booking-sessions/{sessionId}/attendee-checks', {'sessionId': uid()}, headers=b.vh(), json_body={'nik': nik()}, want=(404, 'NOT_FOUND'), name='attendee-check unknown session')
    call('POST', 'booking', '/api/v1/booking-sessions/{sessionId}/attendee-checks', {'sessionId': b.sid}, headers=o.vh(), json_body={'nik': nik()}, want=(403, 'VISITOR_MISMATCH'), name='attendee-check other visitor')
    call('POST', 'booking', '/api/v1/booking-sessions/{sessionId}/attendee-checks', {'sessionId': 'nope'}, headers=b.vh(), json_body={'nik': nik()}, want=(400, 'VALIDATION_ERROR'), name='attendee-check non-uuid')
    # stream 200: read a few bytes then close
    r = SESS.get(BASE['booking'] + f'/api/v1/booking-sessions/{b.sid}/stream', headers=dict(b.vh(), Accept='text/event-stream'), stream=True, timeout=10)
    rec = dict(part=PART, name='stream ok', svc='booking', method='GET', tpl='/api/v1/booking-sessions/{sessionId}/stream', want_status=200, want_code='', status=r.status_code, code='', rid=r.headers.get('X-Request-Id', ''), ok=r.status_code == 200,
               curl=f"curl -N 'http://localhost:8082/api/v1/booking-sessions/{{sessionId}}/stream' -H 'X-Visitor-Token: …' -H 'X-Device-Id: …'", ts=time.time())
    r.close()
    with open(os.path.join(OUTDIR, PART + '.jsonl'), 'a') as f:
        f.write(json.dumps(rec) + '\n')
    print(('PASS' if rec['ok'] else 'FAIL'), 'stream ok', r.status_code)
    for (h, nm, w) in [({}, 'stream no token', (401, 'UNAUTHORIZED')), (BAD, 'stream garbage token', (401, 'UNAUTHORIZED')), (o.vh(), 'stream other visitor', (403, 'VISITOR_MISMATCH'))]:
        call('GET', 'booking', '/api/v1/booking-sessions/{sessionId}/stream', {'sessionId': b.sid}, headers=h, want=w, name=nm)
    call('GET', 'booking', '/api/v1/booking-sessions/{sessionId}/stream', {'sessionId': uid()}, headers=b.vh(), want=(404, 'NOT_FOUND'), name='stream unknown')
    call('GET', 'booking', '/api/v1/booking-sessions/{sessionId}/stream', {'sessionId': 'nope'}, headers=b.vh(), want=(400, 'VALIDATION_ERROR'), name='stream non-uuid')
    # session cancel
    call('POST', 'booking', '/api/v1/booking-sessions/{sessionId}/cancel', {'sessionId': b.sid}, want=(401, 'UNAUTHORIZED'), name='session cancel no token')
    call('POST', 'booking', '/api/v1/booking-sessions/{sessionId}/cancel', {'sessionId': b.sid}, headers=BAD, want=(401, 'UNAUTHORIZED'), name='session cancel garbage token')
    call('POST', 'booking', '/api/v1/booking-sessions/{sessionId}/cancel', {'sessionId': 'nope'}, headers=b.vh(), want=(400, 'VALIDATION_ERROR'), name='session cancel non-uuid')
    call('POST', 'booking', '/api/v1/booking-sessions/{sessionId}/cancel', {'sessionId': uid()}, headers=b.vh(), want=(404, 'NOT_FOUND'), name='session cancel unknown')
    call('POST', 'booking', '/api/v1/booking-sessions/{sessionId}/cancel', {'sessionId': b.sid}, headers=o.vh(), want=(403, 'VISITOR_MISMATCH'), name='session cancel other visitor')
    b.hold(ev.reg, 1)
    call('POST', 'booking', '/api/v1/booking-sessions/{sessionId}/cancel', {'sessionId': b.sid}, headers=b.vh(), want=(409, 'BOOKING_LOCKED'), name='session cancel with hold')
    call('POST', 'booking', '/api/v1/booking-sessions/{sessionId}/cancel', {'sessionId': o.sid}, headers=o.vh(), want=(204, ''), name='session cancel ok')


def holds():
    ev = new_event('P1 holds', 30, 30)
    h = Buyer(ev, 'h1').admit()
    def hp(body, w, nm, buyer=h, k=None, headers=None):
        hh = buyer.vh({'Idempotency-Key': k or key()})
        hh.update(headers or {})
        return call('POST', 'booking', '/api/v1/bookings', headers=hh, json_body=body, want=w, name=nm)
    ok = lambda **kw: h.body(h.items(ev.vip, 1), **kw)
    hp({'foo': 1}, (400, 'VALIDATION_ERROR'), 'hold unknown field / missing fields')
    call('POST', 'booking', '/api/v1/bookings', headers=h.vh({'Idempotency-Key': 'short'}), json_body=ok(), want=(400, 'VALIDATION_ERROR'), name='hold bad idempotency key')
    call('POST', 'booking', '/api/v1/bookings', headers={'Idempotency-Key': key()}, json_body=ok(), want=(401, 'UNAUTHORIZED'), name='hold no visitor token')
    call('POST', 'booking', '/api/v1/bookings', headers=dict(BAD, **{'Idempotency-Key': key()}), json_body=ok(), want=(401, 'UNAUTHORIZED'), name='hold garbage visitor token')
    b = ok(); b['buyer']['nik'] = '1234'
    hp(b, (422, 'NIK_INVALID'), 'hold buyer NIK invalid')
    b = ok(); b['buyer']['emailConfirmation'] = 'other@sim.test'
    hp(b, (422, 'EMAIL_MISMATCH'), 'hold email mismatch')
    b = ok(); b['buyer']['consent'] = False
    hp(b, (422, 'CONSENT_REQUIRED'), 'hold consent false')
    it = h.items(ev.vip, 1); dup = it + [dict(it[0])]
    hp(h.body(dup), (409, 'ATTENDEE_NIK_DUPLICATE_IN_ORDER'), 'hold duplicate attendee in order')
    hp(h.body([]), (422, 'QUANTITY_INVALID'), 'hold zero items')
    hp(h.body(h.items(ev.vip, 4)), (422, 'QUANTITY_EXCEEDS_LIMIT'), 'hold 4 items (max 3)')
    hp(h.body(h.items(ev.vip, 1) + h.items(ev.reg, 1)), (201, ''), 'hold VIP+REG (mixed allowed) -> ok')  # leaves a booking; gives IDEMPOTENCY base
    # idempotency reuse
    k = key(); h2 = Buyer(ev, 'h2').admit()
    r = hp(h2.body(h2.items(ev.vip, 1)), (201, ''), 'hold ok', buyer=h2, k=k)
    h2.bid = r.d('bookingId')
    hp(h2.body(h2.items(ev.reg, 1)), (409, 'IDEMPOTENCY_KEY_REUSED'), 'hold same key different body', buyer=h2, k=k)
    # attendee already registered: reuse an attendee nik from h2's hold
    h3 = Buyer(ev, 'h3').admit()
    reg_nik = os.environ.get('X', '')
    taken = pg(f"select 1")  # placeholder to keep pg import used
    n_taken = nik()
    h4 = Buyer(ev, 'h4').admit()
    a = h4.items(ev.vip, 1); a[0]['attendee']['nik'] = n_taken
    hp(h4.body(a), (201, ''), 'hold with attendee X (setup)', buyer=h4)
    a2 = h3.items(ev.vip, 1); a2[0]['attendee']['nik'] = n_taken
    hp(h3.body(a2), (409, 'ATTENDEE_NIK_ALREADY_REGISTERED'), 'hold attendee already registered', buyer=h3)
    # buyer NIK limit: h (buyer nik) already holds 2 (vip+reg) -> hold 2 more with same buyer nik
    h5 = Buyer(ev, 'h5').admit(); h5.nikb = h.nikb
    hx = Buyer(ev, 'hx').admit(); hx.nikb = h.nikb; hx.hold(None, 0, body=hx.body(hx.items(ev.vip, 1)), name='setup fill buyer quota')   # same buyer NIK now holds 3
    hp(h5.body(h5.items(ev.vip, 1)), (422, 'BUYER_NIK_LIMIT_REACHED'), 'hold buyer nik over quota', buyer=h5)
    # throttle: repeated refused buyer-NIK attempts
    codes = []
    for i in range(14):
        r = hp(h5.body(h5.items(ev.reg, 1)), (429, 'BUYER_NIK_THROTTLED') if i >= 10 else (422, 'BUYER_NIK_LIMIT_REACHED'), 'hold buyer nik repeated #%d' % i, buyer=h5)
        if r.status == 429: break
        codes.append((r.status, r.code))
    note('repeated buyer-NIK attempts:', codes)
    # session of a different visitor
    hp(h4.body(h4.items(ev.reg, 1)) | {'sessionId': h3.sid}, (403, ''), 'hold with someone else\'s session id', buyer=h4)
    # mixed categories disabled
    nm = new_event('P1 nomix', 5, 5, {'allowMixedCategories': False})
    m = Buyer(nm, 'm1').admit()
    hp(m.body(m.items(nm.vip, 1) + m.items(nm.reg, 1)), (422, 'MIXED_CATEGORIES_NOT_ALLOWED'), 'hold mixed categories off', buyer=m)
    # category unavailable
    sm = new_event('P1 small', 1, 3)
    s1 = Buyer(sm, 's1').admit(); s1.hold(sm.vip, 1)
    s2 = Buyer(sm, 's2').admit()
    hp(s2.body(s2.items(sm.vip, 1)), (409, 'CATEGORY_UNAVAILABLE'), 'hold category unavailable', buyer=s2)
    call('POST', 'booking', '/internal/v1/admin/events/{eventId}/cancel', {'eventId': sm.id}, headers=AH(), want=(200, ''), name='setup cancel event')
    hp(s2.body(s2.items(sm.reg, 1)), (409, 'EVENT_CANCELLED'), 'hold event cancelled', buyer=s2)


def edits():
    ev = new_event('P1 edits', 30, 30)
    b = Buyer(ev, 'e1').admit(); b.hold(ev.reg, 1)
    bid = b.bid
    def put(items, w, nm, version=None, buyer=b, bid_=None, headers=None):
        hh = buyer.vh({'Idempotency-Key': key()}); hh.update(headers or {})
        return call('PUT', 'booking', '/api/v1/bookings/{bookingId}/items', {'bookingId': bid_ or buyer.bid}, headers=hh,
                    json_body={'version': version if version is not None else buyer.version, 'items': items}, want=w, name=nm)
    # validation
    call('PUT', 'booking', '/api/v1/bookings/{bookingId}/items', {'bookingId': bid}, headers=b.vh({'Idempotency-Key': key()}), data='{x', want=(400, 'VALIDATION_ERROR'), name='edit bad json')
    call('PUT', 'booking', '/api/v1/bookings/{bookingId}/items', {'bookingId': 'nope'}, headers=b.vh({'Idempotency-Key': key()}), json_body={'version': 1, 'items': b.items(ev.reg, 1)}, want=(400, 'VALIDATION_ERROR'), name='edit non-uuid')
    put([{'categoryId': ev.reg, 'attendee': {'keepFromItemId': uid()}}], (400, 'VALIDATION_ERROR'), 'edit keepFromItemId unknown')
    keep = b.hold_items[0]['itemId']
    put([{'categoryId': ev.reg, 'attendee': {'keepFromItemId': keep, 'nik': nik()}}], (400, 'VALIDATION_ERROR'), 'edit keepFromItemId + nik')
    put([{'categoryId': ev.reg, 'attendee': {}}], (400, 'VALIDATION_ERROR'), 'edit empty attendee')
    call('PUT', 'booking', '/api/v1/bookings/{bookingId}/items', {'bookingId': bid}, json_body={'version': 1, 'items': b.items(ev.reg, 1)}, headers=dict(BAD, **{'Idempotency-Key': key()}), want=(401, 'UNAUTHORIZED'), name='edit garbage token')
    dsp = Buyer(ev, 'e0').admit(); dsp.hold(ev.reg, 1)   # disposable: an unauthenticated edit may succeed and must not disturb b
    call('PUT', 'booking', '/api/v1/bookings/{bookingId}/items', {'bookingId': dsp.bid}, json_body={'version': dsp.version, 'items': dsp.items(ev.reg, 1)}, headers={'Idempotency-Key': key()}, want=(401, 'UNAUTHORIZED'), name='edit no token')
    call('PUT', 'booking', '/api/v1/bookings/{bookingId}/items', {'bookingId': uid()}, headers=b.vh({'Idempotency-Key': key()}), json_body={'version': 1, 'items': b.items(ev.reg, 1)}, want=(404, 'NOT_FOUND'), name='edit unknown booking')
    o = Buyer(ev, 'e2').admit()
    put(b.items(ev.reg, 1), (403, 'VISITOR_MISMATCH'), 'edit other visitor', buyer=o, bid_=bid, version=1)
    put(b.items(ev.reg, 4), (422, 'QUANTITY_EXCEEDS_LIMIT'), 'edit 4 items', version=b.version)
    put(b.items(ev.reg, 1), (409, 'BOOKING_VERSION_CONFLICT'), 'edit stale version', version=99)
    # ok edit with keepFromItemId
    r = put([{'categoryId': ev.vip, 'attendee': {'keepFromItemId': keep}}], (200, ''), 'edit keep holder, change category')
    b.version = r.d('version'); b.hold_items = r.d('items')
    # dup in order via keep + new nik of same? two keeps of same item
    k0 = b.hold_items[0]['itemId']
    put([{'categoryId': ev.reg, 'attendee': {'keepFromItemId': k0}}, {'categoryId': ev.reg, 'attendee': {'keepFromItemId': k0}}], (409, 'ATTENDEE_NIK_DUPLICATE_IN_ORDER'), 'edit keepFromItemId twice')
    # attendee already registered: nik held by another booking
    ob = Buyer(ev, 'e3').admit(); ob.hold(ev.reg, 1)
    taken = pg(f"select 1") and None
    # we know other's nik because we generated it in Buyer.items: re-hold with known nik
    ob2 = Buyer(ev, 'e4').admit(); nk = nik(); it = ob2.items(ev.reg, 1); it[0]['attendee']['nik'] = nk; ob2.hold(None, 0, body=ob2.body(it))
    put([{'categoryId': ev.reg, 'attendee': {'nik': nk, 'fullName': 'Dup Orang'}}], (409, 'ATTENDEE_NIK_ALREADY_REGISTERED'), 'edit attendee already registered')
    # category unavailable
    sm = new_event('P1 edits small', 1, 5)
    c1 = Buyer(sm, 'c1').admit(); c1.hold(sm.reg, 1)
    c2 = Buyer(sm, 'c2').admit(); c2.hold(sm.vip, 1)   # takes the only VIP
    put([{'categoryId': sm.vip, 'attendee': {'keepFromItemId': c1.hold_items[0]['itemId']}}], (409, 'CATEGORY_UNAVAILABLE'), 'edit to sold-out category', buyer=c1)
    # edit limit
    el = Buyer(ev, 'e5').admit(); el.hold(ev.reg, 1)
    last = None
    for i in range(6):
        it = [{'categoryId': ev.reg, 'attendee': {'keepFromItemId': el.hold_items[0]['itemId']}}]
        r = put(it, (200, '') if i < 3 else (409, 'EDIT_LIMIT_REACHED'), 'edit #%d' % (i + 1), buyer=el)
        if r.status == 200:
            el.version = r.d('version'); el.hold_items = r.d('items')
        else:
            break
    # locked: payment attempt started
    lk = Buyer(ev, 'e6').admit(); lk.hold(ev.reg, 1); lk.pay()
    put([{'categoryId': ev.reg, 'attendee': {'keepFromItemId': lk.hold_items[0]['itemId']}}], (409, 'BOOKING_LOCKED'), 'edit after payment attempt', buyer=lk)
    # cancel rows
    call('POST', 'booking', '/api/v1/bookings/{bookingId}/cancel', {'bookingId': lk.bid}, headers=lk.vh(), json_body={}, want=(409, 'BOOKING_LOCKED'), name='cancel after payment attempt')
    call('POST', 'booking', '/api/v1/bookings/{bookingId}/cancel', {'bookingId': b.bid}, headers=BAD, json_body={}, want=(401, 'UNAUTHORIZED'), name='cancel garbage token')
    dc = Buyer(ev, 'e0c').admit(); dc.hold(ev.reg, 1)
    call('POST', 'booking', '/api/v1/bookings/{bookingId}/cancel', {'bookingId': dc.bid}, json_body={}, want=(401, 'UNAUTHORIZED'), name='cancel no token')
    call('POST', 'booking', '/api/v1/bookings/{bookingId}/cancel', {'bookingId': 'nope'}, headers=b.vh(), json_body={}, want=(400, 'VALIDATION_ERROR'), name='cancel non-uuid')
    call('POST', 'booking', '/api/v1/bookings/{bookingId}/cancel', {'bookingId': uid()}, headers=b.vh(), json_body={}, want=(404, 'NOT_FOUND'), name='cancel unknown')
    call('POST', 'booking', '/api/v1/bookings/{bookingId}/cancel', {'bookingId': b.bid}, headers=o.vh(), json_body={}, want=(403, 'VISITOR_MISMATCH'), name='cancel other visitor')
    call('POST', 'booking', '/api/v1/bookings/{bookingId}/cancel', {'bookingId': b.bid}, headers=b.vh(), json_body={}, want=(200, ''), name='cancel ok')
    # get booking rows
    call('GET', 'booking', '/api/v1/bookings/{bookingId}', {'bookingId': b.bid}, headers=b.vh(), want=(200, ''), name='get booking')
    call('GET', 'booking', '/api/v1/bookings/{bookingId}', {'bookingId': b.bid}, want=(401, 'UNAUTHORIZED'), name='get booking no token')
    call('GET', 'booking', '/api/v1/bookings/{bookingId}', {'bookingId': b.bid}, headers=BAD, want=(401, 'UNAUTHORIZED'), name='get booking garbage token')
    call('GET', 'booking', '/api/v1/bookings/{bookingId}', {'bookingId': 'nope'}, headers=b.vh(), want=(400, 'VALIDATION_ERROR'), name='get booking non-uuid')
    call('GET', 'booking', '/api/v1/bookings/{bookingId}', {'bookingId': uid()}, headers=b.vh(), want=(404, 'NOT_FOUND'), name='get booking unknown')
    call('GET', 'booking', '/api/v1/bookings/{bookingId}', {'bookingId': b.bid}, headers=o.vh(), want=(403, 'VISITOR_MISMATCH'), name='get booking other visitor')


def tickets_history():
    ev = new_event('P1 tickets', 10, 10)
    b = Buyer(ev, 't1').admit(); b.hold(ev.reg, 1)
    o = Buyer(ev, 't2').admit()
    call('GET', 'booking', '/api/v1/bookings/{bookingId}/tickets', {'bookingId': b.bid}, headers=b.vh(), want=(409, 'BOOKING_NOT_PAYABLE'), name='tickets before paid')
    b.pay(); gateway(b.order, 'settlement'); assert b.wait_booking('PAID'), 'not paid'
    call('GET', 'booking', '/api/v1/bookings/{bookingId}/tickets', {'bookingId': b.bid}, headers=b.vh(), want=(200, ''), name='tickets ok')
    call('GET', 'booking', '/api/v1/bookings/{bookingId}/tickets', {'bookingId': b.bid}, want=(401, 'UNAUTHORIZED'), name='tickets no token')
    call('GET', 'booking', '/api/v1/bookings/{bookingId}/tickets', {'bookingId': b.bid}, headers=BAD, want=(401, 'UNAUTHORIZED'), name='tickets garbage token')
    call('GET', 'booking', '/api/v1/bookings/{bookingId}/tickets', {'bookingId': 'nope'}, headers=b.vh(), want=(400, 'VALIDATION_ERROR'), name='tickets non-uuid')
    call('GET', 'booking', '/api/v1/bookings/{bookingId}/tickets', {'bookingId': uid()}, headers=b.vh(), want=(404, 'NOT_FOUND'), name='tickets unknown')
    call('GET', 'booking', '/api/v1/bookings/{bookingId}/tickets', {'bookingId': b.bid}, headers=o.vh(), want=(403, 'VISITOR_MISMATCH'), name='tickets other visitor')
    at, rt, _ = login('hist-%d%d@sim.test' % (os.getpid(), random.randint(0, 9999)))
    call('GET', 'booking', '/api/v1/me/bookings', headers={'Authorization': 'Bearer ' + at}, want=(200, ''), name='me/bookings ok')
    call('GET', 'booking', '/api/v1/me/bookings', want=(401, 'UNAUTHORIZED'), name='me/bookings no token')
    call('GET', 'booking', '/api/v1/me/bookings', headers={'Authorization': 'Bearer garbage'}, want=(401, 'UNAUTHORIZED'), name='me/bookings garbage')


if __name__ == '__main__':
    run_parts([events_and_facets, sessions, holds, edits, tickets_history])
