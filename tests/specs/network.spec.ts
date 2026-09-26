import { expect, test } from '@playwright/test';
import http from 'node:http';
import type { AddressInfo } from 'node:net';
import authUtil from '../utils/auth.util';
import { cleanupBackend } from '../utils/cleanup.util';
import { receiverHost } from '../utils/receiver.util';
import runUtil from '../utils/run.util';

// Runs use real sandboxes, so these specs get more time than the suite default
test.describe.configure({ timeout: 90_000 });

test.beforeEach(async ({ page }) => {
	await cleanupBackend(page.request);
	await authUtil.authenticate(page);
});

// A web server on this machine stands for a service on the private network, which the stack reaches through the Docker host
async function startHostService() {
	let hits = 0;
	const server = http.createServer((_req, res) => {
		hits++;
		res.end('hello from the host');
	});
	await new Promise<void>((resolve) => server.listen(0, '0.0.0.0', resolve));
	const { port } = server.address() as AddressInfo;
	return { url: `http://${receiverHost}:${port}/`, hits: () => hits, close: () => server.close() };
}

test('Internet jobs reach private networks through the egress proxy only when allowed', async ({
	page
}) => {
	const service = await startHostService();
	try {
		const job = await runUtil.createJob(page.request, 'Proxy job');
		const command = `curl -s -m 20 ${service.url}; echo; echo "proxy=$HTTPS_PROXY" | sed 's/:[^:@]*@/:***@/'`;
		const output = () =>
			page
				.locator('[data-slot="timeline-step"]', { hasText: 'curl -s' })
				.getByRole('region', { name: 'Output of bash' });

		// Without private network access the proxy refuses the host's private address, and the timeline says so
		const first = await runUtil.runScripted(
			page.request,
			job.id,
			runUtil.bashThenFinish(command, { status: 'success', summary: 'Tried the host' })
		);
		expect(first.status).toBe('succeeded');
		await page.goto(`/runs/${first.runId}`);
		await expect(output()).toContainText('does not connect to this address');
		await expect(output()).toContainText('proxy=http://ump:***@umpteenth:');
		await expect(page.getByTestId('run-timeline')).toContainText(`proxy ${receiverHost}`);
		expect(service.hits()).toBe(0);

		// With it the same request goes through the proxy to the host
		const response = await page.request.patch(`/api/jobs/${job.id}`, {
			data: { allowPrivateNetwork: true }
		});
		expect(response.ok()).toBeTruthy();
		const second = await runUtil.runScripted(
			page.request,
			job.id,
			runUtil.bashThenFinish(command, { status: 'success', summary: 'Reached the host' })
		);
		expect(second.status).toBe('succeeded');
		await page.goto(`/runs/${second.runId}`);
		await expect(output()).toContainText('hello from the host');
		expect(service.hits()).toBe(1);
	} finally {
		service.close();
	}
});
