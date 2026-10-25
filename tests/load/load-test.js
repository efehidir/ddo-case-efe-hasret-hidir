import http from 'k6/http';
import { check, sleep } from 'k6';
import { Rate, Trend } from 'k6/metrics';

const errors = new Rate('business_errors');
const mutationErrors = new Rate('mutation_errors');
const reads = new Trend('read_duration_ms', true);
const writes = new Trend('write_duration_ms', true);
const BASE = __ENV.BASE_URL || 'http://backend:8080';
export const options = {
  scenarios: {
    reads: { executor: 'ramping-vus', exec: 'read', startVUs: 0,
      stages: [{ duration: '10s', target: 25 }, { duration: '25s', target: 50 }, { duration: '5s', target: 0 }] },
    ...Object.fromEntries(Array.from({ length: 8 }, (_, i) => [`branch_${i + 1}`, {
      executor: 'constant-vus', exec: 'order', vus: 1, duration: '40s', env: { BRANCH_ID: String(i + 1) }
    }]))
  },
  summaryTrendStats: ['avg', 'min', 'med', 'max', 'p(50)', 'p(95)', 'p(99)'],
  thresholds: {
    http_req_failed: ['rate<0.01'], business_errors: ['rate<0.01'], mutation_errors: ['rate<0.01'],
    http_req_duration: ['p(95)<500', 'p(99)<1000'],
    write_duration_ms: ['p(95)<500', 'p(99)<1000']
  }
};

export function setup() {
  const response = http.post(`${BASE}/api/v1/auth/login`, JSON.stringify({
    email: 'merkez@sofrapos.local', password: 'Sofra2026!'
  }), { headers: { 'Content-Type': 'application/json' } });
  if (response.status !== 200) throw new Error(`Login failed: ${response.status}`);
  return { token: response.json('access_token'), run: String(Date.now()) };
}

export function read(data) {
  for (const path of ['/health', '/api/v1/menu', '/api/v1/kds/items?branch_id=1', '/api/v1/reports/hq']) {
    const response = http.get(`${BASE}${path}`, { headers: { Authorization: `Bearer ${data.token}` }, tags: { endpoint: path.split('?')[0] } });
    reads.add(response.timings.duration);
    errors.add(!check(response, { 'read succeeded': r => r.status === 200 }));
  }
  sleep(0.2);
}

export function order(data) {
  // Each of the eight VUs owns a distinct branch/table; no shared-order false failures.
  const branch = Number(__ENV.BRANCH_ID);
  const prefix = `load-${data.run}-${__VU}-${__ITER}`;
  function post(path, body, step) {
    const response = http.post(`${BASE}/api/v1${path}`, JSON.stringify(body), {
      headers: { Authorization: `Bearer ${data.token}`, 'Content-Type': 'application/json', 'Idempotency-Key': `${prefix}-${step}` },
      tags: { endpoint: step }
    });
    writes.add(response.timings.duration);
    const ok = check(response, { 'mutation succeeded': r => r.status === 200 || r.status === 201 });
    errors.add(!ok);
    mutationErrors.add(!ok);
    return ok ? response.json() : null;
  }
  const opened = post('/orders', { branch_id: branch, table_id: (branch - 1) * 10 + 10 }, 'open');
  if (!opened) { sleep(1); return; }
  const added = post(`/orders/${opened.id}/items`, { menu_item_id: 4, quantity: 1 }, 'add');
  if (!added) { sleep(1); return; }
  const paid = post(`/orders/${opened.id}/payments`, { method: 'CARD', split_mode: 'FULL' }, 'pay');
  if (paid) errors.add(!check(paid, { 'payment closed exact balance': o => o.status === 'PAID' && o.paid_kurus === o.total_kurus }));
  sleep(1);
}

// k6's legacy summary export also serializes setup_data (including the JWT).
// Persist only metrics/checks; credentials must never enter committed evidence.
export function handleSummary(data) {
  delete data.setup_data;
  const lines = ['k6 final summary (milliseconds)', 'metric | p50 | p95 | p99'];
  for (const name of ['http_req_duration', 'read_duration_ms', 'write_duration_ms']) {
    const v = data.metrics[name].values;
    lines.push(`${name} | ${v['p(50)'].toFixed(3)} | ${v['p(95)'].toFixed(3)} | ${v['p(99)'].toFixed(3)}`);
  }
  for (const name of ['http_reqs', 'http_req_failed', 'mutation_errors', 'business_errors', 'checks']) {
    lines.push(`${name}: ${JSON.stringify(data.metrics[name].values)}`);
  }
  const failed = Object.entries(data.metrics).flatMap(([name, metric]) =>
    Object.entries(metric.thresholds || {}).filter(([, t]) => !t.ok).map(([rule]) => `${name}: ${rule}`));
  lines.push(`Thresholds: ${failed.length ? 'FAIL ' + failed.join(', ') : 'PASS'}`);
  return { stdout: lines.join('\n') + '\n', '/results/load-summary.json': JSON.stringify(data, null, 2) + '\n' };
}
