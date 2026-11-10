// SLO uji (bisa diubah lewat env). Dipakai k6 thresholds dan oleh scripts/report.mjs untuk PASS/WARNING/FAIL.
export const SLO = {
  p95: Number(__ENV.SLO_P95_MS || 500),
  p99: Number(__ENV.SLO_P99_MS || 1000),
  errorRate: Number(__ENV.SLO_ERROR_RATE || 0.01),
  checkRate: Number(__ENV.SLO_CHECK_RATE || 0.99),
};

// Nama endpoint (tag "name") yang dilaporkan terpisah. Harus sama dengan nama di lib/api.js.
export const ENDPOINTS = [
  'events.list', 'events.facets', 'events.detail', 'queue.status', 'queue.join', 'queue.me', 'queue.heartbeat',
  'queue.claim', 'session.create', 'booking.hold', 'booking.get', 'booking.cancel', 'booking.tickets',
  'payment.attempt', 'payment.status', 'payment.webhook', 'user.otp.request', 'user.otp.verify', 'user.me',
  'user.my-bookings', 'user.refresh', 'user.logout',
];

// Threshold "selalu lulus" (max>=0) memaksa k6 menulis submetric ke summary, agar laporan bisa memecah angka
// per level beban dan per endpoint. Threshold yang menentukan lulus/gagal ada di `gates`.
export function thresholds(levels, gates = true) {
  const t = {};
  const always = (m) => (t[m] = t[m] || ['max>=0']);
  const rate = (m) => (t[m] = t[m] || ['rate>=0']);
  const count = (m) => (t[m] = t[m] || ['count>=0']);
  for (const l of levels) {
    always(`http_req_duration{level:${l}}`);
    rate(`http_req_failed{level:${l}}`);
    rate(`checks{level:${l}}`);
    count(`http_reqs{level:${l}}`);
    count(`iterations{level:${l}}`);
  }
  for (const e of ENDPOINTS) {
    always(`http_req_duration{name:${e}}`);
    rate(`http_req_failed{name:${e}}`);
  }
  for (const c of ['2xx', '3xx', '4xx', '5xx', 'timeout']) count(`responses{class:${c}}`);
  if (gates) {
    t.http_req_duration = [`p(95)<${SLO.p95}`, `p(99)<${SLO.p99}`];
    t.http_req_failed = [`rate<${SLO.errorRate}`];
    t.checks = [`rate>${SLO.checkRate}`];
  }
  return t;
}

// Rem darurat untuk stress/spike: hentikan tes sebelum service lumpuh, tetapi setelah titik degradasi terlihat.
export function safetyStop(t) {
  t['http_req_failed'] = (t['http_req_failed'] || []).concat([{ threshold: 'rate<0.30', abortOnFail: true, delayAbortEval: '30s' }]);
  return t;
}
