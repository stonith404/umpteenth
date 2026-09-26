import { expect, test, type Page } from '@playwright/test';
import authUtil from '../utils/auth.util';
import { cleanupBackend } from '../utils/cleanup.util';
import { saveForm } from '../utils/form.util';
import runUtil from '../utils/run.util';

test.beforeEach(async ({ page }) => {
	await cleanupBackend(page.request);
	await authUtil.authenticate(page);
});

test('Create, update and delete a secret without ever showing its value', async ({ page }) => {
	await page.goto('/settings');
	await page.getByRole('tab', { name: 'Secrets' }).click();
	await expect(page).toHaveURL('/settings/secrets');
	await expect(page.getByText('No secrets')).toBeVisible();

	// Create a secret
	await page.getByRole('button', { name: 'Create secret' }).click();
	const createDialog = page.getByRole('dialog', { name: 'Create secret' });
	await createDialog.getByLabel('Name').fill('GITHUB_TOKEN');
	await createDialog.getByLabel('Value').fill('ghp_first_value');
	await createDialog.getByRole('button', { name: 'Create secret' }).click();

	const table = page.getByRole('table', { name: 'Secrets' });
	await expect(table.getByRole('row', { name: /GITHUB_TOKEN/ })).toBeVisible();
	await expect(page.getByText('ghp_first_value')).toHaveCount(0);

	// Replace its value, the dialog starts empty because values are write-only
	await table.getByRole('button', { name: 'Actions for GITHUB_TOKEN' }).click();
	await page.getByRole('menuitem', { name: 'Update value' }).click();
	const updateDialog = page.getByRole('dialog', { name: 'Update GITHUB_TOKEN' });
	await expect(updateDialog.getByLabel('Value')).toHaveValue('');
	await updateDialog.getByLabel('Value').fill('ghp_second_value');
	await updateDialog.getByRole('button', { name: 'Update value' }).click();
	await expect(page.getByText('Updated "GITHUB_TOKEN"')).toBeVisible();

	const listed = await (await page.request.get('/api/secrets')).text();
	expect(listed).not.toContain('ghp_');

	// Delete it through the confirm dialog
	await table.getByRole('button', { name: 'Actions for GITHUB_TOKEN' }).click();
	await page.getByRole('menuitem', { name: 'Delete' }).click();
	await page.getByRole('alertdialog').getByRole('button', { name: 'Delete' }).click();
	await expect(table.getByRole('row', { name: /GITHUB_TOKEN/ })).toBeHidden();
	await expect(page.getByText('No secrets')).toBeVisible();
});

test('Add and edit a provider, then add a model with prices', async ({ page }) => {
	await page.goto('/settings/providers');
	const providers = page.getByRole('table', { name: 'Providers' });
	await expect(providers.getByRole('row', { name: /Fake/ })).toBeVisible();

	// Add an Anthropic provider, whose models come from the catalog
	await page.getByRole('button', { name: 'Add provider' }).click();
	const addDialog = page.getByRole('dialog', { name: 'Add provider' });
	await expect(addDialog.getByLabel('Name')).toHaveValue('Anthropic');
	await addDialog.getByLabel('API key').fill('sk-ant-test-key');
	await expect(addDialog.getByText(/come from the models\.dev catalog/)).toBeVisible();
	await addDialog.getByRole('button', { name: 'Add provider' }).click();

	const anthropic = providers.getByRole('row', { name: /Anthropic/ });
	await expect(anthropic).toContainText('Set');
	await expect(anthropic).toContainText(/[1-9]\d* of [1-9]\d* enabled/);
	await expect(anthropic).toContainText('Catalog');
	const models = page.getByRole('table', { name: 'Models' });
	await expect(models.getByRole('row', { name: /Anthropic/ }).first()).toBeVisible();

	// Rename it and remove its API key
	await anthropic.getByRole('button', { name: 'Actions for Anthropic' }).click();
	await page.getByRole('menuitem', { name: 'Edit' }).click();
	const editDialog = page.getByRole('dialog', { name: 'Edit Anthropic' });
	await editDialog.getByLabel('Name').fill('Anthropic EU');
	await editDialog.getByLabel('Remove the stored API key').check();
	await editDialog.getByRole('button', { name: 'Save' }).click();

	const renamed = providers.getByRole('row', { name: /Anthropic EU/ });
	await expect(renamed).toBeVisible();
	await expect(renamed).toContainText('None');

	// Add a custom model with prices entered in dollars per 1M tokens
	await renamed.getByRole('button', { name: 'Actions for Anthropic EU' }).click();
	await page.getByRole('menuitem', { name: 'Add model' }).click();
	const modelDialog = page.getByRole('dialog', { name: 'Add model' });
	await modelDialog.getByLabel('Model ID').fill('custom-model');
	await modelDialog.getByLabel('Label').fill('Custom model');
	await modelDialog.getByRole('spinbutton', { name: 'Input' }).fill('1.5');
	await modelDialog.getByRole('spinbutton', { name: 'Output' }).fill('6');
	await modelDialog.getByRole('button', { name: 'Add model' }).click();

	const custom = models.getByRole('row', { name: /Custom model/ });
	await expect(custom).toContainText('$1.5');
	await expect(custom).toContainText('$6');

	const listed = await (await page.request.get('/api/models?search=custom-model')).json();
	expect(listed.items[0].price).toMatchObject({ in: 1_500_000, out: 6_000_000 });
	expect(listed.items[0].providerName).toBe('Anthropic EU');
});

test('Disable a model so it can no longer be picked, but not the default one', async ({ page }) => {
	await page.goto('/settings/providers');
	const providers = page.getByRole('table', { name: 'Providers' });
	const models = page.getByRole('table', { name: 'Models' });

	// A new Anthropic provider lists the catalog models, enabled
	await page.getByRole('button', { name: 'Add provider' }).click();
	const addDialog = page.getByRole('dialog', { name: 'Add provider' });
	await addDialog.getByRole('button', { name: 'Add provider' }).click();
	await expect(providers.getByRole('row', { name: /Anthropic/ })).toBeVisible();

	// Turning a model off keeps it in the list but takes it out of the pickers
	const haiku = models.getByRole('row', { name: /Claude Haiku 4\.5/ }).first();
	await haiku.getByRole('switch', { name: 'Disable Claude Haiku 4.5' }).click();
	await expect(haiku.getByRole('switch', { name: 'Enable Claude Haiku 4.5' })).toBeVisible();
	const enabled = await (
		await page.request.get('/api/models?status=enabled&search=claude-haiku-4-5')
	).json();
	expect(enabled.total).toBe(0);
	const disabled = await (
		await page.request.get('/api/models?status=disabled&search=claude-haiku-4-5')
	).json();
	expect(disabled.items[0].enabled).toBe(false);

	// A synced model can only be turned off, since the next sync would add a deleted one back
	await haiku.getByRole('button', { name: 'Actions for Claude Haiku 4.5' }).click();
	await expect(page.getByRole('menuitem', { name: 'Edit' })).toBeVisible();
	await expect(page.getByRole('menuitem', { name: 'Delete' })).toHaveCount(0);
	await page.keyboard.press('Escape');

	// The workspace's default model can't be turned off, and its switch stays on
	const fake = models.getByRole('row', { name: /Fake model/ });
	await fake.getByRole('switch', { name: 'Disable Fake model' }).click();
	await expect(page.getByText(/is the workspace's (agent|utility) model/)).toBeVisible();
	await expect(fake.getByRole('switch', { name: 'Disable Fake model' })).toBeVisible();
});

// Picks how usage shows in the workspace's Usage card and saves it
async function setUsageUnit(page: Page, unit: 'Price' | 'Tokens') {
	await page.goto('/settings/general');
	const card = page.getByRole('form', { name: 'Usage' });
	await card.getByLabel('Show usage as').click();
	await page.getByRole('option', { name: unit }).click();
	await saveForm(card);
}

test('Show usage in tokens instead of dollars, then switch back', async ({ page }) => {
	// The run uses a real sandbox, so this test gets more time than the suite default
	test.setTimeout(60_000);

	// Two scripted turns of 1.5k tokens each, on the fake model, which has no price
	const job = await runUtil.createJob(page.request, 'Usage job');
	const { runId } = await runUtil.runScripted(
		page.request,
		job.id,
		runUtil.bashThenFinish('echo ok', { status: 'success', summary: 'Fine' })
	);

	// Switch to tokens, and the app follows without a reload, since the unit comes with the session
	await setUsageUnit(page, 'Tokens');
	expect((await (await page.request.get('/api/settings')).json()).usageUnit).toBe('tokens');
	await page.getByRole('link', { name: 'Runs', exact: true }).click();
	const table = page.getByRole('table', { name: 'Runs' });
	await expect(table.getByRole('columnheader', { name: 'Tokens' })).toBeVisible();
	await expect(table.getByRole('columnheader', { name: 'Cost' })).toHaveCount(0);
	const row = table.getByRole('row', { name: /Usage job/ });
	await expect(row).toContainText('3k');
	await expect(table).not.toContainText('$');

	// The run's page shows one usage figure in tokens, and so does every turn
	await row.getByRole('link', { name: /Usage job/ }).click();
	await expect(page).toHaveURL(`/runs/${runId}`);
	const details = page.getByLabel('Run details');
	await expect(details).toContainText('Tokens');
	await expect(details).toContainText('3k');
	await expect(details).not.toContainText('Cost');
	await expect(details).not.toContainText('$');
	await expect(page.getByTestId('run-timeline')).toContainText('1.5k tokens');

	// The dashboard's spend becomes a token count too
	await page.goto('/');
	await expect(page.getByTestId('kpi-tokens')).toContainText('3k');
	await expect(page.getByTestId('kpi-spend')).toHaveCount(0);

	// Back to prices, where the same run costs nothing on the unpriced fake model and shows no tokens
	await setUsageUnit(page, 'Price');
	await page.goto(`/runs/${runId}`);
	await expect(details).toContainText('Cost');
	await expect(details).toContainText('$0.00');
	await expect(details).not.toContainText('Tokens');
	await expect(page.getByTestId('run-timeline')).not.toContainText('tokens');
});
