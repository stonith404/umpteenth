import {
	expect,
	type APIRequestContext,
	type Browser,
	type Page,
	type TestInfo
} from '@playwright/test';
import authUtil, { type TestAccount } from './auth.util';

// The workspace everyone starts in, whose ID is fixed so every replica creates the same one
export const DEFAULT_WORKSPACE_ID = '00000000-0000-7000-8000-000000000001';

// The sidebar's workspace switcher, whose label names the workspace the session is in and the role there
export function workspaceSwitcher(page: Page) {
	return page.getByRole('button', { name: /switch workspaces/ });
}

// Creates an invite link through the API and returns its path
// The link is built from APP_URL, which can differ from the base URL the tests reach the app on, so only the path is used
export async function createInviteLink(
	request: APIRequestContext,
	role: 'member' | 'admin' = 'member'
) {
	const response = await request.post('/api/workspace/invites', {
		data: { role, expiresInDays: 7 }
	});
	expect(response.ok()).toBeTruthy();
	const body = (await response.json()) as { url: string };
	return new URL(body.url).pathname;
}

// Signs someone else into the owner's workspace with the role and returns their page, whose context the caller closes
// With workspaces turned on they join through an invite link, and with them off everyone who signs in joins the one workspace as a member anyway
export async function joinWorkspace(
	browser: Browser,
	testInfo: TestInfo,
	owner: Page,
	account: TestAccount,
	role: 'member' | 'admin' = 'member'
) {
	if (!(await authUtil.workspacesEnabled(owner))) {
		return authUtil.pageAs(browser, testInfo, account);
	}
	const redirect = await createInviteLink(owner.request, role);
	return authUtil.pageAs(browser, testInfo, { ...account, redirect });
}

// Creates a workspace secret and returns its ID
export async function createSecret(request: APIRequestContext, name: string, value: string) {
	const response = await request.post('/api/secrets', { data: { name, value } });
	expect(response.ok()).toBeTruthy();
	return ((await response.json()) as { id: string }).id;
}
