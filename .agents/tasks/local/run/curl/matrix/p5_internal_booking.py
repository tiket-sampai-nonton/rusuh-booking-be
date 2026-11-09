"""Part 5: internal-booking.yaml (admin events + service-to-service routes, with service tokens signed like the services do)."""
import os; os.environ.setdefault('PART', 'p5')
from mlib import *
AE = '/internal/v1/admin/events'
AEI = AE + '/{eventId}'
PNG = os.path.join(HERE, '..', 'pixel.png')


def event_body(name='P5', sales='-1 minute', **kw):
    def iso(d):
        return subprocess.check_output(['date', '-u', '-d', d, '+%FT%TZ']).decode().strip()
    b = {'name': name, 'slug': 'p5-%d' % time.time_ns(), 'venue': 'Sabuga', 'city': 'Bandung', 'genres': ['rock'], 'startsAt': iso('+30 days'),
         'salesStartsAt': iso(sales), 'salesEndsAt': iso('+7 days'), 'maxTicketsPerOrder': 3, 'maxTicketsPerBuyerNik': 3, 'allowMixedCategories': True,
         'description': [{'type': 'paragraph', 'children': [{'text': name}]}],
         'categories': [{'code': 'VIP', 'name': 'VIP', 'priceMinor': 250000000, 'capacity': 5}, {'code': 'REG', 'name': 'Regular', 'priceMinor': 90000000, 'capacity': 5}]}
    b.update(kw)
    return b


def admin_events():
    A = AH()
    pt, _, _ = login('p5-plain-%d%d@sim.test' % (os.getpid(), random.randint(0, 9999)))
    NA = {'Authorization': 'Bearer ' + pt}
    # a visitor token (wrong scope) and service tokens must not pass as admin
    ev0 = new_event('P5 vis', 3, 3); vb = Buyer(ev0, 'v'); vb.join()
    VIS = {'Authorization': 'Bearer ' + vb.vt}
    SVC = SH(queue_svc_token())
    # create
    r = call('POST', 'booking', AE, headers=A, json_body=event_body('P5 draft', '+2 days'), want=(201, ''), name='admin create')
    eid = r.d('eventId')
    call('POST', 'booking', AE, json_body=event_body(), want=(401, 'UNAUTHORIZED'), name='admin create no token')
    call('POST', 'booking', AE, headers={'Authorization': 'Bearer garbage'}, json_body=event_body(), want=(401, 'UNAUTHORIZED'), name='admin create garbage token')
    call('POST', 'booking', AE, headers=NA, json_body=event_body(), want=(401, 'UNAUTHORIZED'), name='admin create non-admin user')
    call('POST', 'booking', AE, headers=VIS, json_body=event_body(), want=(401, 'UNAUTHORIZED'), name='admin create visitor token')
    call('POST', 'booking', AE, headers=SVC, json_body=event_body(), want=(401, 'UNAUTHORIZED'), name='admin create service token')
    call('POST', 'booking', AE, headers=A, json_body={'name': 'x'}, want=(0, ''), name='admin create invalid body (observe)')
    # get
    call('GET', 'booking', AEI, {'eventId': eid}, headers=A, want=(200, ''), name='admin get')
    call('GET', 'booking', AEI, {'eventId': 'nope'}, headers=A, want=(400, ''), name='admin get non-uuid')
    call('GET', 'booking', AEI, {'eventId': uid()}, headers=A, want=(404, ''), name='admin get unknown')
    call('GET', 'booking', AEI, {'eventId': eid}, want=(401, ''), name='admin get no token')
    call('GET', 'booking', AEI, {'eventId': eid}, headers=NA, want=(401, ''), name='admin get non-admin')
    # patch
    call('PATCH', 'booking', AEI, {'eventId': eid}, headers=A, json_body={'selectionTtlSeconds': 120, 'paymentTtlSeconds': 300, 'genres': ['jazz']}, want=(200, ''), name='admin patch ok')
    call('PATCH', 'booking', AEI, {'eventId': eid}, headers=A, json_body={'paymentTtlSeconds': 60}, want=(400, ''), name='admin patch ttl below min')
    call('PATCH', 'booking', AEI, {'eventId': eid}, headers=A, data='{x', want=(400, ''), name='admin patch bad json')
    call('PATCH', 'booking', AEI, {'eventId': eid}, json_body={'maxEdits': 1}, want=(401, ''), name='admin patch no token')
    call('PATCH', 'booking', AEI, {'eventId': eid}, headers=NA, json_body={'maxEdits': 1}, want=(401, ''), name='admin patch non-admin')
    call('PATCH', 'booking', AEI, {'eventId': uid()}, headers=A, json_body={'maxEdits': 1}, want=(0, ''), name='admin patch unknown (observe)')
    on = new_event('P5 onsale', 3, 3)
    for nm, b in [('maxTicketsPerOrder', {'maxTicketsPerOrder': 2}), ('allowMixedCategories', {'allowMixedCategories': False}), ('maxTicketsPerBuyerNik', {'maxTicketsPerBuyerNik': 2}), ('maxEdits', {'maxEdits': 1, 'editEnabled': False})]:
        call('PATCH', 'booking', AEI, {'eventId': on.id}, headers=A, json_body=b, want=(409, ''), name='admin patch on-sale ' + nm)
    # publish / unpublish / cancel
    for act, st in [('publish', 200)]:
        call('POST', 'booking', AEI + '/publish', {'eventId': eid}, headers=A, want=(200, ''), name='admin publish')
    call('POST', 'booking', AEI + '/publish', {'eventId': 'nope'}, headers=A, want=(400, ''), name='admin publish non-uuid')
    call('POST', 'booking', AEI + '/publish', {'eventId': uid()}, headers=A, want=(404, ''), name='admin publish unknown')
    call('POST', 'booking', AEI + '/publish', {'eventId': eid}, want=(401, ''), name='admin publish no token')
    call('POST', 'booking', AEI + '/publish', {'eventId': eid}, headers=NA, want=(401, ''), name='admin publish non-admin')
    call('POST', 'booking', AEI + '/unpublish', {'eventId': eid}, headers=A, want=(200, ''), name='admin unpublish (sales not open)')
    call('POST', 'booking', AEI + '/unpublish', {'eventId': 'nope'}, headers=A, want=(400, ''), name='admin unpublish non-uuid')
    call('POST', 'booking', AEI + '/unpublish', {'eventId': uid()}, headers=A, want=(404, ''), name='admin unpublish unknown')
    call('POST', 'booking', AEI + '/unpublish', {'eventId': eid}, want=(401, ''), name='admin unpublish no token')
    call('POST', 'booking', AEI + '/unpublish', {'eventId': eid}, headers=NA, want=(401, ''), name='admin unpublish non-admin')
    call('POST', 'booking', AEI + '/unpublish', {'eventId': on.id}, headers=A, want=(409, 'EVENT_RULES_LOCKED'), name='admin unpublish after sale open')
    call('POST', 'booking', AEI + '/cancel', {'eventId': 'nope'}, headers=A, want=(400, ''), name='admin cancel non-uuid')
    call('POST', 'booking', AEI + '/cancel', {'eventId': uid()}, headers=A, want=(404, ''), name='admin cancel unknown')
    call('POST', 'booking', AEI + '/cancel', {'eventId': eid}, want=(401, ''), name='admin cancel no token')
    call('POST', 'booking', AEI + '/cancel', {'eventId': eid}, headers=NA, want=(401, ''), name='admin cancel non-admin')
    call('POST', 'booking', AEI + '/cancel', {'eventId': eid}, headers=A, want=(200, ''), name='admin cancel ok')
    # images
    IM = AEI + '/images'
    call('POST', 'booking', IM, {'eventId': on.id}, headers=A, params={'kind': 'banner'}, files={'file': ('pixel.png', open(PNG, 'rb'), 'image/png')}, want=(201, ''), name='admin image ok')
    call('POST', 'booking', IM, {'eventId': on.id}, headers=A, params={'kind': 'banner'}, files={'file': ('x.txt', b'not an image', 'text/plain')}, want=(400, 'VALIDATION_ERROR'), name='admin image not an image')
    call('POST', 'booking', IM, {'eventId': on.id}, headers=A, params={'kind': 'nope'}, files={'file': ('pixel.png', open(PNG, 'rb'), 'image/png')}, want=(400, 'VALIDATION_ERROR'), name='admin image bad kind')
    call('POST', 'booking', IM, {'eventId': on.id}, headers=A, params={'kind': 'banner'}, want=(400, 'VALIDATION_ERROR'), name='admin image no file')
    call('POST', 'booking', IM, {'eventId': on.id}, headers=A, params={'kind': 'banner'}, files={'file': ('big.png', b'\x89PNG\r\n\x1a\n' + b'0' * (5 * 1024 * 1024 + 10), 'image/png')}, want=(400, 'VALIDATION_ERROR'), name='admin image >5MB')
    call('POST', 'booking', IM, {'eventId': uid()}, headers=A, params={'kind': 'banner'}, files={'file': ('pixel.png', open(PNG, 'rb'), 'image/png')}, want=(404, 'NOT_FOUND'), name='admin image unknown event')
    call('POST', 'booking', IM, {'eventId': on.id}, params={'kind': 'banner'}, files={'file': ('pixel.png', open(PNG, 'rb'), 'image/png')}, want=(401, ''), name='admin image no token')
    call('POST', 'booking', IM, {'eventId': on.id}, headers=NA, params={'kind': 'banner'}, files={'file': ('pixel.png', open(PNG, 'rb'), 'image/png')}, want=(401, ''), name='admin image non-admin')
    call('POST', 'booking', IM, {'eventId': 'nope'}, headers=A, params={'kind': 'banner'}, files={'file': ('pixel.png', open(PNG, 'rb'), 'image/png')}, want=(0, ''), name='admin image non-uuid (observe)')


def service_routes():
    ev = new_event('P5 svc', 10, 10)
    QT, PT = SH(queue_svc_token()), SH(payment_svc_token())
    bad_variants = [('no token', {}), ('garbage', {'Authorization': 'Bearer garbage'}),
                    ('expired', SH(queue_svc_token(iat=int(time.time()) - 4000, exp=int(time.time()) - 3000))),
                    ('ttl>5m', SH(queue_svc_token(ttl=3600))),
                    ('wrong issuer (payment token on queue route)', PT), ('wrong issuer (queue token on payment route)', QT)]
    # supply
    SUP = '/internal/v1/events/{eventId}/supply'
    call('GET', 'booking', SUP, {'eventId': ev.id}, headers=QT, want=(200, ''), name='supply ok')
    call('GET', 'booking', SUP, {'eventId': uid()}, headers=QT, want=(404, 'NOT_FOUND'), name='supply unknown event')
    d = new_event('P5 supply draft', 3, 3, publish=False)
    call('GET', 'booking', SUP, {'eventId': d.id}, headers=QT, want=(404, 'NOT_FOUND'), name='supply draft event')
    call('GET', 'booking', SUP, {'eventId': 'nope'}, headers=QT, want=(0, ''), name='supply non-uuid (observe)')
    for nm, h in bad_variants[:-1]:
        call('GET', 'booking', SUP, {'eventId': ev.id}, headers=h, want=(401, 'UNAUTHORIZED'), name='supply ' + nm)
    # allocations batch + release
    AB, AR = '/internal/v1/allocations:batch', '/internal/v1/allocations/{allocationId}:release'
    qe, vid = uid(), uid()
    r = call('POST', 'booking', AB, headers=QT, json_body={'eventId': ev.id, 'entries': [{'queueEntryId': qe, 'seq': 1, 'visitorId': vid, 'units': 1}]}, want=(200, ''), name='allocations batch ok')
    note('batch response:', json.dumps(r.data)[:300])
    alloc = None
    for key_ in ('granted', 'allocations', 'results', 'items'):
        v = r.d(key_)
        if isinstance(v, list) and v:
            alloc = v[0].get('allocationId'); break
    for nm, h in bad_variants[:-1]:
        call('POST', 'booking', AB, headers=h, json_body={'eventId': ev.id, 'entries': []}, want=(401, 'UNAUTHORIZED'), name='allocations batch ' + nm)
    call('POST', 'booking', AB, headers=QT, json_body={'eventId': 'x', 'entries': []}, want=(0, ''), name='allocations batch invalid (observe)')
    if alloc:
        call('POST', 'booking', AR, {'allocationId': alloc}, headers=QT, json_body={'reason': 'QUEUE_ENTRY_GONE'}, want=(200, ''), name='allocation release ok')
    else:
        note('NO allocationId found in batch response')
    for nm, h in bad_variants[:-1]:
        call('POST', 'booking', AR, {'allocationId': uid()}, headers=h, json_body={'reason': 'QUEUE_ENTRY_GONE'}, want=(401, 'UNAUTHORIZED'), name='allocation release ' + nm)
    call('POST', 'booking', AR, {'allocationId': uid()}, headers=QT, json_body={'reason': 'QUEUE_ENTRY_GONE'}, want=(0, ''), name='allocation release unknown (observe)')
    call('POST', 'booking', AR, {'allocationId': 'nope'}, headers=QT, json_body={'reason': 'QUEUE_ENTRY_GONE'}, want=(0, ''), name='allocation release non-uuid (observe)')
    call('POST', 'booking', AR, {'allocationId': uid()}, headers=QT, json_body={'reason': 'NOPE'}, want=(0, ''), name='allocation release bad reason (observe)')
    # payable / payment-lock (Payment -> Booking) and deadline-extension
    b = Buyer(ev, 'b').admit(); b.hold(ev.reg, 1)
    PB, PL, DX = '/internal/v1/bookings/{bookingId}/payable', '/internal/v1/bookings/{bookingId}/payment-lock', '/internal/v1/bookings/{bookingId}/deadline-extension'
    call('GET', 'booking', PB, {'bookingId': b.bid}, headers=PT, want=(200, ''), name='payable ok')
    for nm, h in [('no token', {}), ('garbage', {'Authorization': 'Bearer garbage'}), ('queue token (wrong caller)', QT), ('expired', SH(payment_svc_token(iat=int(time.time()) - 4000, exp=int(time.time()) - 3000))), ('ttl>5m', SH(payment_svc_token(ttl=3600)))]:
        call('GET', 'booking', PB, {'bookingId': b.bid}, headers=h, want=(401, 'UNAUTHORIZED'), name='payable ' + nm)
        call('POST', 'booking', PL, {'bookingId': b.bid}, headers=h, json_body={'attemptKey': 'k-' + uid()}, want=(401, 'UNAUTHORIZED'), name='payment-lock ' + nm)
    call('GET', 'booking', PB, {'bookingId': uid()}, headers=PT, want=(0, ''), name='payable unknown (observe)')
    call('GET', 'booking', PB, {'bookingId': 'nope'}, headers=PT, want=(0, ''), name='payable non-uuid (observe)')
    call('POST', 'booking', PL, {'bookingId': b.bid}, headers=PT, json_body={'attemptKey': 'k-' + uid()}, want=(200, ''), name='payment-lock ok')
    call('POST', 'booking', PL, {'bookingId': b.bid}, headers=PT, json_body={}, want=(0, ''), name='payment-lock missing attemptKey (observe)')
    call('POST', 'booking', PL, {'bookingId': uid()}, headers=PT, json_body={'attemptKey': 'k-' + uid()}, want=(0, ''), name='payment-lock unknown (observe)')
    call('POST', 'booking', DX, {'bookingId': b.bid}, headers=QT, json_body={'reason': 'PROVIDER_UNAVAILABLE', 'seconds': 60}, want=(200, ''), name='deadline-extension with queue token')
    call('POST', 'booking', DX, {'bookingId': b.bid}, headers=PT, json_body={'reason': 'PROVIDER_UNAVAILABLE', 'seconds': 60}, want=(0, ''), name='deadline-extension with payment token (observe)')
    for nm, h in [('no token', {}), ('garbage', {'Authorization': 'Bearer garbage'}), ('expired', SH(queue_svc_token(iat=int(time.time()) - 4000, exp=int(time.time()) - 3000))), ('ttl>5m', SH(queue_svc_token(ttl=3600)))]:
        call('POST', 'booking', DX, {'bookingId': b.bid}, headers=h, json_body={'reason': 'PROVIDER_UNAVAILABLE', 'seconds': 60}, want=(401, 'UNAUTHORIZED'), name='deadline-extension ' + nm)
    call('POST', 'booking', DX, {'bookingId': b.bid}, headers=QT, json_body={'reason': 'PROVIDER_UNAVAILABLE', 'seconds': 9999}, want=(0, ''), name='deadline-extension seconds out of range (observe)')
    call('POST', 'booking', DX, {'bookingId': b.bid}, headers=QT, json_body={'reason': 'NOPE', 'seconds': 60}, want=(0, ''), name='deadline-extension bad reason (observe)')
    call('POST', 'booking', DX, {'bookingId': uid()}, headers=QT, json_body={'reason': 'PROVIDER_UNAVAILABLE', 'seconds': 60}, want=(0, ''), name='deadline-extension unknown booking (observe)')


if __name__ == '__main__':
    run_parts([admin_events, service_routes])
