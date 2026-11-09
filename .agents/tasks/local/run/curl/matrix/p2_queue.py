"""Part 2: queue.yaml (public queue routes + admin queue-settings). Rate-limit and outage rows live in p9/p8."""
import os; os.environ.setdefault('PART', 'p2')
from mlib import *
BAD = {'X-Visitor-Token': 'not-a-token'}
QE = '/api/v1/events/{eventId}/queue-entries'


def join_rows():
    ev = new_event('P2 join', 5, 5, capacity=1)
    a = Buyer(ev, 'a')
    a.join(want=(201, ''), name='join new', quiet=False)
    a.join(want=(200, ''), name='join again same token', quiet=False, extra_h={'X-Visitor-Token': a.vt})
    # ACCOUNT_ALREADY_IN_QUEUE: account joins with another anon visitor's token on same event
    at, rt, _ = login('q-acct-%d%d@sim.test' % (os.getpid(), random.randint(0, 9999)))
    ev2 = new_event('P2 acct', 5, 5)
    x = Buyer(ev2, 'x', bearer=at); x.join(want=(201, ''), name='join with bearer')
    anon = Buyer(ev2, 'anon'); anon.join()
    call('POST', 'queue', QE, {'eventId': ev2.id}, headers={'X-Client-Type': 'mobile', 'X-Device-Id': anon.dev, 'X-Visitor-Token': anon.vt, 'Authorization': 'Bearer ' + at},
         want=(409, 'ACCOUNT_ALREADY_IN_QUEUE'), name='join account already in queue')
    # EVENT_NOT_ON_SALE before sales start
    fut = new_event('P2 future', 5, 5, sales_start='+2 days')
    call('POST', 'queue', QE, {'eventId': fut.id}, headers={'X-Client-Type': 'mobile', 'X-Device-Id': 'd-fut'}, want=(409, 'EVENT_NOT_ON_SALE'), name='join before sales open')
    # QUEUE_NOT_CONFIGURED: published but no queue-settings
    nc = new_event('P2 noqueue', 5, 5, queue=False)
    call('POST', 'queue', QE, {'eventId': nc.id}, headers={'X-Client-Type': 'mobile', 'X-Device-Id': 'd-nc'}, want=(409, 'QUEUE_NOT_CONFIGURED'), name='join queue not configured')
    # unknown event
    call('POST', 'queue', QE, {'eventId': uid()}, headers={'X-Client-Type': 'mobile', 'X-Device-Id': 'd-unk'}, want=(0, ''), name='join unknown event (observe)')
    call('POST', 'queue', QE, {'eventId': 'nope'}, headers={'X-Client-Type': 'mobile', 'X-Device-Id': 'd-bad'}, want=(0, ''), name='join non-uuid event (observe)')
    # status document
    for _ in range(40):   # the status document is published by the queue worker, not at event setup
        r = call('GET', 'queue', '/api/v1/queue-status/{eventId}', {'eventId': ev.id}, want=(200, ''), name='status doc', quiet=True)
        if r.status == 200:
            break
        time.sleep(0.5)
    print(('PASS' if r.ok else 'FAIL'), '[p2] status doc:', r.status)
    call('GET', 'queue', '/api/v1/queue-status/{eventId}', {'eventId': uid()}, want=(404, 'NOT_FOUND'), name='status doc unknown event')
    call('GET', 'queue', '/api/v1/queue-status/{eventId}', {'eventId': 'nope'}, want=(0, ''), name='status doc non-uuid (observe)')


def me_claim_leave():
    ev = new_event('P2 me', 5, 5, capacity=1)
    a = Buyer(ev, 'a'); a.join(); a.wait_admitted()
    b = Buyer(ev, 'b'); b.join()   # waits (1 slot)
    call('GET', 'queue', '/api/v1/queue-entries/me', params={'eventId': ev.id}, headers=a.vh(), want=(200, ''), name='me ok')
    call('GET', 'queue', '/api/v1/queue-entries/me', params={'eventId': ev.id}, headers=BAD, want=(401, 'VISITOR_TOKEN_INVALID'), name='me garbage token')
    call('GET', 'queue', '/api/v1/queue-entries/me', params={'eventId': ev.id}, want=(401, 'VISITOR_TOKEN_INVALID'), name='me no token')
    call('GET', 'queue', '/api/v1/queue-entries/me', params={'eventId': ev.id}, headers=dict(a.vh(), **{'X-Device-Id': 'another-phone'}), want=(401, 'VISITOR_TOKEN_INVALID'), name='me other device')
    other = new_event('P2 me other', 2, 2)
    call('GET', 'queue', '/api/v1/queue-entries/me', params={'eventId': other.id}, headers=a.vh(), want=(404, 'NOT_FOUND'), name='me never joined this event')
    call('GET', 'queue', '/api/v1/queue-entries/me', headers=a.vh(), want=(0, ''), name='me missing eventId (observe)')
    # heartbeat
    call('POST', 'queue', '/api/v1/queue-entries/me/heartbeat', params={'eventId': ev.id}, headers=a.vh(), want=(200, ''), name='heartbeat ok')
    call('POST', 'queue', '/api/v1/queue-entries/me/heartbeat', params={'eventId': ev.id}, headers=BAD, want=(401, 'VISITOR_TOKEN_INVALID'), name='heartbeat garbage token')
    call('POST', 'queue', '/api/v1/queue-entries/me/heartbeat', params={'eventId': ev.id}, want=(401, 'VISITOR_TOKEN_INVALID'), name='heartbeat no token')
    call('POST', 'queue', '/api/v1/queue-entries/me/heartbeat', params={'eventId': other.id}, headers=a.vh(), want=(0, ''), name='heartbeat never joined (observe)')
    # claim
    call('POST', 'queue', '/api/v1/queue-entries/{entryId}/claim', {'entryId': b.entry}, params={'eventId': ev.id}, headers=b.vh(), want=(409, 'NOT_ADMITTED'), name='claim while WAITING')
    call('POST', 'queue', '/api/v1/queue-entries/{entryId}/claim', {'entryId': a.entry}, params={'eventId': ev.id}, headers=BAD, want=(401, 'VISITOR_TOKEN_INVALID'), name='claim garbage token')
    call('POST', 'queue', '/api/v1/queue-entries/{entryId}/claim', {'entryId': a.entry}, params={'eventId': ev.id}, want=(401, 'VISITOR_TOKEN_INVALID'), name='claim no token')
    call('POST', 'queue', '/api/v1/queue-entries/{entryId}/claim', {'entryId': uid()}, params={'eventId': ev.id}, headers=a.vh(), want=(404, 'NOT_FOUND'), name='claim absent entry')
    call('POST', 'queue', '/api/v1/queue-entries/{entryId}/claim', {'entryId': b.entry}, params={'eventId': ev.id}, headers=a.vh(), want=(404, 'NOT_FOUND'), name='claim foreign entry')
    call('POST', 'queue', '/api/v1/queue-entries/{entryId}/claim', {'entryId': a.entry}, params={'eventId': other.id}, headers=a.vh(), want=(404, 'NOT_FOUND'), name='claim never joined this event')
    a.claim(want=(200, ''), name='claim ok', quiet=False)
    # leave
    call('DELETE', 'queue', '/api/v1/queue-entries/{entryId}', {'entryId': a.entry}, params={'eventId': ev.id}, headers=a.vh(), want=(409, 'ENTRY_CLAIMED'), name='leave claimed entry')
    call('DELETE', 'queue', '/api/v1/queue-entries/{entryId}', {'entryId': a.entry}, params={'eventId': ev.id}, headers=BAD, want=(401, 'VISITOR_TOKEN_INVALID'), name='leave garbage token')
    call('DELETE', 'queue', '/api/v1/queue-entries/{entryId}', {'entryId': a.entry}, params={'eventId': ev.id}, want=(401, 'VISITOR_TOKEN_INVALID'), name='leave no token')
    call('DELETE', 'queue', '/api/v1/queue-entries/{entryId}', {'entryId': uid()}, params={'eventId': ev.id}, headers=b.vh(), want=(404, 'NOT_FOUND'), name='leave absent entry')
    call('DELETE', 'queue', '/api/v1/queue-entries/{entryId}', {'entryId': a.entry}, params={'eventId': ev.id}, headers=b.vh(), want=(404, 'NOT_FOUND'), name='leave foreign entry')
    call('DELETE', 'queue', '/api/v1/queue-entries/{entryId}', {'entryId': a.entry}, params={'eventId': other.id}, headers=a.vh(), want=(404, 'NOT_FOUND'), name='leave never joined this event')
    call('DELETE', 'queue', '/api/v1/queue-entries/{entryId}', {'entryId': b.entry}, params={'eventId': ev.id}, headers=b.vh(), want=(204, ''), name='leave waiting entry ok')
    # heartbeat ENTRY_NOT_FOUND: entry ended (left)
    call('POST', 'queue', '/api/v1/queue-entries/me/heartbeat', params={'eventId': ev.id}, headers=b.vh(), want=(404, 'ENTRY_NOT_FOUND'), name='heartbeat after leaving')
    call('GET', 'queue', '/api/v1/queue-entries/me', params={'eventId': ev.id}, headers=b.vh(), want=(0, ''), name='me after leaving (observe)')
    call('POST', 'queue', '/api/v1/queue-entries/{entryId}/claim', {'entryId': b.entry}, params={'eventId': ev.id}, headers=b.vh(), want=(404, 'NOT_FOUND'), name='claim after leaving')


def sold_out():
    ev = new_event('P2 soldout', 1, 1, capacity=3)
    b = Buyer(ev, 'b').admit()
    w = Buyer(ev, 'w'); w.join(); w.wait_admitted()   # admitted but not yet claimed -> claim after sell-out
    # b takes both tickets
    b.hold(None, 0, body=b.body(b.items(ev.vip, 1) + b.items(ev.reg, 1)))
    b.pay(); gateway(b.order, 'settlement'); b.wait_booking('PAID')
    for _ in range(40):
        r = call('GET', 'queue', '/api/v1/queue-status/{eventId}', {'eventId': ev.id}, want=(200, ''), name='status doc after sellout', quiet=True)
        if r.d('soldOut'):
            break
        time.sleep(0.5)
    time.sleep(6)
    call('POST', 'queue', '/api/v1/queue-entries/{entryId}/claim', {'entryId': w.entry}, params={'eventId': ev.id}, headers=w.vh(), want=(410, 'EVENT_SOLD_OUT'), name='claim after sold out')
    call('POST', 'queue', QE, {'eventId': ev.id}, headers={'X-Client-Type': 'mobile', 'X-Device-Id': 'd-so'}, want=(410, 'EVENT_SOLD_OUT'), name='join after sold out')


def admin_settings():
    A = AH()
    ev = new_event('P2 settings', 5, 5, queue=False, sales_start='+2 days')   # published, not yet on sale
    QS = '/internal/v1/admin/events/{eventId}/queue-settings'
    call('GET', 'queue', QS, {'eventId': ev.id}, headers=A, want=(404, 'QUEUE_NOT_CONFIGURED'), name='settings get before configured')
    call('GET', 'queue', QS, {'eventId': 'nope'}, headers=A, want=(400, 'VALIDATION_ERROR'), name='settings get non-uuid')
    call('GET', 'queue', QS, {'eventId': ev.id}, want=(401, 'UNAUTHORIZED'), name='settings get no token')
    call('GET', 'queue', QS, {'eventId': ev.id}, headers={'Authorization': 'Bearer garbage'}, want=(401, 'UNAUTHORIZED'), name='settings get garbage token')
    pt, _, _ = login('q-plain-%d%d@sim.test' % (os.getpid(), random.randint(0, 9999)))
    call('GET', 'queue', QS, {'eventId': ev.id}, headers={'Authorization': 'Bearer ' + pt}, want=(401, 'UNAUTHORIZED'), name='settings get non-admin')
    call('PUT', 'queue', QS, {'eventId': ev.id}, headers=A, json_body={'activeSlotCapacity': 5}, want=(201, ''), name='settings put create')
    call('PUT', 'queue', QS, {'eventId': ev.id}, headers=A, json_body={'activeSlotCapacity': 7, 'pollNearMs': 2000}, want=(200, ''), name='settings put update')
    call('GET', 'queue', QS, {'eventId': ev.id}, headers=A, want=(200, ''), name='settings get')
    for nm, b in [('missing capacity', {}), ('unknown field', {'activeSlotCapacity': 5, 'foo': 1}), ('out of range', {'activeSlotCapacity': 0}), ('wrong type', {'activeSlotCapacity': 'x'}), ('cross-field', {'activeSlotCapacity': 7, 'pollNearMs': 5000, 'pollMidMs': 2000})]:
        call('PUT', 'queue', QS, {'eventId': ev.id}, headers=A, json_body=b, want=(400, 'VALIDATION_ERROR'), name='settings put ' + nm)
    call('PUT', 'queue', QS, {'eventId': ev.id}, headers=A, data='{x', want=(400, 'VALIDATION_ERROR'), name='settings put bad json')
    call('PUT', 'queue', QS, {'eventId': ev.id}, json_body={'activeSlotCapacity': 5}, want=(401, 'UNAUTHORIZED'), name='settings put no token')
    call('PUT', 'queue', QS, {'eventId': uid()}, headers=A, json_body={'activeSlotCapacity': 5}, want=(404, 'NOT_FOUND'), name='settings put unknown event')
    dr = new_event('P2 draft', 5, 5, publish=False)
    call('PUT', 'queue', QS, {'eventId': dr.id}, headers=A, json_body={'activeSlotCapacity': 5}, want=(404, 'NOT_FOUND'), name='settings put draft event')
    on = new_event('P2 onsale', 5, 5)
    call('PUT', 'queue', QS, {'eventId': on.id}, headers=A, json_body={'activeSlotCapacity': 5, 'oneActivePerVisitor': False}, want=(409, 'EVENT_RULES_LOCKED'), name='settings put locked rule after sale open')
    call('PUT', 'queue', QS, {'eventId': 'nope'}, headers=A, json_body={'activeSlotCapacity': 5}, want=(0, ''), name='settings put non-uuid (observe)')


if __name__ == '__main__':
    run_parts([join_rows, me_claim_leave, sold_out, admin_settings])
