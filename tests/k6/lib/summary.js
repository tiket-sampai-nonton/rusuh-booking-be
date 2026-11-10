// handleSummary bersama: tulis summary.json (metrik k6 + konteks uji) ke REPORT_DIR. setup_data dibuang karena
// memuat token admin. Ringkasan singkat dicetak ke stdout; laporan lengkap dibuat scripts/report.mjs.
import { TEST_ENV } from '../config/environments.js';
import { SLO } from '../config/thresholds.js';

export function summary(type, windows, extra = {}) {
  return (data) => {
    const dir = __ENV.REPORT_DIR || '.';
    const endedAt = new Date().toISOString();
    const startedAt = new Date(Date.now() - data.state.testRunDurationMs).toISOString();
    const out = {
      context: {
        app: 'rusuh-booking-be', type, env: TEST_ENV, startedAt, endedAt, durationMs: data.state.testRunDurationMs,
        script: __ENV.SCRIPT_PATH || '', runId: __ENV.RUN_ID || '', loadFactor: Number(__ENV.LOAD_FACTOR || 1), slo: SLO,
        levels: windows || [], scenarioStart: data.setup_data && data.setup_data.t0 ? new Date(data.setup_data.t0).toISOString() : null, ...extra,
      },
      metrics: data.metrics,
      root_group: data.root_group,
    };
    const m = data.metrics;
    const v = (k, f) => (m[k] && m[k].values[f] !== undefined ? m[k].values[f] : NaN);
    const line = [
      `${type} @ ${TEST_ENV}: ${v('http_reqs', 'count')} request, ${v('http_reqs', 'rate').toFixed(1)} req/s`,
      `p95 ${v('http_req_duration', 'p(95)').toFixed(0)} ms, p99 ${v('http_req_duration', 'p(99)').toFixed(0)} ms`,
      `gagal ${(v('http_req_failed', 'rate') * 100).toFixed(2)}%, checks ${(v('checks', 'rate') * 100).toFixed(2)}%`,
    ].join(' | ');
    return { [`${dir}/summary.json`]: JSON.stringify(out, null, 1), stdout: `\n${line}\n` };
  };
}
