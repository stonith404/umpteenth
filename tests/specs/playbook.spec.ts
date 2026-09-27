import { expect, test } from '@playwright/test';
import authUtil from '../utils/auth.util';
import { cleanupBackend } from '../utils/cleanup.util';
import { dockerfile, getPlaybook, saveDockerfile } from '../utils/playbook.util';
import runUtil from '../utils/run.util';

test.describe.configure({ timeout: 60_000 });

test.beforeEach(async ({ page }) => {
	await cleanupBackend(page.request);
	await authUtil.authenticate(page);
});

test('Editing the Dockerfile creates playbook versions that can be rolled back', async ({
	page
}) => {
	const job = await runUtil.createJob(page.request, 'Environment job');
	await page.goto(`/jobs/${job.id}/environment`);
	await expect(page.getByText('Runs use the base image')).toBeVisible();

	// A job without a Dockerfile shows the editor only once one is added
	await page.getByRole('button', { name: 'Add a Dockerfile' }).click();

	// Every saved Dockerfile is a playbook version and queues an image build
	await saveDockerfile(page, dockerfile('echo one > /opt/one'));
	expect((await getPlaybook(page.request, job.id)).version).toBe(1);
	// Status badges only render in loaded rows, so they skip the placeholder rows the table shows while it loads
	const statuses = page
		.getByRole('table', { name: 'Image builds' })
		.locator('[data-slot="image-status-badge"]');
	await expect(statuses).toHaveCount(1);

	await saveDockerfile(page, dockerfile('echo two > /opt/two'));
	expect((await getPlaybook(page.request, job.id)).version).toBe(2);
	await expect(statuses).toHaveCount(2);

	// The history lists both versions and shows the change as a diff
	await page.getByRole('tab', { name: 'Playbook' }).click();
	await expect(page.getByRole('heading', { name: 'Version 2' })).toBeVisible();
	const versions = page.getByRole('table', { name: 'Playbook versions' });
	await expect(versions.getByRole('row', { name: /v2.*Updated the Dockerfile/ })).toContainText(
		'Current'
	);
	await expect(versions.getByRole('row', { name: /v1.*Updated the Dockerfile/ })).not.toContainText(
		'Current'
	);
	await versions.getByRole('button', { name: 'Actions for version 2' }).click();
	await page.getByRole('menuitem', { name: 'Show changes' }).click();
	const sheet = page.getByRole('dialog', { name: 'Version 2' });
	await expect(sheet.getByText('Removed: RUN echo one > /opt/one')).toBeAttached();
	await expect(sheet.getByText('Added: RUN echo two > /opt/two')).toBeAttached();
	await page.keyboard.press('Escape');

	// Rolling back creates a third version with the first version's content
	await versions.getByRole('button', { name: 'Actions for version 1' }).click();
	await page.getByRole('menuitem', { name: 'Roll back to version 1' }).click();
	await page.getByRole('alertdialog').getByRole('button', { name: 'Roll back' }).click();
	await expect(versions.getByRole('row', { name: /v3.*Rolled back to version 1/ })).toContainText(
		'Current'
	);
	await expect(page.getByRole('heading', { name: 'Version 3' })).toBeVisible();
	const restored = await getPlaybook(page.request, job.id);
	expect(restored.version).toBe(3);
	expect(restored.content.dockerfile).toBe(dockerfile('echo one > /opt/one'));

	// The environment shows the restored Dockerfile, whose image exists already, so no third build is queued and both builds succeed
	await page.getByRole('tab', { name: 'Environment' }).click();
	await expect(page.getByRole('textbox', { name: 'Dockerfile', exact: true })).toContainText(
		'RUN echo one > /opt/one'
	);
	await expect(statuses).toHaveText(['Ready', 'Ready'], { timeout: 30_000 });
});
