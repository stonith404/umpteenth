import { expect, test, type Locator, type Page } from '@playwright/test';
import authUtil from '../utils/auth.util';
import { cleanupBackend } from '../utils/cleanup.util';
import {
	dockerfile,
	fillDockerfile,
	getPlaybook,
	learning,
	listImages,
	putPlaybook,
	sandboxImage,
	saveDockerfile,
	waitForBuilds
} from '../utils/playbook.util';
import runUtil from '../utils/run.util';
import { toast } from '../utils/ui.util';

// Runs use real sandboxes and every saved Dockerfile builds a real image, so these specs get more time than the suite default
test.describe.configure({ timeout: 120_000 });

test.beforeEach(async ({ page }) => {
	await cleanupBackend(page.request);
	await authUtil.authenticate(page);
});

// The log section of the image sheet, which the sheet heads with 'Build log'
function buildLog(sheet: Locator) {
	return sheet.locator('section', {
		has: sheet.page().getByRole('heading', { name: 'Build log' })
	});
}

// Expects a toast with the title and description and returns it
async function expectToast(page: Page, title: string, description: string) {
	const shown = toast(page, title, description);
	await expect(shown).toBeVisible();
	return shown;
}

test('A failed image build shows its log and fails runs until a fixed Dockerfile builds', async ({
	page
}) => {
	const job = await runUtil.createJob(page.request, 'Build job');
	await page.goto(`/jobs/${job.id}/environment`);
	await expect(page.getByText('Runs use the base image')).toBeVisible();

	// The editor opens on a template that builds on the workspace's default image
	await page.getByRole('button', { name: 'Add a Dockerfile' }).click();
	const editor = page.getByRole('textbox', { name: 'Dockerfile', exact: true });
	await expect(editor).toContainText(`FROM ${sandboxImage}`);

	// A Dockerfile whose RUN fails still saves, and its build fails
	// The command prints a number it computes, so the log shows its output rather than the Dockerfile's text
	await fillDockerfile(page, dockerfile('echo "compiled $((40 + 2)) files" && exit 3'));
	const save = page.getByRole('button', { name: 'Save & build' });
	await save.click();
	await expect(
		page.getByText('The image is being built. Runs wait until it is ready.').first()
	).toBeVisible();
	await expect(page.getByRole('button', { name: 'Rebuild' })).toBeVisible();
	await expect(save).toBeDisabled();
	const builds = page.getByRole('table', { name: 'Image builds' });
	const statuses = builds.locator('[data-slot="image-status-badge"]');
	// This can be the suite's first build, which may have to download the base image
	await expect(statuses).toHaveText(['Failed'], { timeout: 60_000 });

	// The build's sheet explains the failure with the log and the Dockerfile it built
	await builds.getByRole('button', { name: 'Log' }).click();
	const sheet = page.getByRole('dialog', { name: /^Image build/ });
	await expect(sheet.locator('[data-slot="image-status-badge"]')).toHaveText('Failed');
	await expect(sheet.getByRole('alert')).toContainText('The build failed');
	await expect(buildLog(sheet)).toContainText('compiled 42 files');
	await expect(buildLog(sheet)).toContainText('Build failed:');
	await expect(sheet.getByRole('textbox', { name: 'Dockerfile of this build' })).toContainText(
		'exit 3'
	);
	await page.keyboard.press('Escape');
	await expect(sheet).toBeHidden();

	// A run never falls back to the base image, so it fails before it asks the model anything
	await runUtil.scriptModel(page.request, []);
	const failedRunId = await runUtil.startRun(page.request, job.id);
	expect(await runUtil.waitForRun(page.request, failedRunId)).toBe('failed');
	expect((await runUtil.getRun(page.request, failedRunId)).imageRef).toBeNull();
	await page.goto(`/runs/${failedRunId}`);
	await expect(page.getByRole('alert')).toContainText('Environment build failed');

	// A fixed Dockerfile gets a build of its own, while the failed one stays in the list with its log
	await page.goto(`/jobs/${job.id}/environment`);
	await expect(editor).toContainText('exit 3');
	await saveDockerfile(page, dockerfile('echo tools-installed > /opt/marker'));
	await expect(statuses).toHaveText(['Ready', 'Failed'], { timeout: 30_000 });
	const [fixed] = await listImages(page.request, job.id);

	// The next run starts from the fixed image and finds what its Dockerfile installed
	const { runId, status } = await runUtil.runScripted(
		page.request,
		job.id,
		runUtil.bashThenFinish('cat /opt/marker', { status: 'success', summary: 'Found the tools' })
	);
	expect(status).toBe('succeeded');
	expect((await runUtil.getRun(page.request, runId)).imageRef).toBe(fixed.ref);
	await page.goto(`/runs/${runId}`);
	await expect(page.getByRole('region', { name: 'Output of bash' })).toContainText(
		'tools-installed'
	);
});

test('Rebuild builds the saved Dockerfile again, opens the new build and the next run uses it', async ({
	page
}) => {
	const job = await runUtil.createJob(page.request, 'Rebuild job');
	await putPlaybook(
		page.request,
		job.id,
		{ dockerfile: dockerfile('echo rebuilt') },
		{ summary: 'Added a Dockerfile', baseVersion: 0 }
	);
	await waitForBuilds(page.request, job.id);
	await page.goto(`/jobs/${job.id}/environment`);
	const statuses = page
		.getByRole('table', { name: 'Image builds' })
		.locator('[data-slot="image-status-badge"]');
	await expect(statuses).toHaveText(['Ready']);

	// A rebuild opens the new build's sheet by itself, which follows the build until it is ready
	await page.getByRole('button', { name: 'Rebuild' }).click();
	await expect(page.getByText('Rebuilding the image')).toBeVisible();
	const sheet = page.getByRole('dialog', { name: /^Image build/ });
	await expect(sheet.locator('[data-slot="image-status-badge"]')).toHaveText('Ready', {
		timeout: 30_000
	});
	await expect(buildLog(sheet)).toContainText('Built ');

	// The rebuild is a new image with a tag of its own
	const images = await listImages(page.request, job.id);
	expect(images.map((image) => image.status)).toEqual(['ready', 'ready']);
	expect(images[0].ref).toContain(`job-${job.id}:`);
	expect(images[0].ref).not.toBe(images[1].ref);
	await expect(sheet.getByRole('textbox', { name: 'Reference' })).toHaveValue(images[0].ref!);
	await page.keyboard.press('Escape');
	await expect(statuses).toHaveText(['Ready', 'Ready']);

	// The next run starts from the rebuilt image instead of the older build of the same Dockerfile
	const { runId, status } = await runUtil.runScripted(page.request, job.id, [
		runUtil.finish('Ran on the rebuilt image')
	]);
	expect(status).toBe('succeeded');
	expect((await runUtil.getRun(page.request, runId)).imageRef).toBe(images[0].ref);
});

// A learning written outside the Environment tab, which a Dockerfile saved there must keep
const otherTabLearning = learning('L1', 'fact', 'Another tab knows this');

test('Saving a Dockerfile over a newer playbook version fails without losing the text', async ({
	page
}) => {
	const job = await runUtil.createJob(page.request, 'Conflict job');
	await page.goto(`/jobs/${job.id}/environment`);
	await page.getByRole('button', { name: 'Add a Dockerfile' }).click();
	await fillDockerfile(page, dockerfile('echo a'));

	// Another tab adds a learning meanwhile, so the page's version is outdated and the save is refused
	await putPlaybook(
		page.request,
		job.id,
		{ learnings: [otherTabLearning] },
		{ summary: 'Added a learning elsewhere', baseVersion: 0 }
	);
	const save = page.getByRole('button', { name: 'Save & build' });
	await save.click();
	const conflict = await expectToast(
		page,
		'Failed to save the Dockerfile',
		'The playbook changed since it was loaded'
	);
	const editor = page.getByRole('textbox', { name: 'Dockerfile', exact: true });
	await expect(editor).toContainText('RUN echo a');
	expect((await getPlaybook(page.request, job.id)).version).toBe(1);

	// The refused save reloaded the playbook, so saving again with the shortcut keeps the other tab's learning
	await page.keyboard.press('ControlOrMeta+s');
	await expect(page.getByText('Saved the Dockerfile').first()).toBeVisible();
	await expect(page.getByRole('button', { name: 'Rebuild' })).toBeVisible();
	await expect(save).toBeDisabled();
	let playbook = await getPlaybook(page.request, job.id);
	expect(playbook.version).toBe(2);
	expect(playbook.content.dockerfile).toBe(dockerfile('echo a'));
	expect(playbook.content.learnings.map((l) => l.text)).toEqual([otherTabLearning.text]);

	// Another tab changes the Dockerfile itself while this one edits it
	await fillDockerfile(page, dockerfile('echo b'));
	await putPlaybook(
		page.request,
		job.id,
		{ learnings: [otherTabLearning], dockerfile: dockerfile('echo c') },
		{ summary: 'Changed the Dockerfile elsewhere', baseVersion: 2 }
	);
	// The first refusal's toast has to be gone, since the next one reads the same
	await expect(conflict).toBeHidden({ timeout: 10_000 });
	await save.click();
	await expectToast(
		page,
		'Failed to save the Dockerfile',
		'The playbook changed since it was loaded'
	);

	// The page now knows the newer Dockerfile, which the text in the editor was never written against, so it refuses to overwrite it
	await save.click();
	await expectToast(
		page,
		'Failed to save the Dockerfile',
		'It changed since you started editing. Copy your changes, then reload the page to start from the latest version.'
	);
	await expect(editor).toContainText('RUN echo b');
	playbook = await getPlaybook(page.request, job.id);
	expect(playbook.version).toBe(3);
	expect(playbook.content.dockerfile).toBe(dockerfile('echo c'));

	// A reload starts from the newer Dockerfile
	await page.reload();
	await expect(editor).toContainText('RUN echo c');
	await expect(save).toBeDisabled();
	await waitForBuilds(page.request, job.id);
});

test('Using the base image removes the Dockerfile but keeps its builds and history', async ({
	page
}) => {
	const job = await runUtil.createJob(page.request, 'Base image job');
	await putPlaybook(
		page.request,
		job.id,
		{ learnings: [otherTabLearning], dockerfile: dockerfile('echo custom') },
		{ summary: 'Added a Dockerfile', baseVersion: 0 }
	);
	await page.goto(`/jobs/${job.id}/environment`);
	const editor = page.getByRole('textbox', { name: 'Dockerfile', exact: true });
	await expect(editor).toContainText('RUN echo custom');

	// The shortcut saves nothing while the text is unchanged, which the removal's version number checks below
	await editor.click();
	await page.keyboard.press('ControlOrMeta+s');

	// Using the base image asks for confirmation before it removes the Dockerfile
	await page.getByRole('button', { name: 'Use the base image' }).click();
	await page
		.getByRole('alertdialog', { name: 'Remove the Dockerfile' })
		.getByRole('button', { name: 'Remove' })
		.click();
	await expect(page.getByText('Removed the Dockerfile')).toBeVisible();

	// The tab goes back to its calm panel, and keeps the builds the Dockerfile had
	await expect(page.getByText('Runs use the base image')).toBeVisible();
	await expect(editor).toBeHidden();
	await expect(page.getByRole('table', { name: 'Image builds' })).toBeVisible();
	const playbook = await getPlaybook(page.request, job.id);
	// A stray save would add a version, so the removal landing as version 2 proves the shortcut wrote nothing
	expect(playbook.version, 'the shortcut must not save an unchanged Dockerfile').toBe(2);
	expect(playbook.content.dockerfile).toBeNull();
	expect(playbook.content.learnings.map((l) => l.text)).toEqual([otherTabLearning.text]);

	// The history keeps the removal as the current version, and there is nothing left to rebuild
	await page.getByRole('tab', { name: 'Playbook' }).click();
	const versions = page.getByRole('table', { name: 'Playbook versions' });
	await expect(versions.getByRole('row', { name: /v2.*Removed the Dockerfile/ })).toContainText(
		'Current'
	);
	const rebuild = await page.request.post(`/api/jobs/${job.id}/images/rebuild`);
	expect(rebuild.status()).toBe(422);
	expect(await rebuild.json()).toMatchObject({ message: 'The job has no Dockerfile' });
	await waitForBuilds(page.request, job.id);
});
