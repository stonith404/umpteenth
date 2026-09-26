import { expect, test } from '@playwright/test';
import http from 'node:http';
import type { AddressInfo } from 'node:net';
import authUtil from '../utils/auth.util';
import { cleanupBackend } from '../utils/cleanup.util';
import runUtil from '../utils/run.util';

test.describe.configure({ timeout: 60_000 });

test.beforeEach(async ({ page }) => {
	await cleanupBackend();
	await authUtil.authenticate(page);
});

type Delivery = { event: string; text: string; run?: { url: string; status: string } };

// A webhook receiver on this machine, which Umpteenth reaches from its container through host.docker.internal
async function startReceiver() {
	const received: Delivery[] = [];
	const server = http.createServer((req, res) => {
		let body = '';
		req.on('data', (chunk) => (body += chunk));
		req.on('end', () => {
			received.push(JSON.parse(body) as Delivery);
			res.writeHead(204).end();
		});
	});
	await new Promise<void>((resolve) => server.listen(0, '0.0.0.0', resolve));
	const port = (server.address() as AddressInfo).port;
	return { received, url: `http://host.docker.internal:${port}/hook`, close: () => server.close() };
}

test('Failed runs are posted to the notification webhook', async ({ page }) => {
	const receiver = await startReceiver();
	try {
		// The webhook is set in the settings and a test message confirms it works
		await page.goto('/settings/general');
		await page.getByLabel('Webhook URL').fill(receiver.url);
		await page.getByRole('button', { name: 'Save', exact: true }).click();
		await expect(page.getByRole('button', { name: 'Send test' })).toBeEnabled();
		await page.getByRole('button', { name: 'Send test' }).click();
		await expect(page.getByText('Sent a test notification')).toBeVisible();
		expect(receiver.received.map((d) => d.event)).toEqual(['test']);

		// A failing run is reported with a link to it
		const job = await runUtil.createJob(page.request, 'Failing job');
		const { runId, status } = await runUtil.runScripted(page.request, job.id, [
			{ toolCalls: [{ name: 'finish', args: { status: 'failure', summary: 'The API was down' } }] }
		]);
		expect(status).toBe('failed');
		await expect.poll(() => receiver.received.length, { timeout: 20_000 }).toBe(2);
		const failure = receiver.received[1];
		expect(failure.event).toBe('run.failed');
		expect(failure.text).toContain('Failing job run #1 failed');
		expect(failure.run?.url).toContain(`/runs/${runId}`);
	} finally {
		receiver.close();
	}
});
