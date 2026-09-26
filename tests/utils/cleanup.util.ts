import playwrightConfig from '../playwright.config';

// Wipes the backend's database and recreates the default workspace, so every spec starts from a clean state
export async function cleanupBackend() {
	const url = new URL('/api/test/reset', playwrightConfig.use!.baseURL);
	const response = await fetch(url, { method: 'POST' });

	if (!response.ok) {
		throw new Error(`Failed to reset backend: ${response.status} ${response.statusText}`);
	}
}
