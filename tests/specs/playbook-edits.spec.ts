import { expect, test, type Page } from '@playwright/test';
import authUtil from '../utils/auth.util';
import { cleanupBackend } from '../utils/cleanup.util';
import runUtil, { type ReflectionOp } from '../utils/run.util';

const { finish, reflection } = runUtil;

// Runs use real sandboxes, so these specs get more time than the suite default
test.describe.configure({ timeout: 120_000 });

test.beforeEach(async ({ page }) => {
	await cleanupBackend(page.request);
	await authUtil.authenticate(page);
});

type Playbook = {
	version: number;
	content: {
		learnings: { id: string; text: string }[];
		toolkit: { name: string; content: string }[];
	};
};

function storiesScript(body: string) {
	return `#!/usr/bin/env bash
# ump:name        stories
# ump:description Print the stories
# ump:side-effects none
${body}`;
}

async function getPlaybook(page: Page, jobId: string) {
	const response = await page.request.get(`/api/jobs/${jobId}/playbook`);
	expect(response.ok()).toBeTruthy();
	return (await response.json()) as Playbook;
}

// A learning job whose first run leaves version 1 with learning L1 and the stories script, opened on its playbook
async function openLearnedPlaybook(page: Page) {
	const job = await runUtil.createJob(page.request, 'Learning job', {
		instruction: 'Report the top stories.',
		selfImprove: true
	});
	const { runId } = await runUtil.runScripted(page.request, job.id, [
		finish('Reported the stories'),
		reflection('Learned the basics', [
			{ op: 'add_learning', kind: 'fact', text: 'Stories load slowly' },
			{ op: 'upsert_script', content: storiesScript('echo "v1"') }
		])
	]);
	expect((await runUtil.waitForReflection(page.request, runId)).reflectionVersion).toBe(1);
	await page.goto(`/jobs/${job.id}/playbook`);
	await expect(page.getByRole('heading', { name: 'Version 1' })).toBeVisible();
	return job.id;
}

// Another run's reflection writes version 2 while a dialog is open, and the page follows it live
async function reflectMeanwhile(page: Page, jobId: string, ops: ReflectionOp[]) {
	const { runId } = await runUtil.runScripted(page.request, jobId, [
		finish('Reported the stories'),
		reflection('Learned more', ops)
	]);
	expect((await runUtil.waitForReflection(page.request, runId)).reflectionVersion).toBe(2);
	await expect(page.getByRole('heading', { name: 'Version 2' })).toBeVisible();
}

// The dialog still holds what version 1 had, so saving it must fail instead of writing it over reflection's version
async function expectConflict(page: Page, jobId: string) {
	await expect(page.getByText('The playbook changed since it was loaded').first()).toBeVisible();
	const playbook = await getPlaybook(page, jobId);
	expect(playbook.version).toBe(2);
	return playbook;
}

test('Saving the playbook JSON fails when reflection wrote a version while the dialog was open', async ({
	page
}) => {
	const jobId = await openLearnedPlaybook(page);
	await page.getByRole('button', { name: 'Edit as JSON' }).click();
	const dialog = page.getByRole('dialog', { name: 'Edit playbook' });
	await expect(dialog).toBeVisible();

	await reflectMeanwhile(page, jobId, [
		{ op: 'add_learning', kind: 'fact', text: 'Weekends have fewer stories' }
	]);
	await dialog.getByRole('button', { name: 'Save as new version' }).click();
	const playbook = await expectConflict(page, jobId);
	expect(playbook.content.learnings.map((l) => l.text)).toContain('Weekends have fewer stories');

	// Opening the dialog again starts from reflection's version, which then saves
	await dialog.getByRole('button', { name: 'Cancel' }).click();
	await expect(dialog).toBeHidden();
	await page.getByRole('button', { name: 'Edit as JSON' }).click();
	await dialog.getByRole('button', { name: 'Save as new version' }).click();
	await expect(page.getByRole('heading', { name: 'Version 3' })).toBeVisible();
	expect((await getPlaybook(page, jobId)).content.learnings.map((l) => l.text)).toContain(
		'Weekends have fewer stories'
	);
});

test('Saving a toolkit script fails when reflection changed it while the dialog was open', async ({
	page
}) => {
	const jobId = await openLearnedPlaybook(page);
	await page.getByText('Print the stories', { exact: true }).click();
	await page.getByRole('button', { name: 'Edit script stories' }).click();
	const dialog = page.getByRole('dialog', { name: 'Edit script stories' });
	await expect(dialog).toBeVisible();

	await reflectMeanwhile(page, jobId, [
		{ op: 'upsert_script', content: storiesScript('echo "reflection"') }
	]);
	await dialog.getByRole('textbox', { name: 'Edit script stories' }).click();
	await page.keyboard.press('ControlOrMeta+End');
	await page.keyboard.insertText(' # edited by hand');
	await dialog.getByRole('button', { name: 'Save' }).click();
	const playbook = await expectConflict(page, jobId);
	expect(playbook.content.toolkit[0].content).toContain('echo "reflection"');
});

test('Saving a learning fails when reflection rewrote it while the dialog was open', async ({
	page
}) => {
	const jobId = await openLearnedPlaybook(page);
	await page.getByRole('button', { name: 'Edit learning L1' }).click();
	const dialog = page.getByRole('dialog', { name: 'Edit learning L1' });
	await expect(dialog).toBeVisible();

	await reflectMeanwhile(page, jobId, [
		{ op: 'update_learning', id: 'L1', text: 'Stories load slowly after midnight' }
	]);
	await dialog.getByLabel('Text').fill('Stories load slowly, edited by hand');
	await dialog.getByRole('button', { name: 'Save' }).click();
	const playbook = await expectConflict(page, jobId);
	expect(playbook.content.learnings[0].text).toBe('Stories load slowly after midnight');
});
