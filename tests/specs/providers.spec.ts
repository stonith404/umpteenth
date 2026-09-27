import { expect, test, type APIRequestContext, type Locator, type Page } from '@playwright/test';
import authUtil from '../utils/auth.util';
import { cleanupBackend } from '../utils/cleanup.util';
import { fieldError, saveForm } from '../utils/form.util';
import { startFakeOpenAi } from '../utils/openai-fake.util';
import { runHeader } from '../utils/run-view.util';
import runUtil from '../utils/run.util';
import { toast } from '../utils/ui.util';

// Some tests run a job in a real sandbox, so these specs get more time than the suite default
test.describe.configure({ timeout: 60_000 });

// Toasts stack up in the window's bottom right corner, and a tall window keeps them clear of the tables' switches and menus
test.use({ viewport: { width: 1280, height: 1600 } });

test.beforeEach(async ({ page }) => {
	await cleanupBackend(page.request);
	await authUtil.authenticate(page);
});

// The workspace's default models as GET /api/settings returns them, where null means not set
type DefaultModels = {
	agentModelId: string | null;
	utilityModelId: string | null;
	reflectionModelId: string | null;
};

// The API requires every capability of a new model, and these are enough for the fake model to drive an agent
const DEFAULT_CAPS = {
	tools: true,
	parallelTools: true,
	reasoning: false,
	jsonSchema: true,
	promptCache: false,
	vision: false,
	context: 200_000
};

const ZERO_PRICE = { in: 0, out: 0, cacheRead: 0, cacheWrite: 0 };

// The ID of the provider with exactly this name, since a search also matches kinds and base URLs
async function providerId(request: APIRequestContext, name: string) {
	const response = await request.get('/api/providers', { params: { search: name } });
	expect(response.ok()).toBeTruthy();
	const { items } = (await response.json()) as { items: { id: string; name: string }[] };
	const provider = items.find((p) => p.name === name);
	expect(provider, `provider ${name}`).toBeDefined();
	return provider!.id;
}

// The ID of the model with exactly this model ID at its provider, such as fake-model
async function modelId(request: APIRequestContext, model: string) {
	const response = await request.get('/api/models', { params: { search: model } });
	expect(response.ok()).toBeTruthy();
	const { items } = (await response.json()) as { items: { id: string; model: string }[] };
	const found = items.find((m) => m.model === model);
	expect(found, `model ${model}`).toBeDefined();
	return found!.id;
}

// Adds a provider, which lists its models right away, and returns its ID
// The kind fake only exists in e2e builds and answers from the same scripted queue as the seeded Fake provider
async function createProvider(
	request: APIRequestContext,
	provider: { name: string; kind: 'fake' }
) {
	const response = await request.post('/api/providers', { data: provider });
	expect(response.ok()).toBeTruthy();
	return ((await response.json()) as { id: string }).id;
}

// Adds a free model to a provider by hand and returns its ID, turned off when asked
async function createModel(
	request: APIRequestContext,
	model: { providerId: string; model: string; label?: string; enabled?: boolean }
) {
	const { enabled = true, ...fields } = model;
	const response = await request.post('/api/models', {
		data: { ...fields, price: ZERO_PRICE, caps: DEFAULT_CAPS }
	});
	expect(response.ok()).toBeTruthy();
	const id = ((await response.json()) as { id: string }).id;
	if (!enabled) {
		const disabled = await request.patch(`/api/models/${id}`, { data: { enabled: false } });
		expect(disabled.ok()).toBeTruthy();
	}
	return id;
}

// The workspace's default models without its other settings, so a test can compare them whole
async function getDefaultModels(request: APIRequestContext): Promise<DefaultModels> {
	const response = await request.get('/api/settings');
	expect(response.ok()).toBeTruthy();
	const settings = (await response.json()) as DefaultModels;
	return {
		agentModelId: settings.agentModelId,
		utilityModelId: settings.utilityModelId,
		reflectionModelId: settings.reflectionModelId
	};
}

// Points some of the workspace's default models elsewhere, where an empty ID unsets one
async function setDefaultModels(request: APIRequestContext, update: Partial<DefaultModels>) {
	const response = await request.patch('/api/settings', { data: update });
	expect(response.ok()).toBeTruthy();
}

// A row of the providers page's Providers table, matched from the start of its name so "Fake" leaves out other providers of the kind "Fake (tests)"
function providerRow(page: Page, name: string) {
	return page
		.getByRole('table', { name: 'Providers' })
		.getByRole('row', { name: new RegExp(`^${escapeRegExp(name)}`) });
}

// A row of the providers page's Models table, by the label or model ID it leads with
function modelRow(page: Page, name: string) {
	return page
		.getByRole('table', { name: 'Models' })
		.getByRole('row', { name: new RegExp(`^${escapeRegExp(name)}`) });
}

// Opens the '⋯' menu of a row in either table
async function openRowMenu(row: Locator, name: string) {
	await row.getByRole('button', { name: `Actions for ${name}` }).click();
}

// The text as a regular expression that matches it literally
function escapeRegExp(text: string) {
	return text.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

test('Choosing another default agent model moves the next run and the jobs without a model of their own to it', async ({
	page
}) => {
	const fakeProviderId = await providerId(page.request, 'Fake');
	const fakeModelId = await modelId(page.request, 'fake-model');
	const secondId = await createModel(page.request, {
		providerId: fakeProviderId,
		model: 'fake-second',
		label: 'Second fake'
	});

	// Pick the second model as the agent default, leaving the utility model on the seeded one
	await page.goto('/settings/general');
	const defaults = page.getByRole('form', { name: 'Default models' });
	await expect(defaults.getByLabel('Agent')).toContainText('Fake model');
	await defaults.getByLabel('Agent').click();
	await page.getByRole('option', { name: /^Second fake/ }).click();
	await saveForm(defaults);
	expect(await getDefaultModels(page.request)).toMatchObject({
		agentModelId: secondId,
		utilityModelId: fakeModelId
	});

	// A job without a model of its own runs on the new default
	const job = await runUtil.createJob(page.request, 'Model job');
	const { runId, status } = await runUtil.runScripted(page.request, job.id, [
		runUtil.finish('Done')
	]);
	expect(status).toBe('succeeded');
	const run = (await (await page.request.get(`/api/runs/${runId}`)).json()) as {
		modelLabel: string;
		modelName: string;
	};
	expect(run).toMatchObject({ modelLabel: 'Second fake', modelName: 'fake-second' });
	await page.goto(`/runs/${runId}`);
	const header = runHeader(page);
	await expect(header).toContainText('Second fake');

	// The job's model picker names the new default as the one it falls back to
	await page.goto(`/jobs/${job.id}/settings`);
	await expect(page.getByRole('form', { name: 'General' }).getByLabel('Model')).toHaveText(
		'Workspace default (Second fake)'
	);
});

test('Only models still in use as a default are protected from being disabled', async ({
	page
}) => {
	const fakeProviderId = await providerId(page.request, 'Fake');
	const fakeModelId = await modelId(page.request, 'fake-model');
	const secondId = await createModel(page.request, {
		providerId: fakeProviderId,
		model: 'fake-second',
		label: 'Second fake'
	});
	await setDefaultModels(page.request, { agentModelId: secondId });

	// The agent default can't be turned off, and its switch goes back on
	await page.goto('/settings/providers');
	const second = modelRow(page, 'Second fake');
	await second.getByRole('switch', { name: 'Disable Second fake' }).click();
	await expect(
		toast(
			page,
			'Failed to disable the model',
			"Second fake is the workspace's agent model, pick another one in Settings → General first"
		)
	).toBeVisible();
	await expect(second.getByRole('switch', { name: 'Disable Second fake' })).toBeVisible();

	// The seeded model is no longer the agent default, but still the utility one, so it stays protected
	const fake = modelRow(page, 'Fake model');
	await fake.getByRole('switch', { name: 'Disable Fake model' }).click();
	await expect(
		toast(
			page,
			'Failed to disable the model',
			"Fake model is the workspace's utility model, pick another one in Settings → General first"
		)
	).toBeVisible();
	await expect(fake.getByRole('switch', { name: 'Disable Fake model' })).toBeVisible();

	// Letting the utility role follow the agent model frees the seeded model
	await page.goto('/settings/general');
	const defaults = page.getByRole('form', { name: 'Default models' });
	await defaults.getByLabel('Utility').click();
	await page.getByRole('option', { name: 'Same as the agent model' }).click();
	await saveForm(defaults);
	expect(await getDefaultModels(page.request)).toMatchObject({
		agentModelId: secondId,
		utilityModelId: null
	});

	// Now it can be turned off, and the switch stays off after a reload
	await page.goto('/settings/providers');
	await fake.getByRole('switch', { name: 'Disable Fake model' }).click();

	// The switch flips before its request lands and stays disabled until the server answers, and a reload any sooner would cancel the request
	await expect(fake.getByRole('switch', { name: 'Enable Fake model' })).toBeEnabled();
	await page.reload();
	await expect(fake.getByRole('switch', { name: 'Enable Fake model' })).toBeVisible();

	// The disabled model is no longer offered as a default
	await page.goto('/settings/general');
	await defaults.getByLabel('Reflection').click();
	await expect(page.getByRole('option', { name: 'Same as the agent model' })).toBeVisible();
	await expect(page.getByRole('option', { name: /^Second fake/ })).toBeVisible();
	await expect(page.getByRole('option', { name: /^Fake model/ })).toHaveCount(0);
	await page.keyboard.press('Escape');

	// The API refuses it as a default too
	const refused = await page.request.patch('/api/settings', {
		data: { reflectionModelId: fakeModelId }
	});
	expect(refused.status()).toBe(400);
	expect((await refused.json()).fields).toEqual([
		expect.objectContaining({
			field: 'reflectionModelId',
			message: 'is not an enabled model of this workspace'
		})
	]);
});

test('Deleting the provider behind the default and a pinned model unsets both, and runs say no model is configured until a new default is picked', async ({
	page
}) => {
	// A second provider's model drives the agent by default and is pinned on a job
	const spareProviderId = await createProvider(page.request, {
		name: 'Spare',
		kind: 'fake'
	});
	const spareModelId = await createModel(page.request, {
		providerId: spareProviderId,
		model: 'spare-model',
		label: 'Spare model'
	});
	const fakeModelId = await modelId(page.request, 'fake-model');
	await setDefaultModels(page.request, { agentModelId: spareModelId });
	const job = await runUtil.createJob(page.request, 'Pinned job', { modelId: spareModelId });

	await page.goto(`/jobs/${job.id}/settings`);
	const jobModel = page.getByRole('form', { name: 'General' }).getByLabel('Model');
	await expect(jobModel).toHaveText('Spare model · Spare');

	// Delete the provider, whose confirmation says what happens to its models
	await page.goto('/settings/providers');
	const spare = providerRow(page, 'Spare');
	await openRowMenu(spare, 'Spare');
	await page.getByRole('menuitem', { name: 'Delete' }).click();
	const confirm = page.getByRole('alertdialog', { name: 'Delete Spare' });
	await expect(confirm).toContainText('Its 1 model is removed too');
	await confirm.getByRole('button', { name: 'Delete' }).click();
	await expect(toast(page, 'Deleted "Spare"')).toBeVisible();
	await expect(spare).toHaveCount(0);
	await expect(modelRow(page, 'Spare model')).toHaveCount(0);

	// Neither the default nor the job points at the deleted model any more, while the other defaults stay
	expect(await getDefaultModels(page.request)).toMatchObject({
		agentModelId: null,
		utilityModelId: fakeModelId
	});
	const updatedJob = (await (await page.request.get(`/api/jobs/${job.id}`)).json()) as {
		modelId: string | null;
	};
	expect(updatedJob.modelId).toBeNull();

	// The pickers show both as unset instead of a model that is gone
	await page.goto('/settings/general');
	const defaults = page.getByRole('form', { name: 'Default models' });
	await expect(defaults.getByLabel('Agent')).toHaveText('Not set');
	await expect(defaults.getByLabel('Utility')).toHaveText('Fake model · Fake');
	await page.goto(`/jobs/${job.id}/settings`);
	await expect(jobModel).toHaveText('Workspace default');

	// A run fails before it starts a sandbox and says what to do
	const noModel = await runUtil.runScripted(page.request, job.id, []);
	expect(noModel.status).toBe('failed');
	await page.goto(`/runs/${noModel.runId}`);
	const header = runHeader(page);
	await expect(header.locator('[data-slot="alert"]')).toContainText(
		'No model is configured. Pick a model for the job or set a default agent model in Settings.'
	);

	// Picking a new default gets the job running again
	await page.goto('/settings/general');
	await defaults.getByLabel('Agent').click();
	await page.getByRole('option', { name: /^Fake model/ }).click();
	await saveForm(defaults);
	const { runId, status } = await runUtil.runScripted(page.request, job.id, [
		runUtil.finish('Back on the fake')
	]);
	expect(status).toBe('succeeded');
	const run = (await (await page.request.get(`/api/runs/${runId}`)).json()) as {
		modelLabel: string;
	};
	expect(run.modelLabel).toBe('Fake model');
});

test("Testing a provider sends a prompt through the chosen model and shows the reply, the provider's error or why it can't test", async ({
	page
}) => {
	const fakeProviderId = await providerId(page.request, 'Fake');
	const fakeModelId = await modelId(page.request, 'fake-model');

	// The disabled model's ID sorts before fake-model, so it would be the dialog's first model if it didn't prefer an enabled one
	const offModelId = await createModel(page.request, {
		providerId: fakeProviderId,
		model: 'fake-a-off',
		label: 'Off model',
		enabled: false
	});
	const emptyProviderId = await createProvider(page.request, {
		name: 'Empty',
		kind: 'fake'
	});

	// The dialog starts on the provider's enabled model
	await page.goto('/settings/providers');
	await providerRow(page, 'Fake').getByRole('button', { name: 'Test' }).click();
	const dialog = page.getByRole('dialog', { name: 'Test Fake' });
	const model = dialog.getByLabel('Model');
	await expect(model).toHaveText('Fake model · Fake');
	const send = dialog.getByRole('button', { name: 'Send test prompt' });

	// The model's reply shows with how long it took
	await runUtil.scriptModel(page.request, [{ text: 'OK' }]);
	await send.click();
	await expect(dialog.getByText(/^Replied in /)).toBeVisible();
	await expect(dialog.getByText('OK', { exact: true })).toBeVisible();

	// A provider error shows as the failure instead
	await runUtil.scriptModel(page.request, [{ error: 'invalid x-api-key', errorStatus: 401 }]);
	await send.click();
	await expect(dialog.getByText(/^Failed after /)).toBeVisible();
	await expect(dialog.getByText('fake API error (status 401): invalid x-api-key')).toBeVisible();

	// A reply without text still counts as working
	await runUtil.scriptModel(page.request, []);
	await send.click();
	await expect(dialog.getByText('(empty reply)')).toBeVisible();

	// A disabled model can be tested before it is turned on, and the test goes through that model
	await model.click();
	await page.getByRole('option', { name: /^Off model/ }).click();
	await expect(model).toHaveText('Off model · Fake · disabled');
	await runUtil.scriptModel(page.request, [{ text: 'Still answers' }]);
	const [testRequest] = await Promise.all([
		page.waitForRequest((r) => r.method() === 'POST' && r.url().endsWith('/test')),
		send.click()
	]);
	expect(testRequest.postDataJSON()).toEqual({ modelId: offModelId });
	await expect(dialog.getByText('Still answers')).toBeVisible();
	await page.keyboard.press('Escape');
	await expect(dialog).toBeHidden();

	// A provider without models has nothing to test with
	await providerRow(page, 'Empty').getByRole('button', { name: 'Test' }).click();
	const emptyDialog = page.getByRole('dialog', { name: 'Test Empty' });
	await expect(
		emptyDialog.getByText('The provider has no models yet. Add one from its menu to test it.')
	).toBeVisible();
	await expect(emptyDialog.getByRole('button', { name: 'Send test prompt' })).toBeDisabled();

	// The API only tests a provider through one of its own models
	const foreign = await page.request.post(`/api/providers/${emptyProviderId}/test`, {
		data: { modelId: fakeModelId }
	});
	expect(foreign.status()).toBe(400);
	expect((await foreign.json()).fields).toEqual([
		expect.objectContaining({ field: 'modelId', message: 'does not belong to this provider' })
	]);
});

test('The provider dialog rejects a missing name, a non-web base URL, a missing OpenAI key and a taken name inline', async ({
	page
}) => {
	await page.goto('/settings/providers');

	// A provider needs a name, and a base URL has to be a web address
	await page.getByRole('button', { name: 'Add provider' }).click();
	const dialog = page.getByRole('dialog', { name: 'Add provider' });
	const name = dialog.getByLabel('Name');
	const baseUrl = dialog.getByLabel('Base URL');
	const submit = dialog.getByRole('button', { name: 'Add provider' });
	await name.fill('');
	await submit.click();
	await expect(fieldError(dialog, 'Name')).toHaveText('Required');
	await name.fill('Anthropic');
	await baseUrl.fill('ftp://example.com');
	await submit.click();
	await expect(fieldError(dialog, 'Base URL')).toHaveText('Must start with http:// or https://');
	await expect(fieldError(dialog, 'Name')).toHaveCount(0);

	// The OpenAI API itself needs a key, and switching the kind renames the untouched default name
	await dialog.getByRole('tab', { name: 'OpenAI-compatible' }).click();
	await expect(name).toHaveValue('OpenAI');
	await expect(baseUrl).toHaveValue('');
	await submit.click();
	await expect(fieldError(dialog, 'API key')).toHaveText('Required for the OpenAI API');

	// A name another provider has is refused by the server and shown under the name
	await dialog.getByRole('tab', { name: 'Anthropic' }).click();
	await name.fill('Fake');
	await submit.click();
	await expect(fieldError(dialog, 'Name')).toHaveText('Provider name is already in use');
	await expect(toast(page, 'Failed to save the provider')).toBeVisible();
	await expect(dialog).toBeVisible();
	await name.fill('Anthropic');
	await submit.click();
	await expect(toast(page, 'Added "Anthropic"')).toBeVisible();
	await expect(dialog).toBeHidden();

	// Renaming into a taken name is refused the same way
	await openRowMenu(providerRow(page, 'Anthropic'), 'Anthropic');
	await page.getByRole('menuitem', { name: 'Edit' }).click();
	const editDialog = page.getByRole('dialog', { name: 'Edit Anthropic' });
	await editDialog.getByLabel('Name').fill('Fake');
	await editDialog.getByRole('button', { name: 'Save' }).click();
	await expect(fieldError(editDialog, 'Name')).toHaveText('Provider name is already in use');
	await editDialog.getByRole('button', { name: 'Cancel' }).click();
	await expect(editDialog).toBeHidden();
	await expect(providerRow(page, 'Anthropic')).toBeVisible();
});

test('The model dialog rejects a missing ID and context window, and a model ID the provider already has, inline', async ({
	page
}) => {
	// A model needs an ID and a context window, and the dialog starts on the only provider, Fake
	await page.goto('/settings/providers');
	await page.getByRole('button', { name: 'Add model' }).click();
	const modelDialog = page.getByRole('dialog', { name: 'Add model' });
	const submit = modelDialog.getByRole('button', { name: 'Add model' });
	const context = modelDialog.getByLabel('Context window');
	await context.fill('');
	await submit.click();
	await expect(fieldError(modelDialog, 'Model ID')).toHaveText('Required');
	await expect(fieldError(modelDialog, 'Context window')).toHaveText('Must be at least 1');

	// A model ID the provider already has is refused by the server
	await context.fill('1000');
	await modelDialog.getByLabel('Model ID').fill('fake-model');
	await submit.click();
	await expect(fieldError(modelDialog, 'Model ID')).toHaveText('Model is already in use');
	await expect(fieldError(modelDialog, 'Context window')).toHaveCount(0);
	await expect(toast(page, 'Failed to save the model')).toBeVisible();
	await expect(modelDialog).toBeVisible();
});

test('The provider and model dialogs explain a base URL without a scheme, a zero context window and a negative price under their fields', async ({
	page
}) => {
	await page.goto('/settings/providers');

	// An address such as a local server's, which the browser doesn't take as a URL
	await page.getByRole('button', { name: 'Add provider' }).click();
	const dialog = page.getByRole('dialog', { name: 'Add provider' });
	await dialog.getByLabel('Base URL').fill('192.168.1.5:11434');
	await dialog.getByRole('button', { name: 'Add provider' }).click();
	await expect(fieldError(dialog, 'Base URL')).toHaveText('Must start with http:// or https://');
	await expect(dialog).toBeVisible();
	await dialog.getByRole('button', { name: 'Cancel' }).click();

	// A model whose context window and input price are out of range
	await page.getByRole('button', { name: 'Add model' }).click();
	const modelDialog = page.getByRole('dialog', { name: 'Add model' });
	await modelDialog.getByLabel('Model ID').fill('tiny-model');
	await modelDialog.getByLabel('Context window').fill('0');
	await modelDialog.getByRole('spinbutton', { name: 'Input' }).fill('-1');
	await modelDialog.getByRole('button', { name: 'Add model' }).click();
	await expect(fieldError(modelDialog, 'Context window')).toHaveText('Must be at least 1');
	await expect(fieldError(modelDialog, 'Input')).toHaveText('Must be 0 or more');
	await expect(modelDialog).toBeVisible();
});

test('Once the last provider is deleted, the page offers only to add one', async ({ page }) => {
	await page.goto('/settings/providers');
	const fake = providerRow(page, 'Fake');
	await openRowMenu(fake, 'Fake');
	await page.getByRole('menuitem', { name: 'Delete' }).click();
	await page.getByRole('alertdialog').getByRole('button', { name: 'Delete' }).click();
	await expect(fake).toHaveCount(0);

	// Empty panels replace both tables, and the models panel says a provider comes first instead of offering to add a model
	await expect(page.getByRole('table')).toHaveCount(0);
	await expect(page.getByText('No providers')).toBeVisible();
	await expect(page.getByRole('button', { name: 'Add provider' })).toBeVisible();
	await expect(page.getByText('No models')).toBeVisible();
	await expect(page.getByText(/Add a provider first/)).toBeVisible();
	await expect(page.getByRole('button', { name: 'Add model' })).toHaveCount(0);
});

test('An OpenAI-compatible server lists its models into the workspace, a wrong base URL shows Sync failed, and only models it stops listing can be deleted', async ({
	page
}) => {
	const server = await startFakeOpenAi(['e2e-chat-8b', 'e2e-coder-7b']);
	try {
		await page.goto('/settings/providers');

		// Add the server without the /v1 its model list lives under
		await page.getByRole('button', { name: 'Add provider' }).click();
		const dialog = page.getByRole('dialog', { name: 'Add provider' });
		await dialog.getByRole('tab', { name: 'OpenAI-compatible' }).click();
		await dialog.getByLabel('Name').fill('Local LLM');
		await dialog.getByLabel('Base URL').fill(server.url);
		await dialog.getByRole('button', { name: 'Add provider' }).click();

		// The provider is saved, with a warning that says where the model list is expected
		await expect(toast(page, 'Added "Local LLM"')).toBeVisible();
		await expect(
			toast(
				page,
				'Failed to read the models of "Local LLM"',
				"the server's model list is not JSON, check that the base URL ends where /models is served, such as /v1"
			)
		).toBeVisible();
		const local = providerRow(page, 'Local LLM');
		await expect(local).toContainText('OpenAI-compatible');
		await expect(local).toContainText('0 of 0 enabled');
		await expect(local).toContainText('Sync failed');

		// Fixing the base URL syncs the list right away
		await openRowMenu(local, 'Local LLM');
		await page.getByRole('menuitem', { name: 'Edit' }).click();
		const editDialog = page.getByRole('dialog', { name: 'Edit Local LLM' });
		await editDialog.getByLabel('Base URL').fill(`${server.url}/v1`);
		await editDialog.getByRole('button', { name: 'Save' }).click();
		await expect(toast(page, 'Saved "Local LLM"')).toBeVisible();
		await expect(local).toContainText('2 of 2 enabled');
		await expect(local).toContainText(/Server list · synced/);
		await expect(local).not.toContainText('Sync failed');
		await expect(modelRow(page, 'e2e-chat-8b')).toBeVisible();
		await expect(modelRow(page, 'e2e-coder-7b')).toBeVisible();

		// A model the server stops listing is kept but marked, and a new one is added
		server.setModels(['e2e-chat-8b', 'e2e-small-3b']);
		await openRowMenu(local, 'Local LLM');
		await page.getByRole('menuitem', { name: 'Sync models' }).click();
		await expect(toast(page, 'Synced "Local LLM"', '1 added, 1 no longer listed')).toBeVisible();
		const coder = modelRow(page, 'e2e-coder-7b');
		await expect(coder).toContainText('Not listed');
		await expect(modelRow(page, 'e2e-small-3b')).toBeVisible();
		await expect(local).toContainText('2 of 3 enabled');
		const unlisted = (await (
			await page.request.get('/api/models', { params: { status: 'unlisted' } })
		).json()) as { total: number; items: { model: string }[] };
		expect(unlisted.total).toBe(1);
		expect(unlisted.items[0].model).toBe('e2e-coder-7b');

		// Only a model the server no longer lists can be deleted, since a sync would add a listed one back
		await openRowMenu(modelRow(page, 'e2e-chat-8b'), 'e2e-chat-8b');
		await expect(page.getByRole('menuitem', { name: 'Edit' })).toBeVisible();
		await expect(page.getByRole('menuitem', { name: 'Delete' })).toHaveCount(0);
		await page.keyboard.press('Escape');
		await openRowMenu(coder, 'e2e-coder-7b');
		await page.getByRole('menuitem', { name: 'Delete' }).click();
		await page.getByRole('alertdialog').getByRole('button', { name: 'Delete' }).click();
		await expect(toast(page, 'Deleted "e2e-coder-7b"')).toBeVisible();
		await expect(coder).toHaveCount(0);
		await expect(local).toContainText('2 of 2 enabled');

		// Syncing an unchanged list changes nothing
		await openRowMenu(local, 'Local LLM');
		await page.getByRole('menuitem', { name: 'Sync models' }).click();
		await expect(toast(page, 'The models of "Local LLM" are up to date')).toBeVisible();
	} finally {
		await server.close();
	}
});

test('Every model of a provider can be turned off and back on at once, and a provider without a model list has nothing to sync', async ({
	page
}) => {
	// A provider whose models are added by hand, none of which is a default
	const spareProviderId = await createProvider(page.request, {
		name: 'Spare',
		kind: 'fake'
	});
	for (const model of ['spare-one', 'spare-two']) {
		await createModel(page.request, { providerId: spareProviderId, model });
	}
	await page.goto('/settings/providers');
	const spare = providerRow(page, 'Spare');
	await expect(spare).toContainText('2 of 2 enabled');

	// Turning them all off leaves the menu offering only to turn them back on, and never a sync
	const disableAll = page.getByRole('menuitem', { name: 'Disable all models' });
	const enableAll = page.getByRole('menuitem', { name: 'Enable all models' });
	await openRowMenu(spare, 'Spare');
	await expect(disableAll).toBeVisible();
	await expect(enableAll).toHaveCount(0);
	await expect(page.getByRole('menuitem', { name: 'Sync models' })).toHaveCount(0);
	await disableAll.click();
	await expect(toast(page, 'Disabled every model of "Spare"')).toBeVisible();
	await expect(spare).toContainText('0 of 2 enabled');
	await openRowMenu(spare, 'Spare');
	await expect(enableAll).toBeVisible();
	await expect(disableAll).toHaveCount(0);
	await enableAll.click();
	await expect(toast(page, 'Enabled every model of "Spare"')).toBeVisible();
	await expect(spare).toContainText('2 of 2 enabled');
});
