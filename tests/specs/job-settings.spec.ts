import { expect, test, type Locator, type Page } from '@playwright/test';
import { createHash } from 'node:crypto';
import http from 'node:http';
import type { AddressInfo } from 'node:net';
import authUtil, { accounts } from '../utils/auth.util';
import { cleanupBackend } from '../utils/cleanup.util';
import { saveForm } from '../utils/form.util';
import { receiverHost } from '../utils/receiver.util';
import { bashOutput } from '../utils/run-view.util';
import runUtil from '../utils/run.util';
import { toast } from '../utils/ui.util';
import { createSecret } from '../utils/workspace.util';

// Some of these specs run real sandboxes, so they get more time than the suite default
test.describe.configure({ timeout: 60_000 });

test.beforeEach(async ({ page }) => {
	await cleanupBackend(page.request);
	await authUtil.authenticate(page);
});

// One card of the job's Settings tab, whose form is named after the card's title
function card(page: Page, title: string) {
	return page.getByRole('form', { name: title });
}

// Opens a Select and picks one of its options, which render in a portal outside the card
async function pickOption(page: Page, trigger: Locator, name: string | RegExp) {
	await trigger.click();
	await page.getByRole('option', { name }).click();
}

// Clicks a card's Save and expects the error that refuses it, from the card's own validation or from the backend
// A saved card's values become its baseline, which disables Save, so a refused save leaves Save enabled
// The spinner names the button "Loading Save" while the request runs, so the locator finds Save enabled only once the request is over
async function expectSaveRejected(form: Locator, error: Locator) {
	const save = form.getByRole('button', { name: 'Save', exact: true });
	await save.click();
	await expect(error).toBeVisible();
	await expect(save).toBeEnabled();
}

// The time a step gets that waits on the container engine or on a cold load of the app
// The engine serves every sandbox on the machine, and while the suite runs sharded both took longer than the 5 s expect timeout
const slowStepTimeout = 15_000;

// The first 12 hex characters of a value's SHA-256, which lets a run prove it saw a secret without printing it
function sha12(value: string) {
	return createHash('sha256').update(value).digest('hex').slice(0, 12);
}

// A web server on this machine stands for a service on the private network, which the stack reaches through the Docker host
async function startHostService() {
	let hits = 0;
	const server = http.createServer((_req, res) => {
		hits++;
		res.end('hello from the host');
	});
	await new Promise<void>((resolve) => server.listen(0, '0.0.0.0', resolve));
	const { port } = server.address() as AddressInfo;
	return {
		url: `http://${receiverHost}:${port}/`,
		hits: () => hits,
		close: () => new Promise<void>((resolve) => server.close(() => resolve()))
	};
}

// The spec the fake utility model answers the compile step with, as in jobs.spec.ts
const compiledSpec = {
	title: 'Stale PR digest',
	goal: 'Post a digest of stale pull requests',
	schedule: { cron: '0 8 * * 1-5', timezone: 'Europe/Berlin', human: 'Weekdays at 08:00' },
	successCriteria: ['All open PRs are considered', 'Exactly one message is posted'],
	inputs: [],
	outputs: [{ name: 'count', type: 'integer', description: 'Number of stale PRs' }],
	mcp: [{ server: 'slack', why: 'post the digest' }],
	network: 'internet',
	dockerfile: null,
	sideEffects: ['Posts to Slack'],
	questions: []
};

test('The Secrets card refuses incomplete, invalid and duplicate mappings, drops empty rows and stores the rest without values', async ({
	page
}) => {
	// Two workspace secrets and a job that maps neither of them yet
	const githubId = await createSecret(page.request, 'github-token', 'ghp_e2e_first_value');
	const apiKeyId = await createSecret(page.request, 'api.key', 'sk-e2e-api-key-1');
	const job = await runUtil.createJob(page.request, 'Secrets job');
	await page.goto(`/jobs/${job.id}/settings`);
	const secrets = card(page, 'Secrets');
	await expect(secrets.getByText('No secrets are passed to this job')).toBeVisible();

	// A row with a name but no secret is refused, and its error then follows the fixes as they are made
	// The error is read from its field-error slot, since 'Pick a secret' is also the empty picker's text
	const errors = secrets.locator('[data-slot="field-error"]');
	await secrets.getByRole('button', { name: 'Add secret' }).click();
	const firstName = secrets.getByRole('textbox', { name: 'Environment variable 1' });
	await firstName.fill('9LIVES');
	await expectSaveRejected(secrets, errors.getByText('Pick a secret'));

	// Picking a secret keeps a name that was typed already, which is not a valid variable name here
	await pickOption(
		page,
		secrets.getByRole('button', { name: 'Secret 1', exact: true }),
		'github-token'
	);
	await expect(firstName).toHaveValue('9LIVES');
	await expect(errors).toHaveText('Letters, digits and underscores, not starting with a digit');
	await firstName.clear();
	await expect(errors).toHaveText('Required');
	await firstName.fill('GITHUB_TOKEN');
	await expect(errors).toHaveCount(0);

	// An empty name is suggested from the secret's name, and a name used twice is refused
	await secrets.getByRole('button', { name: 'Add secret' }).click();
	await pickOption(page, secrets.getByRole('button', { name: 'Secret 2', exact: true }), 'api.key');
	const secondName = secrets.getByRole('textbox', { name: 'Environment variable 2' });
	await expect(secondName).toHaveValue('API_KEY');
	await secondName.fill('GITHUB_TOKEN');
	await expect(errors).toHaveText('GITHUB_TOKEN is mapped twice');
	await secondName.fill('API_KEY');
	await expect(errors).toHaveCount(0);

	// A row left empty is dropped rather than refused
	await secrets.getByRole('button', { name: 'Add secret' }).click();
	await saveForm(secrets);
	const rows = secrets.getByRole('list', { name: 'Secret mappings' }).getByRole('listitem');
	await expect(rows).toHaveCount(2);

	// The API lists the mappings by variable name, and their exact shape leaves no room for a secret's value
	const mapped = await page.request.get(`/api/jobs/${job.id}/secrets`);
	expect(await mapped.json()).toEqual([
		{ secretId: apiKeyId, secretName: 'api.key', envName: 'API_KEY' },
		{ secretId: githubId, secretName: 'github-token', envName: 'GITHUB_TOKEN' }
	]);

	// The mappings survive a reload in the same order
	await page.reload();
	await expect(rows).toHaveCount(2);
	await expect(secrets.getByRole('button', { name: 'Secret 1', exact: true })).toHaveText(
		'api.key'
	);
	await expect(secrets.getByRole('textbox', { name: 'Environment variable 1' })).toHaveValue(
		'API_KEY'
	);
});

test('Mapped secrets reach the sandbox with the value they have when the run starts, and a deleted secret is no longer passed', async ({
	page
}) => {
	// Two workspace secrets, both mapped to one job
	const githubId = await createSecret(page.request, 'github-token', 'ghp_e2e_first_value');
	const apiKeyId = await createSecret(page.request, 'api.key', 'sk-e2e-api-key-1');
	const job = await runUtil.createJob(page.request, 'Secrets job');
	const mapped = await page.request.put(`/api/jobs/${job.id}/secrets`, {
		data: [
			{ secretId: githubId, envName: 'GITHUB_TOKEN' },
			{ secretId: apiKeyId, envName: 'API_KEY' }
		]
	});
	expect(mapped.ok()).toBeTruthy();

	// Change one workspace secret and delete the other after mapping them, which a run has to follow since it reads the values when it starts
	const updated = await page.request.put(`/api/secrets/${githubId}`, {
		data: { value: 'ghp_e2e_second_value' }
	});
	expect(updated.ok()).toBeTruthy();
	expect((await page.request.delete(`/api/secrets/${apiKeyId}`)).ok()).toBeTruthy();

	// A deleted secret leaves the job that mapped it
	const left = await page.request.get(`/api/jobs/${job.id}/secrets`);
	expect(await left.json()).toEqual([
		{ secretId: githubId, secretName: 'github-token', envName: 'GITHUB_TOKEN' }
	]);

	// The run prints a hash of each variable rather than its value, so neither the command nor its output holds a secret
	const command = `for v in GITHUB_TOKEN API_KEY; do if [ -n "\${!v+x}" ]; then printf '%s=%s\\n' "$v" "$(printf %s "\${!v}" | sha256sum | cut -c1-12)"; else echo "$v=unset"; fi; done`;
	const { runId, status } = await runUtil.runScripted(
		page.request,
		job.id,
		runUtil.bashThenFinish(command, { status: 'success', summary: 'Checked the variables' })
	);
	expect(status).toBe('succeeded');
	await page.goto(`/runs/${runId}`);
	const output = bashOutput(page, 'sha256sum');
	await expect(output).toContainText(`GITHUB_TOKEN=${sha12('ghp_e2e_second_value')}`);
	await expect(output).toContainText('API_KEY=unset');
});

test('An allow-list set in the Sandbox card is validated, stored tidied and enforced by the egress proxy', async ({
	page
}) => {
	// A service on the host that the allow-list names, and a job that reaches the internet so far
	const service = await startHostService();
	const host = new URL(service.url).hostname;
	try {
		const job = await runUtil.createJob(page.request, 'Allow-list job');
		await page.goto(`/jobs/${job.id}/settings`);
		const sandbox = card(page, 'Sandbox');
		const network = sandbox.getByLabel('Network', { exact: true });
		const domains = sandbox.getByLabel('Allowed domains');
		const privateNetwork = sandbox.getByRole('switch', { name: 'Allow private network' });

		// An allow-list without any domain is refused next to its field
		await pickOption(page, network, /^Allowed domains only/);
		await expect(domains).toBeVisible();
		await expectSaveRejected(
			sandbox,
			sandbox.getByText('an allow-list job needs at least one domain')
		);
		await expect(domains).toHaveAttribute('aria-invalid', 'true');

		// A URL is no host name, so the backend refuses it and the job keeps its network
		await domains.fill('https://api.github.com');
		await expectSaveRejected(
			sandbox,
			page.getByText(/must be a host name such as api\.example\.com or \*\.example\.com/)
		);
		expect((await runUtil.getJob(page.request, job.id)).network).toBe('internet');

		// Valid names are stored lowercased, without trailing dots, blank lines or repeats
		// A new network is only stored once the backend asked the container engine whether it offers it, which the save waits for
		await domains.fill(`${host.toUpperCase()}.\n*.example.org\n\n${host}`);
		await privateNetwork.click();
		await saveForm(sandbox, { timeout: slowStepTimeout });
		const saved = await runUtil.getJob(page.request, job.id);
		expect(saved.network).toBe('allowlist');
		expect(saved.allowedDomains).toEqual([host, '*.example.org']);
		expect(saved.allowPrivateNetwork).toBe(true);

		// A reload shows the stored list
		// The page renders once the backend asked the engine for the networks it offers, which it gives up to 5 s
		await page.reload();
		await expect(domains).toHaveValue(`${host}\n*.example.org`, { timeout: slowStepTimeout });
		await expect(privateNetwork).toBeChecked();

		// A listed host goes through the proxy, and a host off the list is refused by name without being looked up
		const { runId, status } = await runUtil.runScripted(
			page.request,
			job.id,
			runUtil.bashThenFinish(
				`curl -s -m 20 ${service.url}; echo; curl -s -m 20 http://example.com/`,
				{
					status: 'success',
					summary: 'Probed both hosts'
				}
			)
		);
		expect(status).toBe('succeeded');

		// The first check waits for a cold load of the app, which fetches the session, the run and its events before the step shows
		await page.goto(`/runs/${runId}`);
		const output = bashOutput(page, 'curl -s');
		await expect(output).toContainText('hello from the host', { timeout: slowStepTimeout });
		await expect(output).toContainText("example.com is not on this job's allow-list");
		await expect(page.getByTestId('run-timeline')).toContainText('proxy example.com');
		expect(service.hits()).toBe(1);

		// Without a network there is nothing to list or allow, so both settings go away
		await page.goto(`/jobs/${job.id}/settings`);
		await pickOption(page, network, /^No network/);
		await expect(domains).toBeHidden();
		await expect(privateNetwork).toBeHidden();

		// Leaving the allow-list is a new network too, so the save waits for the engine again
		await saveForm(sandbox, { timeout: slowStepTimeout });
		const offline = await runUtil.getJob(page.request, job.id);
		expect(offline.network).toBe('none');
		expect(offline.spec.network).toBe('none');
	} finally {
		await service.close();
	}
});

test('A saved allow-list shows in the Allowed domains field as it was stored', async ({ page }) => {
	const job = await runUtil.createJob(page.request, 'Tidy job');
	await page.goto(`/jobs/${job.id}/settings`);
	const sandbox = card(page, 'Sandbox');
	const domains = sandbox.getByLabel('Allowed domains');

	// Mixed case, a trailing dot, a blank line and a repeat are all tidied away by the save
	await pickOption(page, sandbox.getByLabel('Network', { exact: true }), /^Allowed domains only/);
	await domains.fill('API.example.com.\n*.example.org\n\napi.example.com');
	await saveForm(sandbox);
	await expect(domains).toHaveValue('api.example.com\n*.example.org');
});

test('Out-of-range limits show an error next to each field of the Sandbox card', async ({
	page
}) => {
	const job = await runUtil.createJob(page.request, 'Limits job');
	await page.goto(`/jobs/${job.id}/settings`);
	const sandbox = card(page, 'Sandbox');

	// Every limit outside its bounds says why next to its field
	await sandbox.getByLabel('Timeout').fill('10');
	await sandbox.getByLabel('Max turns').fill('0');
	await sandbox.getByLabel('CPUs').fill('100');
	await sandbox.getByLabel('Memory').fill('32');
	await expectSaveRejected(sandbox, sandbox.getByText('Must be at least 30'));
	await expect(sandbox.getByText('Must be at least 1', { exact: true })).toBeVisible();
	await expect(sandbox.getByText('Must be at most 64')).toBeVisible();
	await expect(sandbox.getByText('Must be at least 64')).toBeVisible();
	await expect(sandbox.getByLabel('Timeout')).toHaveAttribute('aria-invalid', 'true');
});

test('Out-of-range limits are never stored, and saved limits and Run as root reach the next run', async ({
	page
}) => {
	// A job that already overrides the timeout, which clearing the field has to remove again
	const job = await runUtil.createJob(page.request, 'Limits job');
	const seeded = await page.request.patch(`/api/jobs/${job.id}`, {
		data: { limits: { timeoutSeconds: 600 } }
	});
	expect(seeded.ok()).toBeTruthy();

	// Every change of the job goes out as one PATCH, so the recorded ones show what the card tried to store
	const patches: unknown[] = [];
	page.on('request', (request) => {
		if (request.method() === 'PATCH' && request.url().endsWith(`/api/jobs/${job.id}`)) {
			patches.push(request.postDataJSON());
		}
	});

	await page.goto(`/jobs/${job.id}/settings`);
	const sandbox = card(page, 'Sandbox');
	const timeout = sandbox.getByLabel('Timeout');
	const maxTurns = sandbox.getByLabel('Max turns');
	const cpus = sandbox.getByLabel('CPUs');
	const memory = sandbox.getByLabel('Memory');
	await expect(timeout).toHaveValue('600');

	// Values outside the backend's bounds are refused in the browser, which keeps the edits to be fixed and sends nothing
	await timeout.fill('10');
	await maxTurns.fill('0');
	await cpus.fill('100');
	await memory.fill('32');
	await sandbox.getByRole('button', { name: 'Save', exact: true }).click();
	await expect(timeout).toHaveValue('10');
	await expect(maxTurns).toHaveValue('0');
	await expect(cpus).toHaveValue('100');
	await expect(memory).toHaveValue('32');

	// Valid limits and root are stored, and emptying the timeout removes its override so the workspace default applies again
	await timeout.clear();
	await maxTurns.fill('1');
	await cpus.fill('0.5');
	await memory.fill('256');
	await sandbox.getByRole('switch', { name: 'Run as root' }).click();
	await saveForm(sandbox);
	expect(patches).toEqual([
		expect.objectContaining({ limits: { maxTurns: 1, cpus: 0.5, memoryMb: 256 }, runAsRoot: true })
	]);
	const saved = await runUtil.getJob(page.request, job.id);
	expect(saved.limits).toEqual({ maxTurns: 1, cpus: 0.5, memoryMb: 256 });
	expect(saved.runAsRoot).toBe(true);

	// The run's commands run as root in a container with the saved CPU and memory limits
	// The cgroup v1 files are the fallback for hosts without cgroup v2, where the CPU quota comes without its period
	const command =
		'echo "uid=$(id -u)"; echo "cpu=$(cat /sys/fs/cgroup/cpu.max 2>/dev/null || cat /sys/fs/cgroup/cpu/cpu.cfs_quota_us)"; echo "memory=$(cat /sys/fs/cgroup/memory.max 2>/dev/null || cat /sys/fs/cgroup/memory/memory.limit_in_bytes)"';
	const { runId, status } = await runUtil.runScripted(
		page.request,
		job.id,
		runUtil.bashThenFinish(command, { status: 'success', summary: 'Should not be reached' })
	);

	// The one allowed turn is spent on the command, so the run fails before the agent can finish
	expect(status).toBe('failed');
	await page.goto(`/runs/${runId}`);
	const output = bashOutput(page, 'id -u');
	await expect(output).toContainText('uid=0');
	await expect(output).toContainText(/cpu=50000( 100000)?\n/);
	await expect(output).toContainText('memory=268435456');
	await expect(page.getByRole('alert').filter({ hasText: 'Failed' })).toContainText(
		'The run exceeded its limit of 1 turn'
	);
});

test('Changing the instruction rebuilds the spec unless the user opts out, and a failed compile saves nothing', async ({
	page
}) => {
	// A job whose settings are open
	const job = await runUtil.createJob(page.request, 'Rebuild job');
	await page.goto(`/jobs/${job.id}/settings`);

	// Edit the spec by hand first, which a rebuild replaces
	const spec = card(page, 'Spec');
	await spec.getByRole('button', { name: 'Add criterion' }).click();
	await spec.getByRole('textbox', { name: 'Success criterion 1' }).fill('Hand-written criterion');
	await saveForm(spec);

	// The rebuild is only offered once the instruction changed, and it is on by default
	const general = card(page, 'General');
	const instruction = general.getByLabel('Instruction');
	const rebuild = general.getByRole('switch', { name: 'Rebuild the spec' });
	await expect(rebuild).toBeHidden();
	await instruction.fill('Every weekday post the stale PRs of acme/api to #eng.');
	await expect(rebuild).toBeChecked();

	// A compile the model fails leaves the job as it was and keeps the edit for another try
	await runUtil.scriptModel(page.request, [{ text: 'this is not a spec' }]);
	await expectSaveRejected(
		general,
		toast(page, 'Failed to save the changes', 'The model could not compile the job.')
	);
	await expect(instruction).toHaveValue('Every weekday post the stale PRs of acme/api to #eng.');
	let saved = await runUtil.getJob(page.request, job.id);
	expect(saved.instruction).toBe('Do the thing.');
	expect(saved.spec.successCriteria).toEqual(['Hand-written criterion']);

	// A successful compile replaces the description in the Spec card, but not what the other settings own
	await runUtil.scriptModel(page.request, [{ text: JSON.stringify(compiledSpec) }]);
	await saveForm(general);
	await expect(spec.getByRole('textbox', { name: 'Success criterion 1' })).toHaveValue(
		'All open PRs are considered'
	);
	await expect(spec.getByRole('textbox', { name: 'Success criterion 2' })).toHaveValue(
		'Exactly one message is posted'
	);
	await expect(spec.getByLabel('Goal')).toHaveValue('Post a digest of stale pull requests');
	await expect(spec.getByRole('textbox', { name: 'Output 1 name' })).toHaveValue('count');
	await expect(rebuild).toBeHidden();
	saved = await runUtil.getJob(page.request, job.id);
	expect(saved.instruction).toBe('Every weekday post the stale PRs of acme/api to #eng.');
	expect(saved.spec.successCriteria).toEqual(compiledSpec.successCriteria);
	expect(saved.spec.sideEffects).toEqual(['Posts to Slack']);
	expect(saved.name).toBe('Rebuild job');
	expect(saved.cron).toBeNull();
	expect(saved.spec.schedule).toBeUndefined();

	// Opting out keeps the spec and never asks the model, so the scripted answer stays queued
	await instruction.fill('Every weekday post the stale PRs of acme/web to #eng.');
	await rebuild.click();
	await expect(rebuild).not.toBeChecked();
	await runUtil.scriptModel(page.request, [
		{ text: JSON.stringify({ ...compiledSpec, successCriteria: ['Should never be used'] }) }
	]);
	await saveForm(general);
	saved = await runUtil.getJob(page.request, job.id);
	expect(saved.instruction).toBe('Every weekday post the stale PRs of acme/web to #eng.');
	expect(saved.spec.successCriteria).toEqual(compiledSpec.successCriteria);
	expect(await runUtil.pendingAnswers(page.request)).toBe(1);
});

test("Another workspace can neither change a job's secret mappings nor map that workspace's secrets to its own job", async ({
	page,
	browser
}, testInfo) => {
	test.skip(!(await authUtil.workspacesEnabled(page)), 'workspaces are turned off');

	// A job of the default workspace with one of its secrets mapped
	const job = await runUtil.createJob(page.request, 'Private job');
	const secretId = await createSecret(page.request, 'private-token', 'private-secret-value');
	const mapped = await page.request.put(`/api/jobs/${job.id}/secrets`, {
		data: [{ secretId, envName: 'PRIVATE_TOKEN' }]
	});
	expect(mapped.ok()).toBeTruthy();

	// Bob signs in without an invite, which gives him a workspace of his own
	const bob = await authUtil.pageAs(browser, testInfo, accounts.bob);
	try {
		// Replacing the job's mappings answers as if the job didn't exist
		const replaced = await bob.request.put(`/api/jobs/${job.id}/secrets`, { data: [] });
		expect(replaced.status()).toBe(404);
		expect(((await replaced.json()) as { message: string }).message).toBe('Job not found');

		// His own job can't take the other workspace's secret, and is left without mappings
		const own = await runUtil.createJob(bob.request, 'Bob job');
		const stolen = await bob.request.put(`/api/jobs/${own.id}/secrets`, {
			data: [{ secretId, envName: 'STOLEN' }]
		});
		expect(stolen.status()).toBe(404);
		expect(((await stolen.json()) as { message: string }).message).toBe('Secret not found');
		expect(await (await bob.request.get(`/api/jobs/${own.id}/secrets`)).json()).toEqual([]);

		// His Secrets card offers nothing of the other workspace
		await bob.goto(`/jobs/${own.id}/settings`);
		await expect(card(bob, 'Secrets').getByText('No secrets yet.')).toBeVisible();
	} finally {
		await bob.context().close();
	}

	// The job keeps its mapping
	const secrets = await (await page.request.get(`/api/jobs/${job.id}/secrets`)).json();
	expect(secrets).toEqual([expect.objectContaining({ secretId, envName: 'PRIVATE_TOKEN' })]);
});

test('A failed request only disables the card it feeds, and Try again recovers it in place', async ({
	page
}) => {
	// A workspace secret, so a Secrets card that rendered would offer to add it
	await createSecret(page.request, 'github-token', 'ghp_e2e_value');
	const job = await runUtil.createJob(page.request, 'Flaky job');

	// The job's secrets and the workspace's models fail to load, while saving still goes through
	const failure = { status: 500, json: { code: 'internal_error', message: 'boom' } };
	const secretsRoute = '**/api/jobs/*/secrets';
	const modelsRoute = /\/api\/models(\?|$)/;
	await page.route(secretsRoute, (route) =>
		route.request().method() === 'GET' ? route.fulfill(failure) : route.continue()
	);
	await page.route(modelsRoute, (route) => route.fulfill(failure));
	await page.goto(`/jobs/${job.id}/settings`);

	// The Secrets card can't be edited without the stored mappings, so an alert stands in for it
	await expect(page.getByText("Couldn't load the job's secrets")).toBeVisible();
	await expect(card(page, 'Secrets')).toHaveCount(0);
	await expect(page.getByRole('button', { name: 'Add secret' })).toHaveCount(0);

	// The model picker is read-only, while the rest of the General card still saves
	const general = card(page, 'General');
	const model = general.getByLabel('Model', { exact: true });
	await expect(general.getByText("Couldn't load the models")).toBeVisible();
	await expect(model).toBeDisabled();
	await general.getByLabel('Name', { exact: true }).fill('Steady job');
	await saveForm(general);
	await expect(page.getByRole('heading', { level: 1, name: 'Steady job' })).toBeVisible();

	// An unsaved edit in another card, which a reload of the page would lose
	const timeout = card(page, 'Sandbox').getByLabel('Timeout');
	await timeout.fill('600');

	// Once the requests work again, Try again brings both cards back and keeps the edit
	await page.unroute(secretsRoute);
	await page.unroute(modelsRoute);
	await page.getByRole('button', { name: 'Try again' }).first().click();
	const secrets = card(page, 'Secrets');
	await expect(secrets.getByText('No secrets are passed to this job')).toBeVisible();
	await expect(secrets.getByRole('button', { name: 'Add secret' })).toBeVisible();
	await expect(model).toBeEnabled();
	await expect(model).toHaveText('Workspace default (Fake model)');
	await expect(page.getByRole('button', { name: 'Try again' })).toHaveCount(0);
	await expect(timeout).toHaveValue('600');
});
