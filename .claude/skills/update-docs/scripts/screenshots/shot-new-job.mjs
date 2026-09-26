// new-job-review: the review step after a compile, with the spec cards, one warning and the save panel
import { open, settle, unionBox, out, content } from './cap.mjs';

const instruction =
	"Every weekday at 18:00 Berlin time, look at the GitHub Actions runs of acme/api from the last 24 hours and find tests that failed and then passed on a retry. Post the list to #ci in Slack with how often each test flaked this month, and keep the counts in state. Don't post if nothing flaked.";

const spec = {
	title: 'Flaky test report',
	goal: "A weekday list of the tests in acme/api's CI that failed and then passed on a retry, posted to #ci.",
	schedule: { cron: '0 18 * * 1-5', timezone: 'Europe/Berlin', human: 'Weekdays at 18:00' },
	successCriteria: [
		'Every GitHub Actions run of acme/api from the last 24 hours is checked',
		'Each flaky test is listed with how often it flaked this month',
		'One message goes to #ci, or none when nothing flaked'
	],
	inputs: [],
	outputs: [
		{ name: 'flaky_tests', type: 'integer', description: 'Tests that failed and then passed on a retry' },
		{ name: 'runs_checked', type: 'integer', description: 'CI runs looked at' }
	],
	mcp: [
		{ server: 'github', why: 'Read the GitHub Actions runs and their test results' },
		{ server: 'slack', why: 'Post the report to #ci' }
	],
	network: 'internet',
	dockerfile: null,
	sideEffects: ['Posts one message to #ci in Slack'],
	warnings: [
		"The description doesn't say what counts as a retry. The spec treats a re-run of a failed job in the same workflow run as a retry."
	]
};

for (const scheme of ['light', 'dark']) {
	const { browser, context, page } = await open(scheme);
	await page.goto('/jobs/new');
	await page.evaluate(() => sessionStorage.clear());
	await page.reload();
	await page.getByRole('textbox', { name: 'Describe the job' }).fill(instruction);
	const scripted = await context.request.post('/api/test/llm-script', {
		data: { reset: true, responses: [{ text: JSON.stringify(spec), usage: { input: 2900, output: 610 }, delayMs: 2500 }] }
	});
	if (!scripted.ok()) throw new Error('script failed');
	await page.getByRole('button', { name: 'Compile' }).click();
	await page.getByText('Check before saving').waitFor({ timeout: 60_000 });
	await settle(page, { wait: 1200 });
	const clip = await unionBox(page, [
		page.locator(content).locator('> *').first(),
		page.getByText('Check before saving'),
		page.locator('[data-slot="card"]', { hasText: 'Success criteria' }).first(),
		page.locator('[data-slot="card"]', { hasText: 'Ready to save?' }).last()
	]);
	const main = await page.locator(content).boundingBox();
	const full = { x: main.x, y: clip.y - 8, width: main.width, height: clip.height + 16 + 8 };
	await page.screenshot({ path: out('new-job-review', scheme), clip: full, fullPage: true });
	console.log(scheme, JSON.stringify(full));
	await browser.close();
}
