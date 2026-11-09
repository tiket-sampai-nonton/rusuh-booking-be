"""Part 4: user.yaml (fast rows) + notification.yaml."""
import os; os.environ.setdefault('PART', 'p4')
from mlib import *
OR, OV, RF, LO = '/api/v1/auth/otp/request', '/api/v1/auth/otp/verify', '/api/v1/auth/refresh', '/api/v1/auth/logout'
PS = '/api/v1/push-subscriptions'
BAD = {'X-Visitor-Token': 'not-a-token'}


def em(tag):
    return '%s-%d%d@sim.test' % (tag, os.getpid(), random.randint(0, 99999))


def auth():
    e = em('u1')
    call('POST', 'user', OR, json_body={'email': e}, want=(202, ''), name='otp request')
    call('POST', 'user', OR, json_body={'email': e}, want=(429, 'OTP_RESEND_TOO_SOON'), name='otp request again')
    for nm, b in [('invalid email', {'email': 'not-an-email'}), ('missing email', {}), ('unknown field', {'email': em('x'), 'foo': 1})]:
        call('POST', 'user', OR, json_body=b, want=(400, 'VALIDATION_ERROR'), name='otp request ' + nm)
    call('POST', 'user', OR, data='{x', want=(400, 'VALIDATION_ERROR'), name='otp request bad json')
    # verify
    for nm, b in [('bad code format', {'email': e, 'code': '12'}), ('missing code', {'email': e}), ('bad email', {'email': 'x', 'code': '123456'})]:
        call('POST', 'user', OV, json_body=b, want=(400, 'VALIDATION_ERROR'), name='otp verify ' + nm)
    call('POST', 'user', OV, data='{x', want=(400, 'VALIDATION_ERROR'), name='otp verify bad json')
    codes = []
    for i in range(6):
        r = call('POST', 'user', OV, json_body={'email': e, 'code': '00000%d' % i}, want=(401, 'OTP_INVALID') if i < 4 else (429, 'OTP_LOCKED') if i >= 5 else (0, ''), name='otp verify wrong #%d' % i)
        codes.append((r.status, r.code))
    note('wrong codes:', codes)
    # happy path + me
    e2 = em('u2'); at, rt, r = login(e2, 'user login')
    call('POST', 'user', OV, json_body={'email': e2, 'code': '000000'}, want=(0, ''), name='otp verify after consume (observe)')
    A = {'Authorization': 'Bearer ' + at}
    call('GET', 'user', '/api/v1/me', headers=A, want=(200, ''), name='me ok')
    call('GET', 'user', '/api/v1/me', want=(401, 'UNAUTHORIZED'), name='me no token')
    call('GET', 'user', '/api/v1/me', headers={'Authorization': 'Bearer garbage'}, want=(401, 'UNAUTHORIZED'), name='me garbage token')
    call('PATCH', 'user', '/api/v1/me', headers=A, json_body={}, want=(400, 'VALIDATION_ERROR'), name='me patch no field')
    call('PATCH', 'user', '/api/v1/me', headers=A, json_body={'nik': '12'}, want=(400, 'VALIDATION_ERROR'), name='me patch bad nik')
    call('PATCH', 'user', '/api/v1/me', headers=A, json_body={'foo': 1}, want=(400, 'VALIDATION_ERROR'), name='me patch unknown field')
    call('PATCH', 'user', '/api/v1/me', json_body={'fullName': 'Budi'}, want=(401, 'UNAUTHORIZED'), name='me patch no token')
    n = nik()
    call('PATCH', 'user', '/api/v1/me', headers=A, json_body={'fullName': 'Budi Satu', 'nik': n}, want=(200, ''), name='me patch ok')
    at3, rt3, _ = login(em('u3'), 'user login 3')
    call('PATCH', 'user', '/api/v1/me', headers={'Authorization': 'Bearer ' + at3}, json_body={'nik': n}, want=(409, 'NIK_ALREADY_LINKED'), name='me patch nik linked elsewhere')
    # refresh / logout
    call('POST', 'user', RF, json_body={'refreshToken': 'garbage'}, want=(401, 'SESSION_INVALID'), name='refresh garbage token')
    call('POST', 'user', RF, json_body={}, want=(0, ''), name='refresh missing token (observe)')
    r1 = call('POST', 'user', RF, json_body={'refreshToken': rt}, want=(200, ''), name='refresh ok')
    call('POST', 'user', RF, json_body={'refreshToken': rt}, want=(401, 'SESSION_INVALID'), name='refresh reused token')
    call('POST', 'user', RF, json_body={'refreshToken': r1.d('refreshToken')}, want=(401, 'SESSION_INVALID'), name='refresh family revoked')
    at4, rt4, _ = login(em('u4'), 'user login 4')
    call('POST', 'user', LO, headers={'Authorization': 'Bearer ' + at4}, json_body={'refreshToken': rt4}, want=(204, ''), name='logout ok')
    call('POST', 'user', LO, headers={'Authorization': 'Bearer ' + at4}, json_body={'refreshToken': rt4}, want=(0, ''), name='logout again (observe)')
    call('POST', 'user', LO, json_body={'refreshToken': 'garbage'}, want=(0, ''), name='logout garbage (observe)')
    call('POST', 'user', LO, json_body={}, want=(0, ''), name='logout empty (observe)')
    call('GET', 'user', '/api/v1/me', headers={'Authorization': 'Bearer ' + at4}, want=(401, 'UNAUTHORIZED'), name='me after logout')


def notif():
    ev = new_event('P4 push', 5, 5)
    n = Buyer(ev, 'n1').admit()
    body = lambda **kw: dict({'eventId': ev.id, 'platform': 'ANDROID', 'pushToken': 'fcm-test-token-%d' % random.randint(0, 10**9)}, **kw)
    call('POST', 'notification', PS, headers=n.vh(), json_body=body(), want=(204, ''), name='push subscribe')
    call('POST', 'notification', PS, headers=n.vh(), json_body=body(platform='WINDOWS'), want=(400, 'VALIDATION_ERROR'), name='push bad platform')
    call('POST', 'notification', PS, headers=n.vh(), json_body={'eventId': 'x'}, want=(400, 'VALIDATION_ERROR'), name='push missing fields')
    call('POST', 'notification', PS, headers=n.vh(), data='{x', want=(400, 'VALIDATION_ERROR'), name='push bad json')
    call('POST', 'notification', PS, json_body=body(), want=(401, 'VALIDATION_ERROR'), name='push no token (documented quirk: code VALIDATION_ERROR)')
    call('POST', 'notification', PS, headers=BAD, json_body=body(), want=(401, 'VALIDATION_ERROR'), name='push garbage token (documented quirk)')
    call('DELETE', 'notification', PS + '/{eventId}', {'eventId': ev.id}, headers=n.vh(), want=(204, ''), name='push unsubscribe')
    call('DELETE', 'notification', PS + '/{eventId}', {'eventId': ev.id}, want=(0, ''), name='push unsubscribe no token (observe)')
    call('DELETE', 'notification', PS + '/{eventId}', {'eventId': 'nope'}, headers=n.vh(), want=(0, ''), name='push unsubscribe non-uuid (observe)')
    call('DELETE', 'notification', PS + '/{eventId}', {'eventId': uid()}, headers=n.vh(), want=(0, ''), name='push unsubscribe unknown event (observe)')


if __name__ == '__main__':
    run_parts([auth, notif])
