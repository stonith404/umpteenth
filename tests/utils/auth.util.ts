import type { Page } from '@playwright/test';

// Signs the page's browser context in as the e2e test user, without going through OIDC
// A reset deletes the user and recreates the workspace, so sessions from before a reset no longer work and specs sign in again after resetting
async function authenticate(page: Page) {
	const response = await page.request.post('/api/test/session');
	if (!response.ok()) {
		throw new Error(
			`Failed to create a test session: ${response.status()} ${response.statusText()}`
		);
	}
}

export default { authenticate };
