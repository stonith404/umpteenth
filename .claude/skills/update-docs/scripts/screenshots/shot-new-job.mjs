// new-job-questions: the questions a compile asks about a description that leaves the channel open, with a likely answer picked
// new-job-review: the review step after a compile of the Hacker News digest, with the spec cards and the save panel
import { open, settle, unionBox, out, content } from './cap.mjs';

// The docs' first-job walkthrough creates the Hacker News digest, so the review shows the instruction and spec setup.py gives jobs["hn"]
const hnInstruction =
	"Every weekday at 7:30 Berlin time, read the Hacker News front page and write a digest of its 30 stories as Markdown: the title with its link, points and comment count, sorted by points.\n\nMark stories about self-hosting, Go or databases with a ★ so they stand out. Save the digest as `digest.md` in the run's outputs and report how many stories matched.";

const hnSpec = {
	title: 'Hacker News digest',
	goal: 'A Markdown digest of the Hacker News front page every weekday morning, with the stories on our topics marked.',
	schedule: { cron: '30 7 * * 1-5', timezone: 'Europe/Berlin', human: 'Weekdays at 07:30' },
	successCriteria: [
		'digest.md lists the 30 front-page stories with title, link, points and comment count',
		'Stories about self-hosting, Go or databases carry a ★',
		'The outputs report how many stories the digest has and how many matched'
	],
	inputs: [],
	outputs: [
		{ name: 'stories', type: 'integer', description: 'Stories in the digest' },
		{ name: 'matches', type: 'integer', description: 'Stories about self-hosting, Go or databases' }
	],
	mcp: [],
	network: 'internet',
	dockerfile: null,
	sideEffects: []
};

// The questions capture keeps the flaky test report, whose description leaves the Slack channel open
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
	sideEffects: ['Posts one message to #ci in Slack']
};

// The description without the channel, which the compile step asks about before the review
const openInstruction = instruction.replace(' to #ci in Slack', ' to Slack');
const questions = [
	{ question: 'Which Slack channel should the report go to?', options: ['#ci', '#eng', '#dev-alerts'] }
];

// Answers the compile call with the spec itself, because shift.py turns the scripted model into an Anthropic provider without a usable key
async function answerCompile(page, body) {
	await page.route('**/api/jobs/compile', async (route) => {
		await new Promise((resolve) => setTimeout(resolve, 1500));
		await route.fulfill({ json: body });
	});
}

for (const scheme of ['light', 'dark']) {
	const { browser, page } = await open(scheme);
	await page.goto('/jobs/new');
	await page.evaluate(() => sessionStorage.clear());
	await page.reload();
	await page.getByRole('textbox', { name: 'Describe the job' }).fill(openInstruction);
	await answerCompile(page, { spec, questions });
	await page.getByRole('button', { name: 'Compile' }).click();
	const form = page.getByRole('form', { name: 'Questions about the job' });
	await form.waitFor({ timeout: 60_000 });
	await form.getByText('#ci', { exact: true }).click();
	await settle(page, { wait: 1200 });
	const clip = await unionBox(page, [page.locator(content).locator('> *').first(), form]);
	const main = await page.locator(content).boundingBox();
	const full = { x: main.x, y: clip.y - 8, width: main.width, height: clip.height + 16 + 8 };
	await page.screenshot({ path: out('new-job-questions', scheme), clip: full, fullPage: true });
	console.log(scheme, JSON.stringify(full));
	await browser.close();
}

for (const scheme of ['light', 'dark']) {
	const { browser, page } = await open(scheme);
	await page.goto('/jobs/new');
	await page.evaluate(() => sessionStorage.clear());
	await page.reload();
	await page.getByRole('textbox', { name: 'Describe the job' }).fill(hnInstruction);
	await answerCompile(page, { spec: hnSpec, questions: [] });
	await page.getByRole('button', { name: 'Compile' }).click();
	await page.getByText('Ready to save?').waitFor({ timeout: 60_000 });
	await settle(page, { wait: 1200 });
	const clip = await unionBox(page, [
		page.locator(content).locator('> *').first(),
		page.locator('[data-slot="card"]', { hasText: 'Success criteria' }).first(),
		page.locator('[data-slot="card"]', { hasText: 'Ready to save?' }).last()
	]);
	const main = await page.locator(content).boundingBox();
	const full = { x: main.x, y: clip.y - 8, width: main.width, height: clip.height + 16 + 8 };
	await page.screenshot({ path: out('new-job-review', scheme), clip: full, fullPage: true });
	console.log(scheme, JSON.stringify(full));
	await browser.close();
}
