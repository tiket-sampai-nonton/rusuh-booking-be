// Satu pintu untuk semua request: base URL per service, header klien mobile, tag endpoint/level, check status,
// dan penghitung kelas respons (2xx/4xx/5xx/timeout) untuk laporan.
import http from 'k6/http';
import { check } from 'k6';
import exec from 'k6/execution';
import { Counter } from 'k6/metrics';
import { target } from '../config/environments.js';

export const responses = new Counter('responses');
let levelOf = () => 'all';

// Dipanggil skenario yang punya jadwal level; tanpa itu semua request bertag level "all".
export function useLevels(windows) {
  levelOf = () => {
    let start;
    try {
      start = exec.scenario.startTime;
    } catch (_) {
      return 'setup'; // setup/teardown tidak punya skenario
    }
    const t = (Date.now() - start) / 1000; // sejak skenario mulai (setelah setup)
    const w = windows.find((x) => t >= x.rampStart && t < x.holdEnd);
    return !w ? 'tail' : t < w.holdStart ? 'ramp' : w.name;
  };
}

function classOf(res) {
  if (res.status === 0) return 'timeout';
  return `${Math.floor(res.status / 100)}xx`;
}

// call(service, method, path, opts) -> { res, data, ok }
//   opts: name (tag endpoint), body (objek), visitor (token), bearer, headers, expect (status yang dianggap benar)
export function call(service, method, path, opts = {}) {
  const level = levelOf();
  exec.vu.metrics.tags.level = level; // ikut ke checks dan iterations
  const headers = Object.assign({ 'Content-Type': 'application/json', 'X-Client-Type': 'mobile' }, opts.headers);
  if (opts.visitor) headers['X-Visitor-Token'] = opts.visitor;
  if (opts.bearer) headers.Authorization = `Bearer ${opts.bearer}`;
  const expect = opts.expect || [200, 201, 204];
  const body = opts.body === undefined ? null : typeof opts.body === 'string' ? opts.body : JSON.stringify(opts.body);
  const res = http.request(method, `${target[service]}${path}`, body, {
    headers,
    tags: { name: opts.name, level },
    timeout: opts.timeout || '30s',
    responseCallback: http.expectedStatuses(...expect),
  });
  responses.add(1, { class: classOf(res), name: opts.name, status: String(res.status) });
  const ok = check(res, { [`${opts.name} -> ${expect.join('/')}`]: (r) => expect.includes(r.status) }, { name: opts.name });
  let data = null;
  try {
    data = res.json('data');
  } catch (_) {
    // body bukan JSON (mis. 502 dari proxy); cukup tercatat sebagai gagal di check
  }
  return { res, data, ok };
}
