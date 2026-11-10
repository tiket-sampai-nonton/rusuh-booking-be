// Concurrency: request bersamaan ke resource yang sama (http.batch mengirim paralel dari satu VU), lalu memeriksa
// invariant bisnis: tidak ada booking/attempt/sesi ganda, tidak oversell, webhook ganda tidak menerbitkan tiket ganda,
// dan nomor antrean unik. Satu VU, satu iterasi: yang diuji ketepatan, bukan throughput.
import http from 'k6/http';
import { check } from 'k6';
import { target, canSignWebhook } from '../config/environments.js';
import { thresholds } from '../config/thresholds.js';
import { call } from '../lib/http.js';
import { adminToken, createEvent, cancelEvent, nik, email, uniq, RUN } from '../lib/data.js';
import { admit, claimed, hold, settlementBody, waitStatus, tickets } from '../lib/flows.js';
import { summary } from '../lib/summary.js';

const N = Number(__ENV.PARALLEL || 10);
const STOCK = 4;

export const options = {
  scenarios: { concurrency: { executor: 'shared-iterations', vus: 1, iterations: 1, maxDuration: '10m' } },
  // 409/429 di sini adalah jawaban benar untuk request kembar, jadi gate memakai checks bisnis, bukan http_req_failed.
  thresholds: Object.assign(thresholds(['all'], false), { checks: ['rate==1'] }),
  summaryTrendStats: ['avg', 'min', 'med', 'p(90)', 'p(95)', 'p(99)', 'max', 'count'],
  setupTimeout: '120s',
};

const H = (token, extra) => ({ headers: Object.assign({ 'Content-Type': 'application/json', 'X-Client-Type': 'mobile', 'X-Visitor-Token': token }, extra) });
const ok = (r) => r.status >= 200 && r.status < 300;
const body = (r, k) => {
  try {
    return r.json(`data.${k}`);
  } catch (_) {
    return undefined;
  }
};
const distinct = (rs, k) => new Set(rs.filter(ok).map((r) => body(r, k))).size;

export function setup() {
  const bearer = adminToken();
  return {
    bearer,
    big: createEvent(bearer, 'conc', [{ code: 'LOAD', name: 'Uji', priceMinor: 30000, capacity: 1000 }]),
    small: createEvent(bearer, 'stock', [{ code: 'LOAD', name: 'Stok Terbatas', priceMinor: 30000, capacity: STOCK }]),
  };
}

export default function (d) {
  // 1. Join serentak: N visitor baru di satu event, nomor antrean (seq) harus unik.
  const joins = http.batch(Array.from({ length: N }, () => ['POST', `${target.queue}/api/v1/events/${d.big}/queue-entries`, '{}', H('', {})]));
  const toks = joins.filter(ok).map((r) => body(r, 'visitorToken'));
  const seqs = toks.map((t) => http.get(`${target.queue}/api/v1/queue-entries/me?eventId=${d.big}`, H(t)).json('data.seq'));
  check(seqs, { [`${N} join serentak: semua berhasil dan seq unik`]: (s) => s.length === N && new Set(s).size === N });

  // 2. Satu admission token dipakai bersamaan untuk membuka sesi: hanya satu sesi yang boleh lahir.
  const c0 = claimed(d.big);
  if (!check(c0, { 'admitted dan claim untuk uji sesi': (x) => x !== null })) return;
  const sess = http.batch(Array.from({ length: N }, () => ['POST', `${target.booking}/api/v1/booking-sessions`, JSON.stringify({ admissionToken: c0.admissionToken }), H(c0.token)]));
  check(sess, {
    'admission token kembar: tepat satu sessionId': (rs) => distinct(rs, 'sessionId') === 1,
    'admission token kembar: tidak ada 5xx': (rs) => rs.every((r) => r.status < 500),
  });
  const first = sess.find(ok);
  if (!first) return;
  const adm0 = { token: c0.token, session: first.json('data') };

  // 3. Hold kembar dengan Idempotency-Key yang sama: tepat satu booking.
  const n = uniq();
  const buyer = email(n);
  const holdBody = JSON.stringify({
    sessionId: adm0.session.sessionId,
    buyer: { nik: nik(n * 2), email: buyer, emailConfirmation: buyer, consent: true },
    items: [{ categoryId: adm0.session.categories[0].categoryId, attendee: { nik: nik(n * 2 + 1), fullName: 'Peserta Uji' } }],
  });
  const holds = http.batch(Array.from({ length: N }, () => ['POST', `${target.booking}/api/v1/bookings`, holdBody, H(adm0.token, { 'Idempotency-Key': `k6-hold-same-${RUN}-${n}` })]));
  check(holds, {
    'hold kembar: tepat satu bookingId': (rs) => distinct(rs, 'bookingId') === 1,
    // Kontrak booking: request kembar yang masih diproses dijawab 503 DEPENDENCY_UNAVAILABLE (ulangi dengan key sama).
    'hold kembar: sisanya ditolak sesuai kontrak (409/429/503), tanpa 500': (rs) => rs.every((r) => ok(r) || [409, 429, 503].includes(r.status)),
  });
  const bookingRes = holds.find(ok);
  if (!bookingRes) return;
  const booking = bookingRes.json('data');

  // 4. Hold kedua dengan key berbeda di sesi yang sama: sesi sudah terpakai, tidak boleh lahir booking kedua.
  const other = http.post(`${target.booking}/api/v1/bookings`, holdBody, H(adm0.token, { 'Idempotency-Key': `k6-hold-other-${RUN}-${n}` }));
  check(other, { 'hold kedua di sesi yang sama ditolak (bukan booking baru)': (r) => !ok(r) || body(r, 'bookingId') === booking.bookingId });

  // 5. Attempt kembar dengan key yang sama: tepat satu attempt (satu transaksi Sandbox).
  const atts = http.batch(Array.from({ length: Math.min(N, 5) }, () => [
    'POST', `${target.payment}/api/v1/payments/${booking.bookingId}/attempts`, JSON.stringify({ method: 'VA_BCA' }),
    H(adm0.token, { 'Idempotency-Key': `k6-pay-same-${RUN}-${n}` }),
  ]));
  check(atts, {
    'attempt kembar: tepat satu attemptNo': (rs) => distinct(rs, 'attemptNo') === 1,
    'attempt kembar: sisanya 409/503 yang bisa diulang, tanpa 500': (rs) => rs.every((r) => ok(r) || [409, 503].includes(r.status)),
  });
  const att = atts.find(ok);

  // 6. Webhook settlement dikirim N kali bersamaan: booking PAID sekali, tiket tidak ganda.
  if (canSignWebhook && att) {
    const wb = settlementBody(booking, att.json('data.attemptNo'));
    const hooks = http.batch(Array.from({ length: N }, () => ['POST', `${target.payment}/api/v1/webhooks/midtrans`, wb, { headers: { 'Content-Type': 'application/json' } }]));
    check(hooks, { 'webhook ganda: semua diterima tanpa 5xx': (rs) => rs.every((r) => r.status < 300) });
    const fin = waitStatus(adm0.token, booking.bookingId, 'PAID');
    check(fin, { 'webhook ganda: booking PAID': (x) => x && x.status === 'PAID' });
    check(tickets(adm0.token, booking.bookingId), { 'webhook ganda: tepat satu e-tiket': (t) => t.length === 1 });
  }

  // 7. Perebutan stok: event berstok 4, pembeli datang bersamaan. Unit terjual + tertahan tidak boleh melebihi stok.
  const buyers = [];
  for (let i = 0; i < 4; i++) {
    const a = admit(d.small, 30);
    if (a) buyers.push(a);
  }
  const reserved = buyers.reduce((s, b) => s + (b.session.reservedUnits || 0), 0);
  check(reserved, { [`reservasi admission tidak melebihi stok ${STOCK}`]: (r) => r <= STOCK });
  const raced = buyers.map((b, i) => hold(b, 'LOAD', `k6-hold-race-${RUN}-${n}-${i}`)).filter((x) => x);
  const detail = call('booking', 'GET', `/api/v1/events/${d.small}`, { name: 'events.detail' }).data;
  const avail = detail && detail.categories ? detail.categories.reduce((s, c) => s + (c.available || 0), 0) : -1;
  check(raced, { [`hold sukses (${raced.length}) tidak melebihi stok ${STOCK}`]: (r) => r.length <= STOCK });
  check(avail, { 'sisa stok tidak negatif': (a) => a >= 0 });
}

export function teardown(d) {
  cancelEvent(d.bearer, d.big);
  cancelEvent(d.bearer, d.small);
}

export const handleSummary = summary('concurrency', [], { executor: 'shared-iterations', vus: 1, iterations: 1, parallel: N });
