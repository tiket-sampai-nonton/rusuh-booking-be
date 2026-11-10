// Smoke: 1 VU, 1 iterasi. Semua service hidup, setiap endpoint utama menjawab benar, dan satu transaksi berjalan
// sampai PAID (lokal). Wajib lulus sebelum tes beban.
import { check } from 'k6';
import http from 'k6/http';
import { target } from '../config/environments.js';
import { thresholds } from '../config/thresholds.js';
import { call } from '../lib/http.js';
import { browse, join, buyAndCancel } from '../lib/flows.js';
import { summary } from '../lib/summary.js';
import paidFlow, { setup as e2eSetup, teardown as e2eTeardown } from './e2e.js';

export const options = {
  scenarios: { smoke: { executor: 'shared-iterations', vus: 1, iterations: 1, maxDuration: '5m' } },
  thresholds: Object.assign(thresholds(['all']), { checks: ['rate==1'], http_req_failed: ['rate==0'] }),
  summaryTrendStats: ['avg', 'min', 'med', 'p(90)', 'p(95)', 'p(99)', 'max', 'count'],
  setupTimeout: '120s',
};

export const setup = e2eSetup;
export const teardown = e2eTeardown;

export default function (d) {
  for (const svc of ['queue', 'booking', 'payment', 'notification', 'user']) {
    const r = http.get(`${target[svc]}/health`, { tags: { name: 'health' } });
    check(r, { [`${svc} sehat`]: (x) => x.status === 200 });
  }
  browse([d.event]);

  // Antrean: join, posisi, heartbeat, lalu keluar.
  const q = join(d.event);
  if (check(q, { 'join antrean mengembalikan visitor token': (x) => x !== null })) {
    call('queue', 'GET', `/api/v1/queue-entries/me?eventId=${d.event}`, { name: 'queue.me', visitor: q.token });
    call('queue', 'POST', `/api/v1/queue-entries/me/heartbeat?eventId=${d.event}`, { name: 'queue.heartbeat', visitor: q.token });
    call('queue', 'DELETE', `/api/v1/queue-entries/${q.entryId}?eventId=${d.event}`, { name: 'queue.leave', visitor: q.token, expect: [200, 204] });
  }

  check(buyAndCancel(d.event), { 'hold lalu batal berhasil': (x) => x });
  paidFlow(d);
}

export const handleSummary = summary('smoke', [], { executor: 'shared-iterations', vus: 1, iterations: 1 });
