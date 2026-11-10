// End-to-end bisnis: katalog -> antrean -> admitted -> claim -> sesi -> hold -> attempt (Midtrans Sandbox) ->
// webhook settlement bertanda tangan (lokal) -> PAID -> e-tiket -> status pembayaran -> email e-tiket di Mailpit.
// Jumlah iterasi kecil: tiap attempt membuat transaksi Sandbox sungguhan.
import http from 'k6/http';
import { check, sleep } from 'k6';
import { Counter } from 'k6/metrics';
import { target, canSignWebhook, canReadMail } from '../config/environments.js';
import { thresholds } from '../config/thresholds.js';
import { call } from '../lib/http.js';
import { adminToken, createEvent, cancelEvent, login, email, uniq } from '../lib/data.js';
import { browse, admit, hold, attempt, settlementBody, webhook, waitStatus, tickets, account } from '../lib/flows.js';
import { summary } from '../lib/summary.js';

const paid = new Counter('e2e_paid');
const VUS = Number(__ENV.E2E_VUS || 3);
const ITER = Number(__ENV.E2E_ITERATIONS || 2);

export const options = {
  scenarios: { e2e: { executor: 'per-vu-iterations', vus: VUS, iterations: ITER, maxDuration: '10m' } },
  thresholds: Object.assign(thresholds(['all']), { checks: ['rate>0.99'], e2e_paid: [canSignWebhook ? `count==${VUS * ITER}` : 'count>=0'] }),
  summaryTrendStats: ['avg', 'min', 'med', 'p(90)', 'p(95)', 'p(99)', 'max', 'count'],
  setupTimeout: '120s',
};

export function setup() {
  const bearer = adminToken();
  return { bearer, event: createEvent(bearer, 'e2e', [{ code: 'LOAD', name: 'Uji E2E', priceMinor: 25000, capacity: 1000 }]) };
}

function mailArrived(to) {
  for (let i = 0; i < 30; i++) {
    const r = http.get(`${target.mailpit}/api/v1/search?query=${encodeURIComponent(`to:"${to}"`)}`, { tags: { name: 'mailpit' } });
    if (r.status === 200 && (r.json('messages_count') || (r.json('messages') || []).length)) return true;
    sleep(1);
  }
  return false;
}

export default function (d) {
  browse([d.event]);
  const adm = admit(d.event);
  if (!check(adm, { 'admitted, claim, dan sesi booking terbuka': (x) => x !== null })) return;
  const b = hold(adm, 'LOAD');
  if (!check(b, { 'hold berhasil, status PENDING_PAYMENT': (x) => x && x.status === 'PENDING_PAYMENT' })) return;
  const a = attempt(adm.token, b.bookingId);
  if (!check(a, { 'attempt dibuat di Midtrans Sandbox (PENDING)': (x) => x && x.status === 'PENDING' })) return;
  if (!canSignWebhook) return; // dev/prod: pembayaran diselesaikan manual di simulator Midtrans

  check(webhook(settlementBody(b, a.attemptNo)).res, { 'webhook settlement diterima': (r) => r.status < 300 });
  const final = waitStatus(adm.token, b.bookingId, 'PAID');
  if (!check(final, { 'booking menjadi PAID': (x) => x && x.status === 'PAID' })) return;
  paid.add(1);
  check(tickets(adm.token, b.bookingId), { 'satu e-tiket terbit, tidak ganda': (t) => t.length === 1 });
  const st = call('payment', 'GET', `/api/v1/payments/${b.bookingId}/status`, { name: 'payment.status', visitor: adm.token }).data;
  check(st, { 'status pembayaran PAID, attempt SUCCEEDED': (x) => x && x.bookingStatus === 'PAID' && x.attempt.status === 'SUCCEEDED' });
  if (canReadMail) check(mailArrived(b.buyerEmail), { 'email e-tiket terkirim ke pembeli': (x) => x });
  if (__ITER === 0) account(login(email(uniq())));
}

export function teardown(d) {
  cancelEvent(d.bearer, d.event);
}

export const handleSummary = summary('e2e', [], { vus: VUS, iterations: ITER, executor: 'per-vu-iterations' });
