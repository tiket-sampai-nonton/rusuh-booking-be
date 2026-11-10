#!/usr/bin/env node
// Bangun laporan dari satu folder hasil run (dipanggil run.sh): analysis.html, capacity.md, levels.csv,
// endpoints.csv, timeseries.csv. Sumber: summary.json (k6), metrics.json.gz (sampel mentah), resources.csv/infra.csv.
// Tanpa dependensi: Node >= 18.
import fs from 'node:fs';
import path from 'node:path';
import zlib from 'node:zlib';
import readline from 'node:readline';
import { fileURLToPath } from 'node:url';

const HERE = path.dirname(fileURLToPath(import.meta.url));
if (!process.argv[2]) throw new Error('pakai: node report.mjs <folder-hasil>');
// Folder hasil harus berada di bawah tests/k6/reports: skrip ini menulis file, jadi argumen CLI tidak boleh keluar dari sana.
const REPORTS = path.resolve(HERE, '../reports');
const DIR = path.resolve(process.argv[2]);
if (!DIR.startsWith(REPORTS + path.sep)) throw new Error(`folder hasil harus di bawah ${REPORTS}`);
const read = (f) => (fs.existsSync(path.join(DIR, f)) ? fs.readFileSync(path.join(DIR, f), 'utf8') : null);
const raw = read('summary.json');
if (!raw) {
  console.error(`summary.json tidak ada di ${DIR}: k6 berhenti sebelum selesai (lihat k6.log)`);
  process.exit(1);
}
const S = JSON.parse(raw);
const RUN = JSON.parse(read('run.json') || '{}');
const C = S.context;
const M = S.metrics;
const SLO = C.slo;
const v = (k, f) => (M[k] && M[k].values && M[k].values[f] !== undefined ? M[k].values[f] : undefined);
const n0 = (x) => (x === undefined || Number.isNaN(x) ? '-' : x);
const ms = (x) => (x === undefined ? '-' : `${Math.round(x)} ms`);
const pct = (x, d = 2) => (x === undefined ? '-' : `${(x * 100).toFixed(d)}%`);
const num = (x, d = 1) => (x === undefined ? '-' : Number(x).toFixed(d));
const esc = (s) => String(s).replace(/[&<>"]/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' })[c]);

// ---------- deret waktu dari metrik mentah ----------
async function timeseries() {
  const f = path.join(DIR, 'metrics.json.gz');
  if (!fs.existsSync(f)) return null;
  const B = 5;
  const buckets = new Map();
  const perSec = new Map();
  const rl = readline.createInterface({ input: fs.createReadStream(f).pipe(zlib.createGunzip()), crlfDelay: Infinity });
  const get = (k) => buckets.get(k) || buckets.set(k, { reqs: 0, fail: 0, failN: 0, dur: [], vus: 0 }).get(k);
  for await (const line of rl) {
    const m = line.match(/"metric":"(http_reqs|http_req_duration|http_req_failed|vus)","type":"Point","data":\{"time":"([^"]+)","value":([\d.eE+-]+)/);
    if (!m) continue;
    const t = Date.parse(m[2]) / 1000;
    const val = Number(m[3]);
    const b = get(Math.floor(t / B) * B);
    if (m[1] === 'http_reqs') {
      b.reqs += val;
      const s = Math.floor(t);
      perSec.set(s, (perSec.get(s) || 0) + val);
    } else if (m[1] === 'http_req_duration') b.dur.push(val);
    else if (m[1] === 'http_req_failed') { b.fail += val; b.failN += 1; } else b.vus = Math.max(b.vus, val);
  }
  if (!buckets.size) return null;
  const keys = [...buckets.keys()].sort((a, b) => a - b);
  const q = (a, p) => (a.length ? a[Math.min(a.length - 1, Math.floor(p * a.length))] : undefined);
  const rows = keys.map((k) => {
    const b = buckets.get(k);
    b.dur.sort((x, y) => x - y);
    return { t: k - keys[0], epoch: k, rps: b.reqs / B, vus: b.vus, err: b.failN ? b.fail / b.failN : 0, p50: q(b.dur, 0.5), p95: q(b.dur, 0.95), p99: q(b.dur, 0.99) };
  });
  return { rows, peakRps: Math.max(...perSec.values()) };
}

// ---------- sumber daya (lokal) ----------
function csv(name) {
  const t = read(name);
  if (!t) return [];
  const [h, ...ls] = t.trim().split('\n');
  const cols = h.split(',');
  return ls.map((l) => Object.fromEntries(l.split(',').map((x, i) => [cols[i], x])));
}
const memMB = (s) => {
  const m = String(s || '').match(/([\d.]+)\s*([KMG]i?B)/);
  if (!m) return undefined;
  return Number(m[1]) * ({ KiB: 1 / 1024, KB: 1 / 1000, MiB: 1, MB: 1, GiB: 1024, GB: 1000 })[m[2]];
};
const RES = csv('resources.csv');
const INFRA = csv('infra.csv');
const measured = RES.length > 0;

function resourcesIn(from, to) {
  const rows = RES.filter((r) => r.ts >= from && r.ts <= to);
  const by = {};
  for (const r of rows) {
    const k = r.container.replace(/^rusuh-booking-be-|-1$/g, '');
    (by[k] = by[k] || { cpu: [], mem: [] }).cpu.push(Number(r.cpu_pct));
    by[k].mem.push(memMB(r.mem));
  }
  const out = Object.entries(by).map(([k, x]) => ({
    container: k, cpuAvg: x.cpu.reduce((a, b) => a + b, 0) / x.cpu.length, cpuMax: Math.max(...x.cpu), memMax: Math.max(...x.mem.filter(Boolean)),
  }));
  const inf = INFRA.filter((r) => r.ts >= from && r.ts <= to);
  const mx = (k) => (inf.length ? Math.max(...inf.map((r) => Number(r[k]) || 0)) : undefined);
  const av = (k) => (inf.length ? inf.reduce((a, r) => a + (Number(r[k]) || 0), 0) / inf.length : undefined);
  return { containers: out.sort((a, b) => b.cpuAvg - a.cpuAvg), pg: mx('pg_connections'), pgLock: mx('pg_lock_waits'), mq: mx('mq_backlog'), vkMem: mx('valkey_mem_mb'), vkOps: av('valkey_ops') };
}

// ---------- level ----------
const typeIsLevelled = (C.levels || []).length > 0;
const startEpoch = C.scenarioStart ? Date.parse(C.scenarioStart) / 1000 : Date.parse(C.startedAt) / 1000;
const levels = (typeIsLevelled ? C.levels : [{ name: 'all', hold: C.durationMs / 1000 }]).map((l) => {
  const k = (m) => `${m}{level:${l.name}}`;
  const d = M[k('http_req_duration')] && M[k('http_req_duration')].values;
  const cnt = v(k('http_reqs'), 'count') || 0;
  const row = {
    name: l.name, hold: l.hold, browse: l.browse, waiters: l.waiters, buy: l.buy, account: l.account, count: cnt,
    rps: cnt / (l.hold || 1), iters: v(k('iterations'), 'count') || 0,
    avg: d && d.avg, p50: d && d.med, p90: d && d['p(90)'], p95: d && d['p(95)'], p99: d && d['p(99)'], max: d && d.max,
    err: v(k('http_req_failed'), 'rate'), checks: v(k('checks'), 'rate'),
    res: measured && typeIsLevelled ? resourcesIn(startEpoch + l.holdStart, startEpoch + l.holdEnd) : measured ? resourcesIn(0, 9e12) : null,
  };
  row.status = levelStatus(row);
  return row;
});

function levelStatus(r) {
  if (!r.count) return 'NA';
  const chk = r.checks === undefined ? 1 : r.checks;
  if (C.type === 'concurrency') return chk === 1 ? 'PASS' : 'FAIL';
  if (C.type === 'smoke') return chk === 1 && !r.err ? 'PASS' : 'FAIL';
  const pass = r.p95 <= SLO.p95 && r.p99 <= SLO.p99 && (r.err || 0) <= SLO.errorRate && chk >= SLO.checkRate;
  if (pass) return 'PASS';
  const warn = r.p95 <= 2 * SLO.p95 && r.p99 <= 2 * SLO.p99 && (r.err || 0) <= 2 * SLO.errorRate && chk >= SLO.checkRate - 0.01;
  return warn ? 'WARNING' : 'FAIL';
}

const RANK = { PASS: 0, NA: 0, WARNING: 1, FAIL: 2 };
const worst = (xs) => xs.reduce((a, b) => (RANK[b] > RANK[a] ? b : a), 'PASS');
function overall() {
  const st = levels.map((l) => l.status);
  if (C.type === 'stress') return st[0] === 'FAIL' ? 'FAIL' : st.every((s) => s === 'PASS') ? 'PASS' : 'WARNING';
  if (C.type === 'spike') {
    const by = Object.fromEntries(levels.map((l) => [l.name, l.status]));
    if (by.recovery !== 'PASS') return 'FAIL';
    return by.spike === 'FAIL' ? 'WARNING' : worst(st);
  }
  return worst(st);
}

// ---------- endpoint, status, checks ----------
const endpoints = Object.keys(M)
  .map((k) => k.match(/^http_req_duration\{name:(.+)\}$/))
  .filter(Boolean)
  .map((m) => {
    const d = M[m[0]].values;
    return { name: m[1], count: d.count || 0, avg: d.avg, p95: d['p(95)'], p99: d['p(99)'], max: d.max, err: v(`http_req_failed{name:${m[1]}}`, 'rate') };
  })
  .filter((e) => e.count > 0)
  .sort((a, b) => b.p95 - a.p95);
const classes = ['2xx', '3xx', '4xx', '5xx', 'timeout'].map((c) => ({ c, n: v(`responses{class:${c}}`, 'count') || 0 }));
const checks = [];
(function walk(g) {
  (g.checks || []).forEach((c) => checks.push(c));
  (g.groups || []).forEach(walk);
})(S.root_group || {});
const business = checks.filter((c) => !c.name.includes(' -> '));

// ---------- kapasitas ----------
function capacity(ts) {
  const L = levels.filter((l) => l.count > 0);
  let sustainable = null;
  for (const l of L) {
    if (l.status !== 'PASS') break;
    sustainable = l;
  }
  const first = L[0];
  const knee = L.find((l) => first && l.p95 > Math.max(SLO.p95, 2 * first.p95));
  const errStart = L.find((l) => (l.err || 0) > SLO.errorRate);
  const breaking = L.find((l) => l.status === 'FAIL');
  const top = L[L.length - 1];
  const hot = top && top.res && top.res.containers[0];
  return {
    maxVus: v('vus_max', 'max'), peakVus: v('vus', 'max'), top, peakRps: ts ? ts.peakRps : undefined,
    maxLevelRps: L.length ? Math.max(...L.map((l) => l.rps)) : undefined, sustainable, knee, errStart, breaking, hot,
    recommendedRps: sustainable ? sustainable.rps * 0.7 : undefined,
    recovery: recoveryTime(ts),
    drift: drift(ts),
  };
}

// Soak: memori container dan p95 pada 60 detik pertama vs terakhir dari masa hold (tanda kebocoran atau degradasi).
function drift(ts) {
  const l = (C.levels || []).find((x) => x.name === 'soak');
  if (!l) return null;
  const a = startEpoch + l.holdStart, b = startEpoch + l.holdEnd;
  const win = (from, to) => {
    const r = resourcesIn(from, to);
    const p = ts ? ts.rows.filter((x) => x.epoch >= from && x.epoch <= to && x.p95 !== undefined).map((x) => x.p95) : [];
    return { mem: Object.fromEntries(r.containers.map((c) => [c.container, c.memMax])), p95: p.length ? p.reduce((s, x) => s + x, 0) / p.length : undefined };
  };
  const first = win(a, a + 60), last = win(b - 60, b);
  const mem = Object.keys(first.mem).map((k) => ({ k, from: first.mem[k], to: last.mem[k], grow: last.mem[k] / first.mem[k] - 1 })).filter((x) => x.from && x.to).sort((x, y) => y.grow - x.grow);
  return { mem, p95From: first.p95, p95To: last.p95 };
}

// Spike: detik sejak lonjakan berakhir sampai p95 per 5 detik kembali <= 1,2x p95 level dasar.
function recoveryTime(ts) {
  const sp = (C.levels || []).find((l) => l.name === 'spike');
  const base = levels.find((l) => l.name === 'base');
  if (!sp || !ts || !base || !base.p95) return undefined;
  const end = startEpoch + sp.holdEnd;
  const after = ts.rows.filter((r) => r.epoch >= end && r.p95 !== undefined);
  const ok = after.find((r) => r.p95 <= base.p95 * 1.2);
  return ok ? Math.max(0, Math.round(ok.epoch - end)) : 'belum pulih dalam jendela uji';
}
const offered = (l) =>
  l.waiters === undefined ? '-' : `${l.waiters} pengantre serentak, katalog ${num(l.browse)} it/s, beli ${num(l.buy)} it/s`;

// ---------- grafik SVG ----------
const COLORS = ['#2f6fde', '#d4692a', '#7b4fd6', '#2a9d8f'];
function lineChart(title, xs, series, opts = {}) {
  const W = 520, H = 220, L = 54, R = 46, T = 14, Bm = 34;
  const all = series.flatMap((s) => s.ys.filter((y) => y !== undefined && !Number.isNaN(y)));
  if (!all.length) return '';
  const xmax = Math.max(...xs, 1), xmin = Math.min(...xs, 0);
  const ymax = Math.max(...all) * 1.1 || 1;
  const X = (x) => L + ((x - xmin) / (xmax - xmin || 1)) * (W - L - R);
  const Y = (y, mx = ymax) => T + (1 - y / mx) * (H - T - Bm);
  const right = series.find((s) => s.right);
  const rmax = right ? Math.max(...right.ys.filter((y) => y !== undefined)) * 1.1 || 1 : 1;
  let g = `<line x1="${L}" y1="${H - Bm}" x2="${W - R}" y2="${H - Bm}" stroke="#bbb"/><line x1="${L}" y1="${T}" x2="${L}" y2="${H - Bm}" stroke="#bbb"/>`;
  for (let i = 0; i <= 4; i++) {
    const y = (ymax * i) / 4;
    g += `<text x="${L - 6}" y="${Y(y) + 4}" font-size="10" text-anchor="end" fill="#666">${num(y, y < 10 ? 1 : 0)}</text><line x1="${L}" x2="${W - R}" y1="${Y(y)}" y2="${Y(y)}" stroke="#f0f0f0"/>`;
    if (right) g += `<text x="${W - R + 6}" y="${Y((rmax * i) / 4, rmax) + 4}" font-size="10" fill="${right.color}">${num((rmax * i) / 4, 0)}</text>`;
  }
  const labels = opts.labels || null;
  xs.forEach((x, i) => {
    if (labels || i % Math.ceil(xs.length / 8) === 0) g += `<text x="${X(x)}" y="${H - Bm + 14}" font-size="10" text-anchor="middle" fill="#666">${esc(labels ? labels[i] : Math.round(x))}</text>`;
  });
  g += `<text x="${(W - R + L) / 2}" y="${H - 4}" font-size="10" text-anchor="middle" fill="#666">${esc(opts.xLabel || '')}</text>`;
  series.forEach((s, si) => {
    const color = s.color || COLORS[si];
    const pts = xs.map((x, i) => (s.ys[i] === undefined || Number.isNaN(s.ys[i]) ? null : `${X(x)},${s.right ? Y(s.ys[i], rmax) : Y(s.ys[i])}`)).filter(Boolean);
    g += `<polyline fill="none" stroke="${color}" stroke-width="1.8" points="${pts.join(' ')}"/>`;
    if (labels) pts.forEach((p) => (g += `<circle cx="${p.split(',')[0]}" cy="${p.split(',')[1]}" r="3" fill="${color}"/>`));
  });
  const legend = series.map((s, si) => `<span><i style="background:${s.color || COLORS[si]}"></i>${esc(s.name)}${s.right ? ' (sumbu kanan)' : ''}</span>`).join('');
  return `<figure><figcaption>${esc(title)}</figcaption><svg viewBox="0 0 ${W} ${H}" width="100%">${g}</svg><div class="legend">${legend}</div></figure>`;
}
function barChart(title, labels, values, unit = '', color = '#2f6fde') {
  if (!values.length) return '';
  const W = 520, rowH = 20, L = 150, H = values.length * rowH + 10;
  const mx = Math.max(...values, 1);
  const g = values.map((val, i) => {
    const w = ((W - L - 70) * val) / mx;
    return `<text x="${L - 6}" y="${i * rowH + 15}" font-size="11" text-anchor="end">${esc(labels[i])}</text><rect x="${L}" y="${i * rowH + 4}" width="${w}" height="13" fill="${color}"/><text x="${L + w + 4}" y="${i * rowH + 15}" font-size="11">${num(val, val < 10 ? 2 : 0)}${unit}</text>`;
  }).join('');
  return `<figure><figcaption>${esc(title)}</figcaption><svg viewBox="0 0 ${W} ${H}" width="100%">${g}</svg></figure>`;
}

// ---------- teks ----------
const PURPOSE = {
  smoke: 'Memastikan semua service hidup dan setiap endpoint utama menjawab benar, termasuk satu transaksi sampai PAID, sebelum tes beban.',
  e2e: 'Menjalankan alur bisnis penuh dari katalog sampai e-tiket dan email, lalu memvalidasi hasil akhir transaksi.',
  concurrency: 'Mengirim request kembar ke resource yang sama untuk membuktikan idempotency, single-use token, dan tidak ada oversell atau tiket ganda.',
  load: 'Mengukur performa pada beban normal yang naik bertahap: throughput, latensi, dan error rate per level.',
  stress: 'Menaikkan beban melewati kondisi normal untuk menemukan titik degradasi dan breaking point.',
  spike: 'Melonjakkan trafik tiba-tiba (10x) lalu kembali normal untuk melihat ketahanan dan pemulihan.',
  soak: 'Menahan beban sedang dalam waktu lama untuk mencari kebocoran memori/koneksi dan penurunan performa bertahap.',
  waiting: 'Mensimulasikan pengantre waiting room dengan pola polling klien (profil A/B/C).',
};
const FLOW = {
  browse: 'Light user: GET /events, /events/facets, /events/{id}, /queue-status/{id}',
  waiters: 'Normal user: POST queue-entries (join), lalu heartbeat, GET queue-status dan /queue-entries/me berulang sesuai nextPollAfterMs',
  buy: 'Heavy user: join, tunggu ADMITTED, claim, POST booking-sessions, POST bookings (hold 1 tiket), POST bookings/{id}/cancel',
  account: 'User login: OTP request (email ke Mailpit), OTP verify, GET /me, GET /me/bookings, refresh, logout',
};

async function main() {
  const ts = await timeseries();
  const cap = capacity(ts);
  const status = overall();
  const total = v('http_reqs', 'count');
  const durS = C.durationMs / 1000;
  const date = new Date(C.startedAt);
  const fmtDate = date.toLocaleString('id-ID', { timeZone: 'Asia/Jakarta', dateStyle: 'full', timeStyle: 'medium' });
  const timeouts = classes.find((x) => x.c === 'timeout').n;
  const fiveXX = classes.find((x) => x.c === '5xx').n;
  const dropped = v('dropped_iterations', 'count') || 0;

  // A. Ringkasan eksekutif
  const tile = (label, value) => `<div class="tile"><span>${esc(label)}</span><b>${esc(value)}</b></div>`;
  let h = `<h1>Laporan uji ${esc(C.type)}: ${esc(C.app)} <span class="badge ${status}">${status}</span></h1>
<p class="muted">Environment <b>${esc(C.env)}</b> · ${esc(fmtDate)} WIB · durasi ${num(durS, 0)} detik · skrip <code>${esc(C.script)}</code> · commit <code>${esc((RUN.commit || '').slice(0, 10))}</code>${RUN.dirty ? ' (ada perubahan belum di-commit)' : ''} · hash skrip <code>${esc(RUN.k6Hash || '-')}</code></p>
<h2>A. Ringkasan eksekutif</h2><div class="tiles">
${tile('Status', status)}${tile('Total request', n0(total))}${tile('RPS rata-rata', num(v('http_reqs', 'rate')))}${tile('RPS puncak (1 detik)', num(cap.peakRps))}
${tile('VU maksimum', n0(cap.maxVus))}${tile('VU serentak puncak', n0(cap.peakVus))}${tile('p95', ms(v('http_req_duration', 'p(95)')))}${tile('p99', ms(v('http_req_duration', 'p(99)')))}
${tile('Error rate', pct(v('http_req_failed', 'rate')))}${tile('Checks lulus', pct(v('checks', 'rate')))}${tile('Timeout', timeouts)}${tile('Dropped iterations', dropped)}
</div>`;
  h += `<p>${statusSentence(status, cap)}</p>`;

  // B. Skenario
  h += `<h2>B. Skenario uji</h2><p>${esc(PURPOSE[C.type] || '')}</p><ul>`;
  if (typeIsLevelled) Object.entries(FLOW).forEach(([k, d]) => (h += `<li><b>${k}</b> (${esc((C.executors || {})[k] || '-')}): ${esc(d)}</li>`));
  else h += `<li>Executor: ${esc(C.executor || '-')}, VU ${esc(C.vus || 1)}, iterasi per VU ${esc(C.iterations || 1)}</li>`;
  h += `</ul><p class="muted">Endpoint yang terlibat: ${endpoints.map((e) => `<code>${esc(e.name)}</code>`).join(' ')}</p>`;
  if (typeIsLevelled) {
    h += `<table><tr><th>Level</th><th>Ramp</th><th>Hold</th><th>Pengantre (VU)</th><th>Katalog (iterasi/s)</th><th>Beli (iterasi/s)</th><th>Login (iterasi/s)</th></tr>`;
    C.levels.forEach((l) => (h += `<tr><td>${l.name}</td><td>${l.ramp}s</td><td>${l.hold}s</td><td>${l.waiters}</td><td>${num(l.browse)}</td><td>${num(l.buy)}</td><td>${num(l.account)}</td></tr>`));
    h += `</table><p class="muted">Arrival rate (iterasi/s) menahan laju kedatangan tetap walau server melambat; VU pengantre mewakili jumlah pengguna serentak. Satu iterasi = beberapa request, jadi iterasi/s bukan RPS.</p>`;
  }
  h += `<p class="muted">SLO: p95 &lt; ${SLO.p95} ms, p99 &lt; ${SLO.p99} ms, error &lt; ${pct(SLO.errorRate)}, checks &gt; ${pct(SLO.checkRate)}. Load factor ${C.loadFactor}.</p>`;

  // C. Hasil performa
  h += `<h2>C. Hasil performa</h2><div class="charts">`;
  if (ts) {
    const xs = ts.rows.map((r) => r.t);
    h += lineChart('Throughput (RPS) dan VU sepanjang waktu', xs, [{ name: 'RPS', ys: ts.rows.map((r) => r.rps) }, { name: 'VU', ys: ts.rows.map((r) => r.vus), right: true, color: '#d4692a' }], { xLabel: 'detik sejak mulai (bucket 5 s)' });
    h += lineChart('Latensi p50 / p95 / p99 sepanjang waktu (ms)', xs, [{ name: 'p50', ys: ts.rows.map((r) => r.p50) }, { name: 'p95', ys: ts.rows.map((r) => r.p95) }, { name: 'p99', ys: ts.rows.map((r) => r.p99) }], { xLabel: 'detik sejak mulai' });
    h += lineChart('Error rate sepanjang waktu (%)', xs, [{ name: 'error %', ys: ts.rows.map((r) => r.err * 100), color: '#cf222e' }, { name: 'RPS', ys: ts.rows.map((r) => r.rps), right: true, color: '#2f6fde' }], { xLabel: 'detik sejak mulai' });
  }
  if (typeIsLevelled) {
    const xs = levels.map((_, i) => i), labels = levels.map((l) => `${l.name} (${l.waiters} VU)`);
    h += lineChart('Per level: VU vs latensi (ms)', xs, [{ name: 'p50', ys: levels.map((l) => l.p50) }, { name: 'p95', ys: levels.map((l) => l.p95) }, { name: 'p99', ys: levels.map((l) => l.p99) }], { labels });
    h += lineChart('Per level: VU vs RPS', xs, [{ name: 'RPS', ys: levels.map((l) => l.rps) }], { labels });
    h += lineChart('Per level: RPS vs error rate (%)', xs, [{ name: 'error %', ys: levels.map((l) => (l.err || 0) * 100), color: '#cf222e' }, { name: 'RPS', ys: levels.map((l) => l.rps), right: true, color: '#2f6fde' }], { labels: levels.map((l) => `${num(l.rps, 0)} rps`) });
  }
  h += barChart('p95 per endpoint (ms)', endpoints.map((e) => e.name), endpoints.map((e) => e.p95), ' ms');
  h += barChart('Distribusi respons', classes.map((x) => x.c), classes.map((x) => x.n), '', '#7b4fd6');
  h += `</div>`;
  if (typeIsLevelled) {
    h += `<h3>Per level beban (angka diukur hanya selama hold)</h3><table><tr><th>Level</th><th>Beban</th><th>Request</th><th>RPS</th><th>p50</th><th>p95</th><th>p99</th><th>maks</th><th>Error</th><th>Checks</th><th>Status</th></tr>`;
    levels.forEach((l) => (h += `<tr><td>${l.name}</td><td>${esc(offered(l))}</td><td>${l.count}</td><td>${num(l.rps)}</td><td>${ms(l.p50)}</td><td>${ms(l.p95)}</td><td>${ms(l.p99)}</td><td>${ms(l.max)}</td><td>${pct(l.err)}</td><td>${pct(l.checks)}</td><td><span class="badge ${l.status}">${l.status}</span></td></tr>`));
    h += `</table>`;
  }
  h += `<h3>Per endpoint</h3><table><tr><th>Endpoint</th><th>Request</th><th>avg</th><th>p95</th><th>p99</th><th>maks</th><th>Error</th></tr>`;
  endpoints.forEach((e) => (h += `<tr><td>${esc(e.name)}</td><td>${e.count}</td><td>${ms(e.avg)}</td><td>${ms(e.p95)}</td><td>${ms(e.p99)}</td><td>${ms(e.max)}</td><td>${pct(e.err)}</td></tr>`));
  h += `</table>`;
  if (business.length) {
    h += `<h3>Validasi bisnis</h3><table><tr><th>Pemeriksaan</th><th>Lulus</th><th>Gagal</th></tr>`;
    business.forEach((c) => (h += `<tr><td>${esc(c.name)}</td><td>${c.passes}</td><td>${c.fails ? `<b style="color:var(--fail)">${c.fails}</b>` : 0}</td></tr>`));
    h += `</table>`;
  }

  // D. Kapasitas
  const capText = capacityText(cap);
  h += `<h2>D. Analisis kapasitas</h2><pre>${esc(capText)}</pre>
<ul><li><b>Maximum tested capacity</b>: beban tertinggi yang dijalankan, apa pun hasilnya.</li>
<li><b>Maximum observed throughput</b>: RPS tertinggi yang teramati (puncak 1 detik dan rata-rata level).</li>
<li><b>Maximum sustainable capacity</b>: level tertinggi yang, beserta semua level di bawahnya, memenuhi SLO.</li>
<li><b>Recommended operating capacity</b>: 70% dari kapasitas berkelanjutan, menyisakan 30% untuk lonjakan dan variasi.</li></ul>
<p class="note">Jumlah VU bukan jumlah pengguna aktif sebenarnya, dan VU bukan RPS. Kapasitas disimpulkan dari RPS, latensi, error rate, dropped iterations, dan sumber daya, bukan dari jumlah VU yang berhasil berjalan.${C.env === 'local' ? ' Uji lokal: k6 dan seluruh stack berbagi CPU laptop yang sama, jadi angka ini batas bawah dan tidak mewakili kapasitas produksi.' : ''}</p>`;

  // E. Bottleneck
  h += `<h2>E. Bottleneck dan rekomendasi</h2>`;
  if (measured) {
    const lv = typeIsLevelled ? levels : levels.slice(0, 1);
    const names = [...new Set(lv.flatMap((l) => (l.res ? l.res.containers.slice(0, 6).map((c) => c.container) : [])))];
    h += `<h3>Sumber daya per level (CPU 100% = 1 core)</h3><table><tr><th>Container</th>${lv.map((l) => `<th>${l.name} CPU avg/maks</th>`).join('')}</tr>`;
    names.forEach((n) => {
      h += `<tr><td>${esc(n)}</td>${lv.map((l) => {
        const c = l.res && l.res.containers.find((x) => x.container === n);
        return `<td>${c ? `${num(c.cpuAvg, 0)}% / ${num(c.cpuMax, 0)}% · ${num(c.memMax, 0)} MB` : '-'}</td>`;
      }).join('')}</tr>`;
    });
    h += `<tr><td>Koneksi PostgreSQL (maks)</td>${lv.map((l) => `<td>${n0(l.res && l.res.pg)}</td>`).join('')}</tr>
<tr><td>Sesi PostgreSQL menunggu lock (maks)</td>${lv.map((l) => `<td>${n0(l.res && l.res.pgLock)}</td>`).join('')}</tr>
<tr><td>Backlog RabbitMQ (maks)</td>${lv.map((l) => `<td>${n0(l.res && l.res.mq)}</td>`).join('')}</tr>
<tr><td>Valkey memori MB (maks) / ops/s (avg)</td>${lv.map((l) => `<td>${l.res ? `${num(l.res.vkMem)} / ${num(l.res.vkOps, 0)}` : '-'}</td>`).join('')}</tr></table>`;
  } else h += `<p><span class="badge NOT-MEASURED">NOT MEASURED</span> CPU, memori, koneksi database, dan backlog antrean tidak diukur di environment ini.</p>`;
  h += `<p class="muted">Latensi query database dan pemakaian connection pool per service: NOT MEASURED (butuh pg_stat_statements atau metrik pool dari Prometheus, jalankan <code>make obs</code>).</p>`;
  h += `<ul>${recommendations(cap, timeouts, fiveXX, dropped).map((r) => `<li>${r}</li>`).join('')}</ul>`;
  h += `<h2>Artefak</h2><ul><li><a href="report.html">report.html</a>: dashboard deret waktu bawaan k6</li><li><a href="summary.json">summary.json</a>, <a href="levels.csv">levels.csv</a>, <a href="endpoints.csv">endpoints.csv</a>, <a href="timeseries.csv">timeseries.csv</a>, <a href="capacity.md">capacity.md</a></li><li>metrics.json.gz (sampel mentah), resources.csv / infra.csv, run.json, k6.log</li></ul>`;

  const tpl = fs.readFileSync(path.join(HERE, '../dashboards/report-template.html'), 'utf8');
  fs.writeFileSync(path.join(DIR, 'analysis.html'), tpl.replace('{{TITLE}}', `k6 ${C.type} ${C.env}`).replace('{{CONTENT}}', h));
  fs.writeFileSync(path.join(DIR, 'capacity.md'), `# Analisis kapasitas: ${C.type} @ ${C.env} (${C.startedAt})\n\nStatus: ${status}\n\n\`\`\`text\n${capText}\n\`\`\`\n\n${levelTableMd()}\n`);
  const csvOut = (rows, cols) => [cols.join(','), ...rows.map((r) => cols.map((c) => (r[c] === undefined ? '' : typeof r[c] === 'number' ? +r[c].toFixed(3) : r[c])).join(','))].join('\n');
  fs.writeFileSync(path.join(DIR, 'levels.csv'), csvOut(levels, ['name', 'waiters', 'browse', 'buy', 'count', 'rps', 'avg', 'p50', 'p90', 'p95', 'p99', 'max', 'err', 'checks', 'status']));
  fs.writeFileSync(path.join(DIR, 'endpoints.csv'), csvOut(endpoints, ['name', 'count', 'avg', 'p95', 'p99', 'max', 'err']));
  if (ts) fs.writeFileSync(path.join(DIR, 'timeseries.csv'), csvOut(ts.rows, ['t', 'epoch', 'vus', 'rps', 'err', 'p50', 'p95', 'p99']));
  const result = {
    type: C.type, env: C.env, startedAt: C.startedAt, commit: (RUN.commit || '').slice(0, 10), dirty: Boolean(RUN.dirty),
    vusMax: cap.maxVus, requests: total, rpsAvg: v('http_reqs', 'rate'), rpsPeak: cap.peakRps,
    p95: v('http_req_duration', 'p(95)'), p99: v('http_req_duration', 'p(99)'), errorRate: v('http_req_failed', 'rate'),
    checks: v('checks', 'rate'), sustainableRps: FUNCTIONAL || !cap.sustainable ? null : cap.sustainable.rps,
    breaking: cap.breaking ? cap.breaking.name : null, status,
  };
  fs.writeFileSync(path.join(DIR, 'result.json'), JSON.stringify(result, null, 1));
  updateSummary(result);
  console.log(`${C.type} @ ${C.env}: ${status}`);
}

// RINGKASAN.md per environment: tabel hasil terbaru per jenis (dibuat ulang) + riwayat benchmark (satu baris per run,
// tidak pernah dihapus). Teks di luar blok otomatis ditulis manual dan tidak disentuh.
function updateSummary(r) {
  const envDir = path.dirname(path.resolve(DIR));
  const f = path.join(envDir, 'RINGKASAN.md');
  const START = '<!-- auto:terbaru:mulai -->', END = '<!-- auto:terbaru:selesai -->', HIST = '<!-- auto:riwayat -->';
  const wib = (iso) => new Date(iso).toLocaleString('sv-SE', { timeZone: 'Asia/Jakarta' });
  const cells = (x) => [n0(x.vusMax), n0(x.requests), num(x.rpsAvg), num(x.rpsPeak), ms(x.p95), ms(x.p99), pct(x.errorRate)];
  let md = fs.existsSync(f) ? fs.readFileSync(f, 'utf8') : `# Benchmark performa: ${r.env}

Dibuat dan diperbarui otomatis oleh \`tests/k6/scripts/report.mjs\` setiap run. Folder per jenis tes hanya menyimpan hasil
terbaru; perbandingan antar-run ada di riwayat di bawah. Teks di luar blok otomatis boleh diedit manual.

## Hasil terbaru per jenis

${START}
${END}

## Riwayat benchmark

Satu baris per run, terbaru di atas. RPS berkelanjutan = level tertinggi yang memenuhi SLO (hanya tes beban).

${HIST}
| Waktu (WIB) | Jenis | Commit | VU maks | Request | RPS rata-rata | RPS puncak | p95 | p99 | Error | RPS berkelanjutan | Status |
|---|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---|

## Analisis

(tulis manual)
`;
  const latest = fs.readdirSync(envDir)
    .map((d) => path.join(envDir, d, 'result.json'))
    .filter((p) => fs.existsSync(p))
    .map((p) => JSON.parse(fs.readFileSync(p, 'utf8')))
    .sort((a, b) => a.type.localeCompare(b.type));
  const table = ['| Jenis | Waktu (WIB) | Commit | VU maks | Request | RPS rata-rata | RPS puncak | p95 | p99 | Error | Status | Laporan |',
    '|---|---|---|---:|---:|---:|---:|---:|---:|---:|---|---|',
    ...latest.map((x) => `| ${x.type} | ${wib(x.startedAt)} | ${x.commit}${x.dirty ? '*' : ''} | ${cells(x).join(' | ')} | ${x.status} | [analysis](${x.type}/analysis.html) |`),
    '', '\\* commit memiliki perubahan yang belum di-commit saat run.'].join('\n');
  md = md.replace(new RegExp(`${START}[\\s\\S]*?${END}`), `${START}\n${table}\n${END}`);
  const row = `| ${wib(r.startedAt)} | ${r.type} | ${r.commit}${r.dirty ? '*' : ''} | ${cells(r).join(' | ')} | ${r.sustainableRps == null ? '-' : num(r.sustainableRps)} | ${r.status}${r.breaking ? ` (breaking ${r.breaking})` : ''} |`;
  if (!md.includes(`| ${wib(r.startedAt)} | ${r.type} |`)) {
    const lines = md.split('\n');
    const at = lines.indexOf(HIST);
    if (at >= 0) lines.splice(at + 3, 0, row); // setelah header dan pemisah tabel
    md = lines.join('\n');
  }
  fs.writeFileSync(f, md);
}

function statusSentence(status, cap) {
  const s = cap.sustainable;
  if (C.type === 'concurrency') return status === 'PASS' ? 'Semua invariant bisnis bertahan di bawah request bersamaan.' : 'Ada invariant bisnis yang dilanggar di bawah request bersamaan; lihat tabel validasi bisnis.';
  if (C.type === 'smoke' || C.type === 'e2e') return status === 'PASS' ? 'Seluruh alur dan validasi bisnis lulus.' : 'Ada langkah atau validasi yang gagal; lihat tabel validasi bisnis.';
  if (!s) return 'Level pertama sudah tidak memenuhi SLO: kapasitas berkelanjutan belum ditemukan di rentang yang diuji.';
  return `Sistem memenuhi SLO sampai level ${s.name} (${offered(s)}), sekitar ${num(s.rps, 0)} RPS.${cap.breaking ? ` Breaking point teramati di ${cap.breaking.name}.` : ' Breaking point belum tercapai di rentang yang diuji.'}`;
}

const FUNCTIONAL = ['smoke', 'e2e', 'concurrency'].includes(C.type);

function capacityText(cap) {
  const L = (k, val) => `${k.padEnd(34)}: ${val}`;
  if (FUNCTIONAL) {
    return ['CAPACITY ANALYSIS', '', L('Virtual Users', n0(cap.maxVus)), L('Observed RPS (1 s peak)', num(cap.peakRps)),
      L('Overall P95 / P99', `${ms(v('http_req_duration', 'p(95)'))} / ${ms(v('http_req_duration', 'p(99)'))}`),
      '', 'Kapasitas TIDAK DISIMPULKAN: ini tes fungsional dengan beban sangat kecil.', 'Gunakan hasil load/stress untuk kapasitas.'].join('\n');
  }
  const t = cap.top;
  const lines = [
    'CAPACITY ANALYSIS',
    '',
    L('Maximum Tested Load', t ? `${t.name}: ${offered(t)}` : '-'),
    L('Maximum Tested Virtual Users', n0(cap.maxVus)),
    L('Peak Concurrent VUs', n0(cap.peakVus)),
    L('Maximum Observed RPS (1 s peak)', num(cap.peakRps)),
    L('Maximum Level Avg RPS', num(cap.maxLevelRps)),
    L('Overall Avg RPS', num(v('http_reqs', 'rate'))),
    L('Overall P95 / P99', `${ms(v('http_req_duration', 'p(95)'))} / ${ms(v('http_req_duration', 'p(99)'))}`),
    L('Overall Error Rate', pct(v('http_req_failed', 'rate'))),
    '',
    L('Maximum Sustainable Capacity', cap.sustainable ? `${cap.sustainable.name}: ${offered(cap.sustainable)}` : 'BELUM DITEMUKAN (level pertama tidak lulus SLO)'),
    L('Maximum Sustainable RPS', cap.sustainable ? num(cap.sustainable.rps) : '-'),
    L('Recommended Operating RPS (70%)', cap.recommendedRps !== undefined ? num(cap.recommendedRps) : '-'),
    L('Latency Knee (p95 > 2x awal / SLO)', cap.knee ? `${cap.knee.name} (p95 ${ms(cap.knee.p95)})` : 'tidak teramati'),
    L('Error Rate Starts Rising', cap.errStart ? `${cap.errStart.name} (${pct(cap.errStart.err)})` : 'tidak teramati'),
    L('Breaking Point (status FAIL)', cap.breaking ? cap.breaking.name : 'tidak tercapai di rentang uji'),
    ...(C.type === 'spike' ? [L('Recovery Time After Spike', cap.recovery === undefined ? '-' : typeof cap.recovery === 'number' ? `${cap.recovery} s (bucket 5 s)` : cap.recovery)] : []),
    ...(cap.drift ? [L('p95 menit pertama -> terakhir', `${ms(cap.drift.p95From)} -> ${ms(cap.drift.p95To)}`),
      ...cap.drift.mem.slice(0, 4).map((m) => L(`Memori ${m.k}`, `${num(m.from)} -> ${num(m.to)} MB (${m.grow >= 0 ? '+' : ''}${(m.grow * 100).toFixed(0)}%)`))] : []),
    L('Hottest Container at Top Level', cap.hot ? `${cap.hot.container} CPU avg ${num(cap.hot.cpuAvg, 0)}%` : measured ? '-' : 'NOT MEASURED'),
  ];
  return lines.join('\n');
}

function levelTableMd() {
  if (!typeIsLevelled) return '';
  return ['| Level | Beban | RPS | p95 | p99 | Error | Status |', '|---|---|---:|---:|---:|---:|---|',
    ...levels.map((l) => `| ${l.name} | ${offered(l)} | ${num(l.rps)} | ${ms(l.p95)} | ${ms(l.p99)} | ${pct(l.err)} | ${l.status} |`)].join('\n');
}

function recommendations(cap, timeouts, fiveXX, dropped) {
  const r = [];
  const slow = endpoints[0];
  if (slow) r.push(`Endpoint paling lambat: <code>${esc(slow.name)}</code> (p95 ${ms(slow.p95)}, ${slow.count} request).`);
  const errE = endpoints.filter((e) => e.err > 0).sort((a, b) => b.err - a.err)[0];
  if (errE) r.push(`Error tertinggi: <code>${esc(errE.name)}</code> (${pct(errE.err)}); periksa log service untuk errorCode-nya.`);
  const me = endpoints.find((e) => e.name === 'queue.me');
  const total = endpoints.reduce((a, e) => a + e.count, 0);
  if (me && total && me.count / total > 0.3) r.push(`<code>queue.me</code> menyumbang ${pct(me.count / total, 0)} request. Polling bertingkat dan zona jauh tanpa /me (poin 2 dan 3) memangkas beban ini paling besar.`);
  if (cap.hot && cap.hot.cpuAvg > 80) r.push(`<code>${esc(cap.hot.container)}</code> memakai CPU rata-rata ${num(cap.hot.cpuAvg, 0)}% di level tertinggi: kandidat bottleneck aplikasi; tambah instance atau profil (pprof) jalur panasnya.`);
  if (fiveXX) r.push(`${fiveXX} respons 5xx: cari errorCode di log; 503 DEPENDENCY_UNAVAILABLE berarti dependency (DB/Valkey/service lain) lambat atau penuh.`);
  if (timeouts) r.push(`${timeouts} request timeout (30 s): service atau proxy berhenti menjawab di beban itu.`);
  if (dropped) r.push(`${dropped} iterasi terbuang (dropped iterations): server terlalu lambat atau generator kekurangan VU; naikkan maxVUs atau anggap ini tanda saturasi.`);
  const pg = Math.max(...levels.map((l) => (l.res && l.res.pg) || 0));
  if (pg > 80) r.push(`Koneksi PostgreSQL mencapai ${pg}: dekat batas default 100, atur ukuran pool per service.`);
  const lockW = Math.max(...levels.map((l) => (l.res && l.res.pgLock) || 0));
  if (lockW > 0) r.push(`Sampai ${lockW} sesi PostgreSQL menunggu lock: kontensi baris (mis. stok event yang sama) memperlambat endpoint tulis.`);
  const mq = Math.max(...levels.map((l) => (l.res && l.res.mq) || 0));
  if (mq > 100) r.push(`Backlog RabbitMQ mencapai ${mq} pesan: worker tidak mengimbangi; tambah consumer atau periksa worker yang lambat.`);
  if (cap.drift) {
    const leak = cap.drift.mem.filter((m) => m.grow > 0.2 && m.to - m.from > 20);
    r.push(leak.length ? `Memori naik >20% selama soak: ${leak.map((m) => esc(m.k)).join(', ')}; jalankan soak lebih lama untuk memastikan kebocoran.` : 'Tidak ada pertumbuhan memori berarti selama soak (tidak ada tanda kebocoran pada durasi ini).');
  }
  if (!r.length) r.push('Tidak ada bottleneck yang menonjol di rentang beban ini; naikkan beban (stress) untuk menemukannya.');
  if (C.env === 'local') r.push('Ulangi di dev/prod (k6 di mesin terpisah) sebelum memakai angka ini untuk keputusan kapasitas.');
  return r;
}

main().catch((e) => {
  console.error(e);
  process.exit(1);
});
