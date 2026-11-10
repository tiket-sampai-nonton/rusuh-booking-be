// Uji beban waiting room: meniru polling klien (mode mobile/token) pada satu event khusus uji.
//   bash tests/k6/scripts/run.sh waiting            (lokal: event uji dibuat otomatis)
//   TEST_ENV=dev EVENT_ID=<uuid> PROFILE=C VUS=2000 bash tests/k6/scripts/run.sh waiting
// PROFILE: A = klien sekarang (/me + queue-status tiap nextPollAfterMs)
//          B = interval mengikuti posisi (poll.farMs/midMs/nearMs)
//          C = seperti B, tetapi zona jauh hanya membaca queue-status dan menghitung posisi dari seq - headSeq
// Jangan pakai event yang dipakai orang lain: pengantre yang mendapat giliran menahan stok sampai claim window habis.
import http from 'k6/http';
import { sleep } from 'k6';
import { Counter, Rate } from 'k6/metrics';
import { target } from '../config/environments.js';
import { thresholds } from '../config/thresholds.js';
import { adminToken, createEvent, cancelEvent } from '../lib/data.js';
import { summary } from '../lib/summary.js';

const BASE = __ENV.BASE || target.queue;
let EVENT = __ENV.EVENT_ID;
const PROFILE = __ENV.PROFILE || 'A';
if (!'ABC'.includes(PROFILE)) throw new Error('PROFILE harus A|B|C');

const cacheServed = new Rate('queue_status_from_cache');
const admitted = new Counter('visitors_admitted');
const edgeBlocked = new Counter('edge_blocked');

export const options = {
  scenarios: {
    waiting: {
      executor: 'ramping-vus',
      startVUs: 0,
      stages: [
        { duration: __ENV.RAMP || '30s', target: Number(__ENV.VUS || 200) },
        { duration: __ENV.HOLD || '60s', target: Number(__ENV.VUS || 200) },
      ],
      gracefulRampDown: '5s',
      gracefulStop: '5s',
    },
  },
  thresholds: Object.assign(thresholds(['all'], false), {
    http_req_failed: [{ threshold: 'rate<0.01', abortOnFail: true, delayAbortEval: '20s' }],
    http_req_duration: [{ threshold: 'p(95)<2000', abortOnFail: true, delayAbortEval: '20s' }],
  }),
  setupTimeout: '120s',
  summaryTrendStats: ['avg', 'p(50)', 'p(95)', 'p(99)', 'max'],
  userAgent: 'rusuh-k6-loadtest',
};

// Event uji dibuat otomatis kecuali EVENT_ID diberikan (dev/prod).
export function setup() {
  if (EVENT) return { event: EVENT };
  const bearer = adminToken();
  return { bearer, event: createEvent(bearer, `waiting-${PROFILE}`, [{ code: 'LOAD', name: 'Uji Beban', priceMinor: 1000, capacity: 100000 }]) };
}

export function teardown(d) {
  if (d.bearer) cancelEvent(d.bearer, d.event);
}

export const handleSummary = summary('waiting', [], { profile: PROFILE, executor: 'ramping-vus', vus: Number(__ENV.VUS || 200) });

const FINISHED = ['ADMITTED', 'CLAIMED', 'ENDED', 'MISSED', 'LEFT', 'COMPLETED'];

function call(method, path, name, token, body) {
  const headers = { 'Content-Type': 'application/json', 'X-Client-Type': 'mobile' };
  if (token) headers['X-Visitor-Token'] = token;
  // queue-status 404 sesaat setelah join pertama di event baru: Status Document belum terbit (bukan error).
  const expected = name === 'queue.status' ? http.expectedStatuses(200, 404) : http.expectedStatuses({ min: 200, max: 299 });
  const res = http.request(method, `${BASE}/api/v1${path}`, body, { headers, tags: { name, level: 'all' }, responseCallback: expected });
  // Cloudflare menjawab HTML saat menantang/memblokir; itu bukan kapasitas sistem kita.
  if (res.status === 403 || res.status === 503) edgeBlocked.add(1);
  return res;
}

const data = (res) => {
  try {
    return res.json('data') || null;
  } catch (_) {
    return null;
  }
};

function waitMs(poll, nextPollAfterMs, position) {
  if (PROFILE === 'A' || !poll) return nextPollAfterMs || 3000;
  if (position > poll.farAbove) return poll.farMs;
  return position > poll.midAbove ? poll.midMs : poll.nearMs;
}

function readStatus() {
  const res = call('GET', `/queue-status/${EVENT}`, 'queue.status');
  const cf = res.headers['Cf-Cache-Status'] || '';
  cacheServed.add(['HIT', 'UPDATING', 'REVALIDATED'].includes(cf));
  return data(res);
}

export default function (d) {
  EVENT = d.event;
  sleep(Math.random() * 2); // sebar join dalam tiap detik ramp
  const join = data(call('POST', `/events/${EVENT}/queue-entries`, 'queue.join', null, '{}'));
  if (!join || !join.visitorToken) return sleep(3);
  const token = join.visitorToken;

  let me = data(call('GET', `/queue-entries/me?eventId=${EVENT}`, 'queue.me', token));
  if (!me) return;
  const seq = me.seq;
  let position = me.exactPosition || 0;
  let nextHeartbeatAt = 0;

  for (;;) {
    if (Date.now() >= nextHeartbeatAt) {
      const hb = data(call('POST', `/queue-entries/me/heartbeat?eventId=${EVENT}`, 'queue.heartbeat', token));
      nextHeartbeatAt = Date.now() + ((hb && hb.nextHeartbeatMs) || 120000);
      if (hb && FINISHED.includes(hb.state)) break;
    }

    const status = readStatus();
    const estimate = status && status.headSeq != null ? Math.max(seq - status.headSeq, 0) : position;
    const far = PROFILE === 'C' && status && status.poll && estimate > status.poll.farAbove;

    if (!far) {
      me = data(call('GET', `/queue-entries/me?eventId=${EVENT}`, 'queue.me', token));
      if (me) {
        position = me.exactPosition || position;
        if (FINISHED.includes(me.state)) {
          if (me.state === 'ADMITTED') admitted.add(1);
          break;
        }
      }
    } else {
      position = estimate;
    }

    let ms = waitMs(status && status.poll, status && status.nextPollAfterMs, position);
    ms = Math.min(ms, Math.max(nextHeartbeatAt - Date.now(), 1000));
    sleep((ms + Math.random() * 500) / 1000);
  }
  // Pengantre selesai tidak langsung join lagi, agar jumlah pengantre aktif tidak diganti entri baru terus-menerus.
  sleep(30);
}
