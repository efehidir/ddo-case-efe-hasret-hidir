import http from 'k6/http';
import { check, sleep } from 'k6';
import { Rate, Trend } from 'k6/metrics';

const errorRate = new Rate('errors');
const healthLatency = new Trend('health_duration_ms', true);
const dbCheckLatency = new Trend('db_check_duration_ms', true);
const metricsLatency = new Trend('metrics_duration_ms', true);

export const options = {
	scenarios: {
		steady_load: {
			executor: 'ramping-vus',
			startVUs: 0,
			stages: [
				{ duration: '10s', target: 25 },
				{ duration: '25s', target: 50 },
				{ duration: '5s', target: 0 }
			],
			gracefulRampDown: '5s'
		}
	},
	summaryTrendStats: ['avg', 'min', 'med', 'max', 'p(50)', 'p(90)', 'p(95)', 'p(99)'],
	thresholds: {
		http_req_failed: ['rate<0.01'],
		http_req_duration: ['p(50)<15', 'p(95)<50', 'p(99)<100'],
		health_duration_ms: ['p(95)<30', 'p(99)<60'],
		db_check_duration_ms: ['p(95)<60', 'p(99)<120'],
		errors: ['rate<0.01']
	}
};

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';

export default function () {
	// 1. GET /health
	const healthRes = http.get(`${BASE_URL}/health`, {
		tags: { endpoint: 'health' }
	});
	healthLatency.add(healthRes.timings.duration);
	const healthOk = check(healthRes, {
		'GET /health status is 200': (r) => r.status === 200,
		'GET /health database is up': (r) => r.body && r.body.includes('"status":"up"')
	});
	errorRate.add(!healthOk);

	// 2. GET /api/v1/db/check
	const dbRes = http.get(`${BASE_URL}/api/v1/db/check`, {
		tags: { endpoint: 'db_check' }
	});
	dbCheckLatency.add(dbRes.timings.duration);
	const dbOk = check(dbRes, {
		'GET /api/v1/db/check status is 200': (r) => r.status === 200,
		'GET /api/v1/db/check connected is true': (r) => r.body && r.body.includes('"connected":true')
	});
	errorRate.add(!dbOk);

	// 3. GET /metrics
	const metricsRes = http.get(`${BASE_URL}/metrics`, {
		tags: { endpoint: 'metrics' }
	});
	metricsLatency.add(metricsRes.timings.duration);
	const metricsOk = check(metricsRes, {
		'GET /metrics status is 200': (r) => r.status === 200,
		'GET /metrics has http_requests_total': (r) =>
			r.body && r.body.includes('http_requests_total')
	});
	errorRate.add(!metricsOk);

	sleep(0.05);
}

