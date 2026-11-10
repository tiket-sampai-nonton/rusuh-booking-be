// Alur bisnis yang sama dengan klien (mode mobile, token di header). Setiap fungsi mengembalikan hasil atau null.
import { sleep, check } from 'k6';
import crypto from 'k6/crypto';
import { call } from './http.js';
import { nik, email, uniq } from './data.js';

const jitter = (ms) => sleep((ms + Math.random() * 500) / 1000);

// Light user: lihat katalog, facet, detail, dan status antrean.
export function browse(eventIds) {
  call('booking', 'GET', '/api/v1/events', { name: 'events.list' });
  call('booking', 'GET', '/api/v1/events/facets', { name: 'events.facets' });
  const id = eventIds[Math.floor(Math.random() * eventIds.length)];
  call('booking', 'GET', `/api/v1/events/${id}`, { name: 'events.detail' });
  call('queue', 'GET', `/api/v1/queue-status/${id}`, { name: 'queue.status', expect: [200, 404] });
}

export function join(eventId) {
  const j = call('queue', 'POST', `/api/v1/events/${eventId}/queue-entries`, { name: 'queue.join', body: {}, expect: [200, 201] });
  return j.data && j.data.visitorToken ? { token: j.data.visitorToken, entryId: j.data.entryId } : null;
}

const me = (eventId, token) => call('queue', 'GET', `/api/v1/queue-entries/me?eventId=${eventId}`, { name: 'queue.me', visitor: token }).data;
const heartbeat = (eventId, token) =>
  call('queue', 'POST', `/api/v1/queue-entries/me/heartbeat?eventId=${eventId}`, { name: 'queue.heartbeat', visitor: token }).data;

// Normal user: menunggu seperti FE sekarang (me + status tiap nextPollAfterMs, heartbeat sesuai server) sampai
// admitted/berakhir atau batas waktu. Pengantre yang admitted tidak claim (slot kembali setelah claim window).
export function wait(eventId, maxSeconds) {
  const q = join(eventId);
  if (!q) return;
  const end = Date.now() + maxSeconds * 1000;
  let nextHb = 0;
  while (Date.now() < end) {
    if (Date.now() >= nextHb) {
      const hb = heartbeat(eventId, q.token);
      nextHb = Date.now() + ((hb && hb.nextHeartbeatMs) || 120000);
    }
    // 404 sesaat setelah join pertama di event baru: Status Document belum terbit (worker tiap 1 detik).
    const st = call('queue', 'GET', `/api/v1/queue-status/${eventId}`, { name: 'queue.status', expect: [200, 404] }).data;
    const m = me(eventId, q.token);
    if (m && m.state !== 'WAITING' && m.state !== 'STALE') return m.state;
    jitter((st && st.nextPollAfterMs) || 3000);
  }
  return 'TIMEOUT';
}

// Join sampai ADMITTED lalu claim. Mengembalikan { token, admissionToken } atau null.
export function claimed(eventId, maxSeconds = 90) {
  const q = join(eventId);
  if (!q) return null;
  const end = Date.now() + maxSeconds * 1000;
  let m = me(eventId, q.token);
  while (m && m.state !== 'ADMITTED' && Date.now() < end) {
    if (m.state !== 'WAITING' && m.state !== 'STALE') return null;
    jitter(1500);
    m = me(eventId, q.token);
  }
  if (!m || m.state !== 'ADMITTED') return null;
  const c = call('queue', 'POST', `/api/v1/queue-entries/${q.entryId}/claim?eventId=${eventId}`, { name: 'queue.claim', visitor: q.token });
  return c.data ? { token: q.token, admissionToken: c.data.admissionToken } : null;
}

// claimed + buka sesi booking. Mengembalikan { token, session } atau null.
export function admit(eventId, maxSeconds = 90) {
  const c = claimed(eventId, maxSeconds);
  if (!c) return null;
  const s = call('booking', 'POST', '/api/v1/booking-sessions', {
    name: 'session.create', visitor: c.token, body: { admissionToken: c.admissionToken }, expect: [200, 201],
  });
  return s.data ? { token: c.token, session: s.data } : null;
}

// Hold 1 tiket di kategori `code` dengan pembeli fiktif yang unik.
export function hold(adm, code, key) {
  const cat = adm.session.categories.find((x) => x.code === code) || adm.session.categories[0];
  const n = uniq();
  const buyer = email(n);
  const h = call('booking', 'POST', '/api/v1/bookings', {
    name: 'booking.hold', visitor: adm.token, headers: { 'Idempotency-Key': key || `k6-hold-${adm.session.sessionId}` },
    body: {
      sessionId: adm.session.sessionId,
      buyer: { nik: nik(n * 2), email: buyer, emailConfirmation: buyer, consent: true },
      items: [{ categoryId: cat.categoryId, attendee: { nik: nik(n * 2 + 1), fullName: 'Peserta Uji' } }],
    },
    expect: [200, 201],
  });
  return h.data ? Object.assign({ buyerEmail: buyer }, h.data) : null;
}

export function cancelBooking(token, bookingId) {
  return call('booking', 'POST', `/api/v1/bookings/${bookingId}/cancel`, { name: 'booking.cancel', visitor: token }).ok;
}

export function attempt(token, bookingId, key) {
  return call('payment', 'POST', `/api/v1/payments/${bookingId}/attempts`, {
    name: 'payment.attempt', visitor: token, headers: { 'Idempotency-Key': key || `k6-pay-${bookingId}` },
    body: { method: 'VA_BCA' }, expect: [200, 201],
  }).data;
}

// Notifikasi "settlement" seperti yang dikirim Midtrans, ditandatangani dengan server key lokal (bukan pembayaran nyata).
export function settlementBody(booking, attemptNo) {
  const orderId = `${booking.orderId}-${attemptNo}`;
  const gross = `${booking.amountMinor}.00`;
  const signature = crypto.sha512(`${orderId}200${gross}${__ENV.MIDTRANS_SERVER_KEY}`, 'hex');
  const now = new Date(Date.now() + 7 * 3600 * 1000).toISOString().replace('T', ' ').slice(0, 19);
  return JSON.stringify({
    order_id: orderId, status_code: '200', gross_amount: gross, signature_key: signature, transaction_status: 'settlement',
    transaction_id: `k6-${orderId}`, settlement_time: now, fraud_status: 'accept', payment_type: 'bank_transfer',
  });
}

export function webhook(body) {
  return call('payment', 'POST', '/api/v1/webhooks/midtrans', { name: 'payment.webhook', body, expect: [200, 204] });
}

export function waitStatus(token, bookingId, want, seconds = 60) {
  const end = Date.now() + seconds * 1000;
  let b = null;
  while (Date.now() < end) {
    b = call('booking', 'GET', `/api/v1/bookings/${bookingId}`, { name: 'booking.get', visitor: token }).data;
    if (b && b.status === want) return b;
    sleep(1);
  }
  return b;
}

export function tickets(token, bookingId) {
  const t = call('booking', 'GET', `/api/v1/bookings/${bookingId}/tickets`, { name: 'booking.tickets', visitor: token }).data;
  return (t && (t.tickets || t)) || [];
}

// Heavy user tanpa pembayaran: join, admitted, claim, sesi, hold, lalu batal agar stok kembali.
export function buyAndCancel(eventId) {
  const adm = admit(eventId);
  if (!adm) return false;
  const b = hold(adm, 'LOAD');
  if (!b) return false;
  return check(cancelBooking(adm.token, b.bookingId), { 'hold dibatalkan, stok kembali': (x) => x });
}

// Pengguna login: profil, riwayat booking, refresh token, logout.
export function account(tokens) {
  if (!tokens || !tokens.accessToken) return;
  call('user', 'GET', '/api/v1/me', { name: 'user.me', bearer: tokens.accessToken });
  call('booking', 'GET', '/api/v1/me/bookings?limit=10', { name: 'user.my-bookings', bearer: tokens.accessToken });
  if (!tokens.refreshToken) return;
  const r = call('user', 'POST', '/api/v1/auth/refresh', { name: 'user.refresh', body: { refreshToken: tokens.refreshToken } });
  const rt = (r.data && r.data.refreshToken) || tokens.refreshToken;
  call('user', 'POST', '/api/v1/auth/logout', { name: 'user.logout', body: { refreshToken: rt }, expect: [200, 204] });
}
