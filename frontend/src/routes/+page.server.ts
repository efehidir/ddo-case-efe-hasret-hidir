import { env } from '$env/dynamic/private';
import type { PageServerLoad } from './$types';
import type { DatabaseDiagnosticResponse, HealthResponse } from '#lib/types';

export const load: PageServerLoad = async ({ fetch }) => {
	const internalApiUrl = env.INTERNAL_API_URL || 'http://backend:8080';
	const publicApiUrl = env.PUBLIC_API_URL || 'http://localhost:8080';

	let health: HealthResponse | null = null;
	let dbDiagnostics: DatabaseDiagnosticResponse | null = null;
	let errorMessage: string | null = null;

	try {
		const [healthRes, diagRes] = await Promise.all([
			fetch(`${internalApiUrl}/health`),
			fetch(`${internalApiUrl}/api/v1/db/check`)
		]);

		if (healthRes.ok || healthRes.status === 503) {
			health = (await healthRes.json()) as HealthResponse;
		}
		if (diagRes.ok || diagRes.status === 503) {
			dbDiagnostics = (await diagRes.json()) as DatabaseDiagnosticResponse;
		}
	} catch (err) {
		errorMessage = err instanceof Error ? err.message : 'Backend servisine erişilemedi.';
	}

	return {
		health,
		dbDiagnostics,
		errorMessage,
		publicApiUrl,
		checkedAt: new Date().toISOString()
	};
};
