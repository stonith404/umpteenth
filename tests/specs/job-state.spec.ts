import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import authUtil from '../utils/auth.util';
import { cleanupBackend } from '../utils/cleanup.util';
import { replaceCode } from '../utils/form.util';
import {
	dockerfile,
	getPlaybook,
	learning,
	listImages,
	putPlaybook,
	waitForBuilds
} from '../utils/playbook.util';
import runUtil from '../utils/run.util';

// Sandbox runs and an image build take longer than the suite default
test.describe.configure({ timeout: 60_000 });

test.beforeEach(async ({ page }) => {
	await cleanupBackend(page.request);
	await authUtil.authenticate(page);
});

// One key of a job's state, as GET /api/jobs/{id}/state lists it
type StateEntry = { key: string; value: string; updatedAt: number };

// The job's whole state, sorted by key
async function listState(request: APIRequestContext, jobId: string) {
	const response = await request.get(`/api/jobs/${jobId}/state`);
	expect(response.ok()).toBeTruthy();
	return ((await response.json()) as { items: StateEntry[] }).items;
}

// The value of one key, or null when the job doesn't have it
async function getState(request: APIRequestContext, jobId: string, key: string) {
	const response = await request.get(`/api/jobs/${jobId}/state/${encodeURIComponent(key)}`);
	if (response.status() === 404) return null;
	expect(response.ok()).toBeTruthy();
	return ((await response.json()) as StateEntry).value;
}

// Stores the value of one key as a run or a script would
async function putState(request: APIRequestContext, jobId: string, key: string, value: string) {
	const response = await request.put(`/api/jobs/${jobId}/state/${encodeURIComponent(key)}`, {
		data: { value }
	});
	expect(response.ok()).toBeTruthy();
}

// One row of the State tab, found by its exact key so a key that contains another one never matches it
function stateRow(page: Page, key: string) {
	return page
		.getByRole('table', { name: 'State' })
		.getByRole('row')
		.filter({ has: page.getByRole('cell', { name: key, exact: true }) });
}

// The value cell of one row of the State tab, which shows the value itself or says that it is empty
function valueCell(page: Page, key: string) {
	return stateRow(page, key).getByRole('cell').nth(1);
}

// Adds an entry through the Add entry dialog and waits for the toast that confirms it
async function addEntry(page: Page, key: string, value: string) {
	await page.getByRole('button', { name: 'Add entry' }).click();
	const dialog = page.getByRole('dialog', { name: 'Add state entry' });
	await dialog.getByLabel('Key').fill(key);
	await dialog.getByLabel('Value').fill(value);
	await dialog.getByRole('button', { name: 'Save' }).click();
	await expect(page.getByText(`Saved "${key.trim()}"`).first()).toBeVisible();
	await expect(dialog).toBeHidden();
}

// Changes a plain value through the edit dialog its row opens and waits for the toast that confirms it
async function editEntry(page: Page, key: string, value: string) {
	await stateRow(page, key).getByRole('cell', { name: key, exact: true }).click();
	const dialog = page.getByRole('dialog', { name: `Edit ${key}` });
	await dialog.getByRole('textbox', { name: 'Value' }).fill(value);
	await dialog.getByRole('button', { name: 'Save' }).click();
	await expect(page.getByText(`Saved "${key}"`).first()).toBeVisible();
	await expect(dialog).toBeHidden();
}

// Deletes an entry through its row menu and the confirmation, and waits for the toast that confirms it
async function deleteEntry(page: Page, key: string) {
	await page.getByRole('button', { name: `Actions for ${key}` }).click();
	await page.getByRole('menuitem', { name: 'Delete' }).click();
	await page
		.getByRole('alertdialog', { name: `Delete ${key}` })
		.getByRole('button', { name: 'Delete' })
		.click();
	await expect(page.getByText(`Deleted "${key}"`).first()).toBeVisible();
}

test('Adding a state entry by hand needs a key that is new, trims it, and stores empty and JSON values as typed', async ({
	page
}) => {
	const job = await runUtil.createJob(page.request, 'State job');
	await page.goto(`/jobs/${job.id}`);
	await page.getByRole('tab', { name: 'State' }).click();
	await expect(page).toHaveURL(`/jobs/${job.id}/state`);

	// A job without state says so and has nothing to search yet
	await expect(page.getByText('No state yet')).toBeVisible();
	await expect(page.getByRole('searchbox', { name: 'Search state' })).toHaveCount(0);

	// A key made of spaces counts as missing and saves nothing
	// The dialog was just opened, so no alert from an earlier attempt can stand in for this one
	await page.getByRole('button', { name: 'Add entry' }).click();
	const add = page.getByRole('dialog', { name: 'Add state entry' });
	await add.getByLabel('Key').fill('   ');
	await add.getByRole('button', { name: 'Save' }).click();
	await expect(add.getByRole('alert')).toHaveText('Required');
	expect(await listState(page.request, job.id)).toEqual([]);

	// The key is stored without the spaces around it
	await add.getByLabel('Key').fill('  last_id  ');
	await add.getByLabel('Value').fill('41');
	await add.getByRole('button', { name: 'Save' }).click();
	await expect(page.getByText('Saved "last_id"').first()).toBeVisible();
	await expect(add).toBeHidden();
	await expect(valueCell(page, 'last_id')).toHaveText('41');
	expect(await getState(page.request, job.id, 'last_id')).toBe('41');

	// Saving is an upsert, so adding a key that exists is refused instead of overwriting it
	await page.getByRole('button', { name: 'Add entry' }).click();
	await add.getByLabel('Key').fill('last_id');
	await add.getByLabel('Value').fill('99');
	await add.getByRole('button', { name: 'Save' }).click();
	await expect(add.getByRole('alert')).toHaveText('This key already exists');
	await add.getByRole('button', { name: 'Cancel' }).click();
	await expect(add).toBeHidden();
	await expect(valueCell(page, 'last_id')).toHaveText('41');
	expect(await getState(page.request, job.id, 'last_id')).toBe('41');

	// An empty value is allowed and says so, and a JSON value added by hand is stored as typed
	await addEntry(page, 'empty_note', '');
	await expect(valueCell(page, 'empty_note')).toHaveText('Empty');
	const cursor = '{"page":2,"seen":[101,102]}';
	await addEntry(page, 'cursor', cursor);
	await expect(valueCell(page, 'cursor')).toHaveText(cursor);
	expect((await listState(page.request, job.id)).map((e) => [e.key, e.value])).toEqual([
		['cursor', cursor],
		['empty_note', ''],
		['last_id', '41']
	]);
});

test('State entries open in the editor their value suits, are changed, searched by value and deleted after a confirmation', async ({
	page
}) => {
	const job = await runUtil.createJob(page.request, 'State job');
	await putState(page.request, job.id, 'cursor', '{"page":2,"seen":[101,102]}');
	await putState(page.request, job.id, 'last_id', '41');
	await page.goto(`/jobs/${job.id}/state`);

	// A JSON value opens in the code editor, and an existing key can't be renamed
	await stateRow(page, 'cursor').getByRole('cell', { name: 'cursor', exact: true }).click();
	const editCursor = page.getByRole('dialog', { name: 'Edit cursor' });
	await expect(editCursor.locator('[data-slot="code-editor"]')).toBeVisible();
	await expect(editCursor.getByRole('textbox', { name: 'Value' })).toContainText('"page":2');
	await expect(editCursor.getByLabel('Key')).toHaveCount(0);
	await editCursor.getByRole('button', { name: 'Cancel' }).click();
	await expect(editCursor).toBeHidden();

	// A plain value opens in a text field, which toHaveValue needs, and is replaced on save
	await stateRow(page, 'last_id').getByRole('cell', { name: 'last_id', exact: true }).click();
	const editLast = page.getByRole('dialog', { name: 'Edit last_id' });
	await expect(editLast.getByRole('textbox', { name: 'Value' })).toHaveValue('41');
	await editLast.getByRole('textbox', { name: 'Value' }).fill('42');
	await editLast.getByRole('button', { name: 'Save' }).click();
	await expect(page.getByText('Saved "last_id"').first()).toBeVisible();
	await expect(editLast).toBeHidden();
	await expect(valueCell(page, 'last_id')).toHaveText('42');
	expect(await getState(page.request, job.id, 'last_id')).toBe('42');

	// Search covers the values too and keeps its term in the URL
	// The rows from before the search stay until the filtered ones arrive, so the row that doesn't match has to go before the match counts
	await page.getByRole('searchbox', { name: 'Search state' }).fill('101');
	await expect(page).toHaveURL(/search=101/);
	await expect(stateRow(page, 'last_id')).toBeHidden();
	await expect(stateRow(page, 'cursor')).toBeVisible();
	await page.getByRole('button', { name: 'Clear search' }).click();
	await expect(page).not.toHaveURL(/search=/);
	await expect(stateRow(page, 'last_id')).toBeVisible();

	// Cancelling the confirmation keeps the entry
	await page.getByRole('button', { name: 'Actions for last_id' }).click();
	await page.getByRole('menuitem', { name: 'Delete' }).click();
	const confirm = page.getByRole('alertdialog', { name: 'Delete last_id' });
	await confirm.getByRole('button', { name: 'Cancel' }).click();
	await expect(confirm).toBeHidden();
	await expect(stateRow(page, 'last_id')).toBeVisible();
	expect(await getState(page.request, job.id, 'last_id')).toBe('42');

	// Confirming deletes it, and deleting every entry brings the empty state back
	await deleteEntry(page, 'last_id');
	await expect(stateRow(page, 'last_id')).toBeHidden();
	expect(await getState(page.request, job.id, 'last_id')).toBeNull();
	await deleteEntry(page, 'cursor');
	await expect(page.getByText('No state yet')).toBeVisible();
	expect(await listState(page.request, job.id)).toEqual([]);
});

// A main script that bumps a counter in the job's state by a step and reports the new value
function counterMain(step: number) {
	return [
		'#!/usr/bin/env bash',
		'set -euo pipefail',
		'n=$(ump state get counter || echo 0)',
		`next=$((n + ${step}))`,
		'ump state set counter "$next"',
		'ump summary "Counter is now $next"',
		''
	].join('\n');
}

test('State a run writes carries into the next run, and hand edits to the state and to the main script change what the next run does', async ({
	page
}) => {
	// A main script makes the runs Scripted, so they read and write state through ump without a model
	const job = await runUtil.createJob(page.request, 'Counter job');
	await putPlaybook(
		page.request,
		job.id,
		{ main: counterMain(1) },
		{ summary: 'Wrote the main script', baseVersion: 0 }
	);
	await page.goto(`/jobs/${job.id}`);
	await expect(page.getByText('Runs as')).toContainText('Scripted');

	// The first run starts without a counter and leaves one behind
	const first = await runUtil.runScripted(page.request, job.id, []);
	expect(first.status).toBe('succeeded');
	expect(await runUtil.getRun(page.request, first.runId)).toMatchObject({
		mode: 'scripted',
		turns: 0,
		summary: 'Counter is now 1'
	});
	await page.goto(`/jobs/${job.id}/state`);
	await expect(valueCell(page, 'counter')).toHaveText('1');

	// A value changed by hand is what the next run reads
	await editEntry(page, 'counter', '41');
	const second = await runUtil.runScripted(page.request, job.id, []);
	expect(second.status).toBe('succeeded');
	expect((await runUtil.getRun(page.request, second.runId)).summary).toBe('Counter is now 42');
	expect(await getState(page.request, job.id, 'counter')).toBe('42');
	await page.reload();
	await expect(valueCell(page, 'counter')).toHaveText('42');

	// The main script is edited by hand on the Playbook tab, which becomes a new version
	await page.getByRole('tab', { name: 'Playbook' }).click();
	await page.getByRole('button', { name: /^Main Does the whole job without the agent/ }).click();
	await page.getByRole('button', { name: 'Edit', exact: true }).click();
	const dialog = page.getByRole('dialog', { name: 'Edit the main script' });
	const editor = dialog.getByRole('textbox', { name: 'Edit the main script' });
	await expect(editor).toContainText('next=$((n + 1))');
	await replaceCode(editor, counterMain(10));
	await dialog.getByRole('button', { name: 'Save' }).click();
	await expect(page.getByText('Saved the main script').first()).toBeVisible();
	await expect(dialog).toBeHidden();
	const playbook = await getPlaybook(page.request, job.id);
	expect(playbook).toMatchObject({ version: 2, summary: 'Edited the main script' });
	expect(playbook.content.main).toBe(counterMain(10));

	// A deleted entry is gone for the next run, which starts over from nothing with the edited script
	await page.getByRole('tab', { name: 'State' }).click();
	await deleteEntry(page, 'counter');
	await expect(page.getByText('No state yet')).toBeVisible();
	expect(await getState(page.request, job.id, 'counter')).toBeNull();
	const third = await runUtil.runScripted(page.request, job.id, []);
	expect(third.status).toBe('succeeded');
	expect(await runUtil.getRun(page.request, third.runId)).toMatchObject({
		mode: 'scripted',
		turns: 0,
		summary: 'Counter is now 10'
	});
	await page.reload();
	await expect(valueCell(page, 'counter')).toHaveText('10');
});

test('State the agent writes with its tools and with ump persists even from a failed run, and a missing key reads as not set', async ({
	page
}) => {
	// The first run writes one key with the state_set tool and one with ump state in the sandbox, then gives up
	const job = await runUtil.createJob(page.request, 'Agent state job');
	const cursor = '{"page":2,"seen":[101,102]}';
	const first = await runUtil.runScripted(page.request, job.id, [
		{ toolCalls: [{ name: 'state_set', args: { key: 'cursor', value: cursor } }] },
		{
			toolCalls: [
				{ name: 'bash', args: { command: 'ump state set last_id 41 && ump state get last_id' } }
			]
		},
		{ toolCalls: [{ name: 'finish', args: { status: 'failure', summary: 'The feed was down' } }] }
	]);
	expect(first.status).toBe('failed');
	const written = await runUtil.toolResults(page.request, first.runId);
	expect(written.map((r) => r.name)).toEqual(['state_set', 'bash', 'finish']);
	expect(written[0].content).toBe('Saved.');
	expect(written[1].content).toBe('exit code: 0\n41\n');

	// Writes land right away, so the failed run's state is the job's
	await page.goto(`/jobs/${job.id}/state`);
	await expect(valueCell(page, 'cursor')).toHaveText(cursor);
	await expect(valueCell(page, 'last_id')).toHaveText('41');

	// The next run reads the kept state with the state_get tool and with ump state, and a missing key reads as not set
	const second = await runUtil.runScripted(page.request, job.id, [
		{ toolCalls: [{ name: 'state_get', args: { key: 'last_id' } }] },
		{ toolCalls: [{ name: 'state_get', args: { key: 'never_set' } }] },
		{
			toolCalls: [
				{
					name: 'bash',
					args: { command: 'ump state list; ump state get never_set; echo "exit=$?"' }
				}
			]
		},
		runUtil.finish('Read the state')
	]);
	expect(second.status).toBe('succeeded');
	const read = await runUtil.toolResults(page.request, second.runId);
	expect(read.map((r) => r.name)).toEqual(['state_get', 'state_get', 'bash', 'finish']);
	expect(read[0].content).toBe('41');
	expect(read[1].content).toBe('(not set)');
	expect(read[2].content).toContain('"last_id": "41"');
	expect(read[2].content).toContain('"cursor": "{\\"page\\":2,\\"seen\\":[101,102]}"');
	expect(read[2].content).toContain('exit=1');

	// Reading a missing key doesn't create it
	expect((await listState(page.request, job.id)).map((e) => e.key)).toEqual(['cursor', 'last_id']);
});

test("Another workspace can neither read nor change a job's state, playbook or images, and another job can't reach its image", async ({
	page,
	browser
}, testInfo) => {
	test.skip(!(await authUtil.workspacesEnabled(page)), 'workspaces are turned off');
	const request = page.request;

	// The owner's job has state, a playbook and an image build, which a Dockerfile queues on save
	const job = await runUtil.createJob(request, 'Private job');
	await putState(request, job.id, 'cursor', '7');
	await putPlaybook(
		request,
		job.id,
		{ learnings: [learning('L1', 'fact', 'The feed pages by 50')], dockerfile: dockerfile('true') },
		{ summary: 'Owner version', baseVersion: 0 }
	);

	// The build shares one queue with later tests' builds, so it settles before a failing check here could leave it running
	const builds = await waitForBuilds(request, job.id);
	expect(builds).toHaveLength(1);
	const [image] = builds;

	// Someone signing in elsewhere gets a workspace of their own, from which the job doesn't exist
	const outsider = await authUtil.pageAs(browser, testInfo, {
		subject: 'outsider',
		email: 'outsider@example.com',
		name: 'Outsider'
	});
	try {
		// The job's pages, its state list, a state write and the current playbook are refused the same way in jobs-list.spec.ts
		const calls: { method: string; path: string; data?: unknown }[] = [
			{ method: 'GET', path: `/api/jobs/${job.id}/state/cursor` },
			{ method: 'DELETE', path: `/api/jobs/${job.id}/state/cursor` },
			{ method: 'GET', path: `/api/jobs/${job.id}/playbook/versions` },
			{ method: 'GET', path: `/api/jobs/${job.id}/playbook/versions/1` },
			{ method: 'POST', path: `/api/jobs/${job.id}/playbook/rollback`, data: { version: 0 } },
			{ method: 'GET', path: `/api/jobs/${job.id}/images` },
			{ method: 'GET', path: `/api/jobs/${job.id}/images/${image.id}` },
			{ method: 'POST', path: `/api/jobs/${job.id}/images/rebuild` }
		];
		for (const { method, path, data } of calls) {
			const response = await outsider.request.fetch(path, { method, data });
			expect(response.status(), `${method} ${path}`).toBe(404);
			expect(await response.json(), `${method} ${path}`).toMatchObject({
				code: 'not_found',
				message: 'Job not found'
			});
		}
	} finally {
		await outsider.context().close();
	}

	// Nothing the outsider tried changed the owner's job
	expect(await getState(request, job.id, 'cursor')).toBe('7');
	const playbook = await getPlaybook(request, job.id);
	expect(playbook).toMatchObject({ version: 1, summary: 'Owner version' });
	expect(playbook.content.learnings.map((l) => l.text)).toEqual(['The feed pages by 50']);
	expect((await listImages(request, job.id)).map((i) => i.id)).toEqual([image.id]);

	// Another job of the same workspace can't look up the image or the state either, since both belong to one job
	const other = await runUtil.createJob(request, 'Other job');
	const foreignImage = await request.get(`/api/jobs/${other.id}/images/${image.id}`);
	expect(foreignImage.status()).toBe(404);
	expect(await foreignImage.json()).toMatchObject({
		code: 'not_found',
		message: 'Image not found'
	});
	const foreignState = await request.get(`/api/jobs/${other.id}/state/cursor`);
	expect(foreignState.status()).toBe(404);
	expect(await foreignState.json()).toMatchObject({
		code: 'not_found',
		message: 'State key not found'
	});
});
