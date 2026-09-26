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

test('A webhook starts a run with its body as input, with or without a body', async ({
	page,
	playwright,
	baseURL
}) => {
	const job = await runUtil.createJob(page.request, 'Webhook job');
	const rotated = await page.request.post(`/api/jobs/${job.id}/webhook-token`);
	expect(rotated.ok()).toBeTruthy();
	const { token } = (await rotated.json()) as { token: string };

	// Webhook callers are external systems without the browser session
	const external = await playwright.request.newContext({ baseURL });
	const auth = { Authorization: `Bearer ${token}` };
	try {
		// A JSON body becomes the run's input
		await runUtil.scriptModel(page.request, []);
		let response = await external.post(`/hooks/${job.id}`, { headers: auth, data: { pr: 42 } });
		expect(response.status()).toBe(200);
		const { runId } = (await response.json()) as { runId: string };
		expect(await runUtil.waitForRun(page.request, runId)).toBe('succeeded');
		const run = await runUtil.getRun(page.request, runId);
		expect(run.trigger).toBe('webhook');
		expect(run.input).toEqual({ pr: 42 });

		// Schedulers and `curl -X POST` often send no body at all
		await runUtil.scriptModel(page.request, []);
		response = await external.post(`/hooks/${job.id}`, { headers: auth });
		expect(response.status()).toBe(200);
		const second = (await response.json()) as { runId: string };
		expect(await runUtil.waitForRun(page.request, second.runId)).toBe('succeeded');

		// Missing and wrong tokens are both unauthorized
		expect((await external.post(`/hooks/${job.id}`)).status()).toBe(401);
		const wrong = await external.post(`/hooks/${job.id}`, {
			headers: { Authorization: 'Bearer nope' }
		});
		expect(wrong.status()).toBe(401);
	} finally {
		await external.dispose();
	}
});
