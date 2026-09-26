import type { APIRequestContext } from '@playwright/test';

// Wipes the backend's database and recreates the default workspace, so every spec starts from a clean state
export async function cleanupBackend(request: APIRequestContext) {
	const response = await request.post('/api/test/reset');
	if (!response.ok()) {
		throw new Error(`Failed to reset backend: ${response.status()} ${response.statusText()}`);
	}
}
