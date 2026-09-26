import { expect, test, type Page } from '@playwright/test';
import authUtil, { accounts } from '../utils/auth.util';
import { cleanupBackend } from '../utils/cleanup.util';
import { fieldError, saveForm } from '../utils/form.util';
import { card } from '../utils/ui.util';
import { createSecret, joinWorkspace } from '../utils/workspace.util';

test.beforeEach(async ({ page }) => {
	await cleanupBackend(page.request);
	await authUtil.authenticate(page);
});

// The fields of GET /api/settings that these specs check
type Settings = {
	defaultImage: string;
	defaultLimits: Record<string, number>;
	dailySpendLimitUsd: number;
	retentionDays: number;
	notifyWebhookUrl: string | null;
};

async function getSettings(page: Page) {
	const response = await page.request.get('/api/settings');
	expect(response.ok()).toBeTruthy();
	return (await response.json()) as Settings;
}

// Records the body of every request the page sends to an endpoint with the method, to tell a refusal in the browser from one by the server
// A refused save sends nothing, which only a later request that did go out can settle, since requests are reported in the order they are sent
function recordRequests(page: Page, method: string, pathname: string) {
	const bodies: unknown[] = [];
	page.on('request', (request) => {
		if (request.method() === method && new URL(request.url()).pathname === pathname) {
			bodies.push(request.postDataJSON());
		}
	});
	return bodies;
}

const webhookUrl = 'https://hooks.slack.com/services/T000/B000/XXXXSECRET';

test('General settings cards refuse out-of-range values without saving them, keep the edits and save once they are fixed', async ({
	page
}) => {
	// Every settings save the page sends, next to the values it started from
	const before = await getSettings(page);
	const patches = recordRequests(page, 'PATCH', '/api/settings');
	await page.goto('/settings/general');

	// Sandbox limits outside what runs accept, and no image, are refused
	const sandbox = page.getByRole('form', { name: 'Sandbox defaults' });
	await sandbox.getByLabel('Default image').fill('');
	await sandbox.getByLabel('Timeout').fill('10');
	await sandbox.getByLabel('Max turns').fill('0');
	await sandbox.getByLabel('CPUs').fill('100');
	await sandbox.getByLabel('Memory').fill('32');
	await sandbox.getByRole('button', { name: 'Save', exact: true }).click();

	// The edits stay in place to be fixed
	await expect(sandbox.getByLabel('Default image')).toHaveValue('');
	await expect(sandbox.getByLabel('Timeout')).toHaveValue('10');
	await expect(sandbox.getByLabel('Max turns')).toHaveValue('0');
	await expect(sandbox.getByLabel('CPUs')).toHaveValue('100');
	await expect(sandbox.getByLabel('Memory')).toHaveValue('32');

	// Fixed values save in a single request, so the refused ones were never sent
	await sandbox.getByLabel('Default image').fill(before.defaultImage);
	await sandbox.getByLabel('Timeout').fill('600');
	await sandbox.getByLabel('Max turns').fill('5');
	await sandbox.getByLabel('CPUs').fill('2');
	await sandbox.getByLabel('Memory').fill('512');
	await saveForm(sandbox);
	expect(patches).toHaveLength(1);
	expect((await getSettings(page)).defaultLimits).toEqual({
		...before.defaultLimits,
		timeoutSeconds: 600,
		maxTurns: 5,
		cpus: 2,
		memoryMb: 512
	});

	// A retention outside 1 to 3650 days, a negative or a missing spend limit are refused as well
	const budget = page.getByRole('form', { name: 'Spend and retention' });
	const budgetSave = budget.getByRole('button', { name: 'Save', exact: true });
	await budget.getByLabel('Retention').fill('0');
	await budget.getByLabel('Daily spend limit').fill('-1');
	await budgetSave.click();
	await budget.getByLabel('Retention').fill('4000');
	await budget.getByLabel('Daily spend limit').fill('');
	await budgetSave.click();
	await expect(budget.getByLabel('Retention')).toHaveValue('4000');

	// A webhook address that isn't a URL is refused too
	const notifications = page.getByRole('form', { name: 'Notifications' });
	await notifications.getByLabel('Webhook URL').fill('not a url');
	await notifications.getByRole('button', { name: 'Save', exact: true }).click();
	await expect(notifications.getByLabel('Webhook URL')).toHaveValue('not a url');

	// The spend and retention card saves once its values fit, and nothing refused was ever sent
	await budget.getByLabel('Retention').fill('30');
	await budget.getByLabel('Daily spend limit').fill('12.5');
	await saveForm(budget);
	expect(patches).toHaveLength(2);
	expect(await getSettings(page)).toMatchObject({
		dailySpendLimitUsd: 12.5,
		retentionDays: 30,
		notifyWebhookUrl: null
	});

	// The saved values are what the cards show after a reload
	await page.reload();
	await expect(sandbox.getByLabel('Timeout')).toHaveValue('600');
	await expect(sandbox.getByLabel('Max turns')).toHaveValue('5');
	await expect(sandbox.getByLabel('CPUs')).toHaveValue('2');
	await expect(sandbox.getByLabel('Memory')).toHaveValue('512');
	await expect(budget.getByLabel('Retention')).toHaveValue('30');
	await expect(budget.getByLabel('Daily spend limit')).toHaveValue('12.5');
	await expect(notifications.getByLabel('Webhook URL')).toHaveValue('');
});

test('Out-of-range values are explained next to their fields, and the first one gets the focus', async ({
	page
}) => {
	await page.goto('/settings/general');

	// Every refused sandbox field says why, and the first one is focused
	const sandbox = page.getByRole('form', { name: 'Sandbox defaults' });
	const image = sandbox.getByLabel('Default image');
	const timeout = sandbox.getByLabel('Timeout');
	await image.fill('');
	await timeout.fill('10');
	await sandbox.getByLabel('Max turns').fill('0');
	await sandbox.getByLabel('CPUs').fill('100');
	await sandbox.getByLabel('Memory').fill('32');
	await sandbox.getByRole('button', { name: 'Save', exact: true }).click();
	await expect(fieldError(sandbox, 'Default image')).toHaveText('Required');
	await expect(fieldError(sandbox, 'Timeout')).toHaveText('Must be at least 30');
	await expect(fieldError(sandbox, 'Max turns')).toHaveText('Must be at least 1');
	await expect(fieldError(sandbox, 'CPUs')).toHaveText('Must be at most 64');
	await expect(fieldError(sandbox, 'Memory')).toHaveText('Must be at least 64');
	await expect(image).toBeFocused();

	// Editing a field takes its error away while the others stay
	await timeout.fill('600');
	await expect(fieldError(sandbox, 'Timeout')).toHaveCount(0);
	await expect(fieldError(sandbox, 'Default image')).toHaveText('Required');

	// The spend and retention card names its bounds
	const budget = page.getByRole('form', { name: 'Spend and retention' });
	const retention = budget.getByLabel('Retention');
	const spend = budget.getByLabel('Daily spend limit');
	await retention.fill('0');
	await spend.fill('-1');
	await budget.getByRole('button', { name: 'Save', exact: true }).click();
	await expect(fieldError(budget, 'Retention')).toHaveText('Must be at least 1');
	await expect(fieldError(budget, 'Daily spend limit')).toHaveText('Must be at least 0');
	await retention.fill('4000');
	await spend.fill('');
	await budget.getByRole('button', { name: 'Save', exact: true }).click();
	await expect(fieldError(budget, 'Retention')).toHaveText('Must be at most 3650');
	await expect(fieldError(budget, 'Daily spend limit')).toHaveText('Required');

	// The webhook address has to be a URL
	const notifications = page.getByRole('form', { name: 'Notifications' });
	const url = notifications.getByLabel('Webhook URL');
	await url.fill('not a url');
	await notifications.getByRole('button', { name: 'Save', exact: true }).click();
	await expect(fieldError(notifications, 'Webhook URL')).toHaveText('Must be a URL');
});

test("The workspace can't be renamed to an empty name", async ({ page }) => {
	test.skip(!(await authUtil.workspacesEnabled(page)), 'workspaces are turned off');

	// Every rename the page sends
	const renames = recordRequests(page, 'PATCH', '/api/workspace');
	await page.goto('/settings/general');

	// An empty name is kept back in the browser
	const workspace = page.getByRole('form', { name: 'Workspace' });
	const name = workspace.getByLabel('Name', { exact: true });
	await name.fill('');
	await workspace.getByRole('button', { name: 'Save', exact: true }).click();

	// A real name saves, and it is the only rename that was ever sent
	await name.fill('Platform team');
	await saveForm(workspace);
	expect(renames).toEqual([{ name: 'Platform team' }]);
});

test('Members see the workspace settings as plain text, with the webhook URL cut down to its origin and nothing to save', async ({
	page,
	browser
}, testInfo) => {
	const workspacesOn = await authUtil.workspacesEnabled(page);

	// The owner sets every value the member view shows, and posts the notifications to a chat webhook whose path is its credential
	await createSecret(page.request, 'HOOK_SECRET', 's3cret');
	const updated = await page.request.patch('/api/settings', {
		data: {
			defaultLimits: { timeoutSeconds: 600, maxTurns: 5, maxCostUsd: 1.5, cpus: 2, memoryMb: 512 },
			dailySpendLimitUsd: 12.5,
			retentionDays: 30,
			usageUnit: 'tokens',
			notifyWebhookUrl: webhookUrl,
			notifySecret: 'HOOK_SECRET',
			notifyOn: ['run.failed']
		}
	});
	expect(updated.ok()).toBeTruthy();
	const { defaultImage } = await getSettings(page);

	const bobPage = await joinWorkspace(browser, testInfo, page, accounts.bob);
	try {
		// Bob, a member, gets the settings with nothing to change or save
		await bobPage.goto('/settings/general');
		await expect(
			bobPage.getByText('Only admins of the workspace can change its settings.')
		).toBeVisible();
		await expect(
			bobPage.getByRole('heading', { name: 'Sandbox defaults', exact: true })
		).toBeVisible();
		await expect(bobPage.getByRole('form')).toHaveCount(0);
		await expect(bobPage.getByRole('textbox')).toHaveCount(0);
		await expect(bobPage.getByRole('button', { name: 'Save' })).toHaveCount(0);
		await expect(bobPage.getByRole('button', { name: 'Send test' })).toHaveCount(0);

		// Each card lists the saved values as text
		const values = (title: string) => card(bobPage, title).getByRole('definition');
		if (workspacesOn) await expect(values('Workspace')).toHaveText(['Default']);
		await expect(values('Sandbox defaults')).toHaveText([
			defaultImage,
			'600 seconds',
			'5',
			'$1.50',
			'2 cores',
			'512 MB'
		]);
		await expect(values('Spend and retention')).toHaveText(['$12.50 per day', '30 days']);
		await expect(values('Usage')).toHaveText(['Tokens']);

		// The webhook shows only where it points, so members never learn the address that posts into the channel
		await expect(values('Notifications')).toHaveText([
			'https://hooks.slack.com/…',
			'HOOK_SECRET',
			'A run fails or times out'
		]);
		await expect(bobPage.locator('body')).not.toContainText('XXXXSECRET');

		// The API masks the webhook for members too, and won't let them post a test message to it
		expect((await (await bobPage.request.get('/api/settings')).json()).notifyWebhookUrl).toBe(
			'https://hooks.slack.com/…'
		);
		expect((await bobPage.request.post('/api/settings/notifications/test')).status()).toBe(403);
	} finally {
		await bobPage.context().close();
	}

	// The owner still reads and edits the webhook whole
	expect((await getSettings(page)).notifyWebhookUrl).toBe(webhookUrl);
	await page.goto('/settings/general');
	await expect(
		page.getByRole('form', { name: 'Notifications' }).getByLabel('Webhook URL')
	).toHaveValue(webhookUrl);
});
