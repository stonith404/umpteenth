import { test as setup } from '@playwright/test';
import { cleanupBackend } from '../../utils/cleanup.util';
import { pathFromRoot } from '../../utils/fs.util';

const authFile = pathFromRoot('.tmp/auth/user.json');

// Stores a session as storageState, so browser specs start signed in without OIDC
setup('authenticate', async ({ request }) => {
	await cleanupBackend();

	// The session endpoint answers with the session cookie, which the request context keeps
	const response = await request.post('/api/test/session');
	if (!response.ok()) {
		throw new Error(
			`Failed to create a test session: ${response.status()} ${response.statusText()}`
		);
	}

	await request.storageState({ path: authFile });
});
