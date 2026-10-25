import { env } from 'node:process';
import type { PageServerLoad } from './$types';
import type { HealthResponse } from '#lib/types.js';

export const load: PageServerLoad = async ({ fetch }) => {
	const internalApiUrl = env.INTERNAL_API_URL || 'http://backend:8080';
	const publicApiUrl = env.PUBLIC_API_URL || 'http://localhost:8080';

	let health: HealthResponse | null = null;
	let errorMessage: string | null = null;

	try {
		const healthRes = await fetch(`${internalApiUrl}/health`, {
			signal: AbortSignal.timeout(5000)
		});

		if (healthRes.ok || healthRes.status === 503) {
			health = (await healthRes.json()) as HealthResponse;
		}
	} catch (err) {
		errorMessage = err instanceof Error ? err.message : 'Backend servisine erişilemedi.';
	}

	return {
		health,
		dbDiagnostics: null,
		errorMessage,
		publicApiUrl,
		checkedAt: new Date().toISOString()
	};
};
