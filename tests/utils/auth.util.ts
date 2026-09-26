import type { Browser, Page, TestInfo } from '@playwright/test';

// An account the test endpoints sign in as, where every field falls back to the default e2e user
export type TestAccount = {
	subject?: string;
	email?: string;
	// Whether the sign-in provider vouched for the email address, true when left out
	emailVerified?: boolean;
	name?: string;
	// Signs in as an instance admin
	admin?: boolean;
	// Where the login returns to, such as an invite page
	redirect?: string;
};

// Signs the page's browser context in as the e2e test user, without going through a sign-in provider
// A reset deletes the user and recreates the workspace, so sessions from before a reset no longer work and specs sign in again after resetting
// The session can pretend to come from a sign-in provider, which the login page then remembers as the last one used
async function authenticate(page: Page, provider?: string) {
	await signInAs(page, {}, provider);
}

// Signs the page's browser context in as the account, following the same rules as a real sign-in, and returns where that login would redirect to
async function signInAs(page: Page, account: TestAccount, provider?: string) {
	const response = await page.request.post('/api/test/session', {
		params: provider ? { provider } : undefined,
		data: account
	});
	if (!response.ok()) {
		throw new Error(
			`Failed to create a test session: ${response.status()} ${response.statusText()}`
		);
	}
	const body = (await response.json()) as { userId: string; redirect: string };
	return body;
}

// Opens a page in a browser context of its own, signed in as another user
async function pageAs(browser: Browser, testInfo: TestInfo, account: TestAccount) {
	const context = await browser.newContext({ baseURL: testInfo.project.use.baseURL });
	const page = await context.newPage();
	await signInAs(page, account);
	return page;
}

// Whether the backend under test lets people have several workspaces, which decides which specs apply
async function workspacesEnabled(page: Page) {
	const response = await page.request.get('/api/users/me');
	return ((await response.json()) as { workspacesEnabled: boolean }).workspacesEnabled;
}

export default { authenticate, signInAs, pageAs, workspacesEnabled };
