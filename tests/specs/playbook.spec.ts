import { expect, test, type Page } from '@playwright/test';
import authUtil from '../utils/auth.util';
import { cleanupBackend } from '../utils/cleanup.util';
import runUtil from '../utils/run.util';

test.describe.configure({ timeout: 60_000 });

test.beforeEach(async ({ page }) => {
	await cleanupBackend();
	await authUtil.authenticate(page);
});

// Replaces the content of the CodeMirror Dockerfile editor and saves it
async function saveDockerfile(page: Page, dockerfile: string) {
	const editor = page.getByRole('textbox', { name: 'Dockerfile', exact: true });
	await editor.click();
	await page.keyboard.press('ControlOrMeta+a');
	await page.keyboard.press('Delete');
	await page.keyboard.insertText(dockerfile);
	await page.getByRole('button', { name: 'Save & build' }).click();
	await expect(page.getByText('Saved, the image is being built').first()).toBeVisible();
}

test('Editing the Dockerfile creates playbook versions that can be rolled back', async ({
	page
}) => {
	const job = await runUtil.createJob(page.request, 'Environment job');
	await page.goto(`/jobs/${job.id}/environment`);
	await expect(page.getByText('No builds yet')).toBeVisible();

	// Every saved Dockerfile is a playbook version and queues an image build
	await saveDockerfile(
		page,
		'FROM ghcr.io/stonith404/umpteenth-sandbox:latest\nRUN echo one > /opt/one\n'
	);
	await expect(page.getByText('Playbook v1')).toBeVisible();
	const builds = page.getByRole('table', { name: 'Image builds' });
	await expect(builds.locator('tbody tr')).toHaveCount(1);

	await saveDockerfile(
		page,
		'FROM ghcr.io/stonith404/umpteenth-sandbox:latest\nRUN echo two > /opt/two\n'
	);
	await expect(page.getByText('Playbook v2')).toBeVisible();
	await expect(builds.locator('tbody tr')).toHaveCount(2);

	// The history lists both versions and shows the change as a diff
	await page.getByRole('tab', { name: 'Playbook' }).click();
	const versions = page.getByRole('table', { name: 'Playbook versions' });
	await expect(versions.getByRole('row', { name: /v2.*Updated the Dockerfile/ })).toContainText(
		'Current'
	);
	await versions.getByRole('button', { name: 'Show changes of version 2' }).click();
	const sheet = page.getByRole('dialog', { name: 'Version 2' });
	await expect(sheet.getByText('Removed: RUN echo one > /opt/one')).toBeAttached();
	await expect(sheet.getByText('Added: RUN echo two > /opt/two')).toBeAttached();
	await page.keyboard.press('Escape');

	// Rolling back creates a third version with the first version's content
	await versions.getByRole('button', { name: 'Roll back to version 1' }).click();
	await page.getByRole('alertdialog').getByRole('button', { name: 'Roll back' }).click();
	await expect(versions.getByRole('row', { name: /v3.*Rolled back to version 1/ })).toBeVisible();
	await expect(page.getByText('Playbook v3')).toBeVisible();

	// The environment shows the restored Dockerfile
	await page.getByRole('tab', { name: 'Environment' }).click();
	await expect(page.getByRole('textbox', { name: 'Dockerfile', exact: true })).toContainText(
		'RUN echo one > /opt/one'
	);
});
