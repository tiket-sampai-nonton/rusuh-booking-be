// Campuran trafik realistis untuk load / stress / spike / soak (TEST_TYPE). Bentuk beban dari config/profiles.js.
//   browse  : ramping-arrival-rate (iterasi per detik)  -> beban baca katalog, tidak bergantung latensi
//   waiters : ramping-vus (pengguna serentak)           -> pengantre yang polling seperti FE sekarang
//   buy     : ramping-arrival-rate                      -> join..hold..batal (tanpa Midtrans)
//   account : ramping-arrival-rate (hanya bila login bisa diotomatiskan)
import { canReadMail } from '../config/environments.js';
import { schedule, stages } from '../config/profiles.js';
import { thresholds, safetyStop } from '../config/thresholds.js';
import { useLevels } from '../lib/http.js';
import { adminToken, createEvent, cancelEvent, login, email, uniq } from '../lib/data.js';
import { browse, wait, buyAndCancel, account } from '../lib/flows.js';
import { summary } from '../lib/summary.js';

const TYPE = __ENV.TEST_TYPE || 'load';
const windows = schedule(TYPE);
useLevels(windows);

// Laju dalam iterasi per 10 detik agar laju pecahan (mis. 0,5/detik) tetap bisa dipakai.
const per10 = (key) => stages(TYPE, key).map((st) => ({ duration: st.duration, target: Math.round(st.target * 10) }));
const rate = (key, exec, maxVUs) => ({
  executor: 'ramping-arrival-rate', exec, timeUnit: '10s', startRate: 0, stages: per10(key),
  preAllocatedVUs: Math.min(50, maxVUs), maxVUs,
});

const scenarios = {
  browse: rate('browse', 'browseFn', 600),
  waiters: { executor: 'ramping-vus', exec: 'waitFn', startVUs: 0, stages: stages(TYPE, 'waiters'), gracefulRampDown: '10s' },
  buy: rate('buy', 'buyFn', 400),
};
if (canReadMail || __ENV.AUTH_TOKEN) scenarios.account = rate('account', 'accountFn', 20);

const t = thresholds(windows.map((w) => w.name), TYPE === 'load' || TYPE === 'soak');
export const options = {
  scenarios,
  thresholds: safetyStop(t),
  summaryTrendStats: ['avg', 'min', 'med', 'p(90)', 'p(95)', 'p(99)', 'max', 'count'],
  setupTimeout: '120s',
  teardownTimeout: '60s',
};

export function setup() {
  const bearer = adminToken();
  const big = [{ code: 'LOAD', name: 'Uji Beban', priceMinor: 1000, capacity: 1000000 }];
  const d = { bearer, wait: createEvent(bearer, `${TYPE}-wait`, big), buy: createEvent(bearer, `${TYPE}-buy`, big) };
  return Object.assign(d, { t0: Date.now() }); // skenario mulai tepat setelah setup; dipakai laporan untuk jendela level
}

export function browseFn(d) {
  browse([d.wait, d.buy]);
}

export function waitFn(d) {
  wait(d.wait, 120);
}

export function buyFn(d) {
  buyAndCancel(d.buy);
}

export function accountFn() {
  account(login(email(uniq())));
}

export function teardown(d) {
  cancelEvent(d.bearer, d.wait);
  cancelEvent(d.bearer, d.buy);
}

export const handleSummary = summary(TYPE, windows, {
  executors: { browse: 'ramping-arrival-rate', waiters: 'ramping-vus', buy: 'ramping-arrival-rate', account: 'ramping-arrival-rate' },
});
