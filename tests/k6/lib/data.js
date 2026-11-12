// Data uji: NIK/email fiktif yang unik, event khusus uji lewat API admin, dan login OTP (kode dibaca dari Mailpit).
import http from 'k6/http';
import exec from 'k6/execution';
import { sleep } from 'k6';
import { call } from './http.js';
import { target, canReadMail } from '../config/environments.js';

export const RUN = __ENV.RUN_ID || `${Date.now()}`.slice(-7);
let seq = 0;

// Nomor unik per pemanggilan di seluruh tes (VU x urutan lokal), aman dipakai lintas VU.
export function uniq() {
  seq += 1;
  return exec.vu.idInTest * 100000 + seq;
}

// NIK fiktif yang lolos validasi pkg/nik: wilayah 31.74.01, tanggal lahir 1970-1999, serial 0001-9999.
export function nik(n) {
  const serial = 1 + (n % 9999);
  let r = Math.floor(n / 9999);
  const dd = 1 + (r % 28);
  r = Math.floor(r / 28);
  const mm = 1 + (r % 12);
  r = Math.floor(r / 12);
  const yy = 70 + (r % 30);
  const p = (v, w) => String(v).padStart(w, '0');
  return `317401${p(dd, 2)}${p(mm, 2)}${p(yy, 2)}${p(serial, 4)}`;
}

// Domain .test tidak pernah dikirim ke internet; di lokal semua email masuk Mailpit.
export const email = (n) => `${__ENV.TEST_EMAIL_PREFIX || 'k6'}+${RUN}-${n}@example.test`;

const iso = (d) => new Date(Date.now() + d * 3600 * 1000).toISOString();

// Event khusus uji: penjualan sudah buka, kategori besar untuk beban, kategori kecil untuk uji perebutan stok.
export function createEvent(bearer, label, categories) {
  const slug = `k6-${label}-${RUN}`.toLowerCase();
  const c = call('admin', 'POST', '/internal/v1/admin/events', {
    name: 'admin.create', bearer, expect: [201],
    body: {
      name: `[UJI BEBAN k6] ${label} ${RUN}`, slug, venue: 'Internal, tidak dijual',
      startsAt: iso(72), salesStartsAt: iso(-1), salesEndsAt: iso(48),
      maxTicketsPerOrder: 3, maxTicketsPerBuyerNik: 3, allowMixedCategories: true,
      description: [{ type: 'paragraph', children: [{ text: 'Event internal uji beban. Bukan event sungguhan.' }] }],
      categories,
    },
  });
  if (!c.data) throw new Error(`gagal membuat event uji: ${c.res.status} ${c.res.body}`);
  const id = c.data.eventId;
  call('admin', 'POST', `/internal/v1/admin/events/${id}/publish`, { name: 'admin.publish', bearer, expect: [200] });
  // Queue ketat (QSA-6): tanpa pengaturan antrean, join menjawab 409 QUEUE_NOT_CONFIGURED. Event terbit dulu, baru PUT.
  const q = call('queueAdmin', 'PUT', `/internal/v1/admin/events/${id}/queue-settings`, {
    name: 'admin.queue-settings', bearer, body: { activeSlotCapacity: Number(__ENV.QUEUE_ACTIVE_SLOTS || 1000) }, expect: [200, 201],
  });
  if (!q.ok) throw new Error(`gagal mengonfigurasi antrean event uji: ${q.res.status} ${q.res.body}`);
  return id;
}

export function cancelEvent(bearer, id) {
  call('admin', 'POST', `/internal/v1/admin/events/${id}/cancel`, { name: 'admin.cancel', bearer, expect: [200] });
}

function otpFromMail(to) {
  for (let i = 0; i < 30; i++) {
    const list = http.get(`${target.mailpit}/api/v1/search?query=${encodeURIComponent(`to:"${to}"`)}`, { tags: { name: 'mailpit' } });
    const msgs = list.status === 200 ? list.json('messages') || [] : [];
    if (msgs.length) {
      const m = http.get(`${target.mailpit}/api/v1/message/${msgs[0].ID}`, { tags: { name: 'mailpit' } });
      const code = (m.json('Text') || '').match(/\b(\d{6})\b/);
      if (code) return code[1];
    }
    sleep(0.5);
  }
  return null;
}

// Login OTP penuh. Di dev/prod tidak ada Mailpit: pakai AUTH_TOKEN dari environment.
export function login(addr) {
  if (!canReadMail) return __ENV.AUTH_TOKEN ? { accessToken: __ENV.AUTH_TOKEN } : null;
  const req = call('user', 'POST', '/api/v1/auth/otp/request', { name: 'user.otp.request', body: { email: addr }, expect: [200, 202, 204] });
  if (!req.ok) return null;
  const code = otpFromMail(addr);
  if (!code) return null;
  const v = call('user', 'POST', '/api/v1/auth/otp/verify', { name: 'user.otp.verify', body: { email: addr, code }, expect: [200] });
  return v.data;
}

// Token admin untuk membuat event uji: AUTH_TOKEN bila diberikan, atau login OTP di lokal.
export function adminToken() {
  if (__ENV.AUTH_TOKEN) return __ENV.AUTH_TOKEN;
  const t = login(`k6-admin+${RUN}@example.test`);
  if (!t || !t.accessToken) throw new Error('tidak bisa mendapatkan token admin (set AUTH_TOKEN atau jalankan Mailpit lokal)');
  return t.accessToken;
}
