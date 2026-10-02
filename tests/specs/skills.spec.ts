import { expect, test, type Page } from '@playwright/test';
import http from 'node:http';
import type { AddressInfo } from 'node:net';
import authUtil from '../utils/auth.util';
import { cleanupBackend } from '../utils/cleanup.util';
import { receiverHost } from '../utils/receiver.util';
import { timelineStep } from '../utils/run-view.util';
import runUtil from '../utils/run.util';
import {
	attachSkills,
	jobSkills,
	makeZip,
	repoTarball,
	skillZip,
	startFakeGitHub,
	uploadSkill,
	type Skill
} from '../utils/skill.util';

// One test starts a run in a real sandbox, so these specs get more time than the suite default
test.describe.configure({ timeout: 60_000 });

test.beforeEach(async ({ page }) => {
	await cleanupBackend(page.request);
	await authUtil.authenticate(page);
});

// The skill's row in the table on the skills page
function skillRow(page: Page, name: string) {
	return page
		.getByRole('table', { name: 'Skills' })
		.getByRole('row', { name: new RegExp(`\\b${name}\\b`) });
}

// Picks a zip in the upload dialog and submits it
async function submitZip(page: Page, title: string, fileName: string, zip: Buffer, submit: string) {
	const dialog = page.getByRole('dialog', { name: title });
	await dialog.getByLabel('Zip file').setInputFiles({ name: fileName, mimeType: '', buffer: zip });
	await dialog.getByRole('button', { name: submit }).click();
	return dialog;
}

test('Upload a skill and look through its files', async ({ page }) => {
	await page.goto('/skills');
	await expect(page.getByText('No skills yet')).toBeVisible();

	// A .skill file is a zip too, which browsers give no type
	await page.getByRole('button', { name: 'Add skill' }).click();
	await submitZip(
		page,
		'Add skill',
		'greeter.skill',
		skillZip('greeter', 'Greets people in their language'),
		'Add skill'
	);
	await expect(page.getByText('Added "greeter"')).toBeVisible();
	await expect(skillRow(page, 'greeter')).toContainText('Greets people in their language');
	await expect(skillRow(page, 'greeter')).toContainText('3 files');

	// The sheet lists the files, marks the script and renders SKILL.md without its frontmatter
	await skillRow(page, 'greeter').getByRole('button', { name: 'greeter', exact: true }).click();
	const sheet = page.getByRole('dialog', { name: 'greeter' });
	await expect(sheet.getByRole('button', { name: /scripts\/hello\.sh/ })).toContainText(
		'Executable'
	);
	await expect(sheet.getByRole('heading', { name: 'greeter', level: 1 })).toBeVisible();
	await expect(sheet).not.toContainText('description: Greets');

	// Another file opens in the sheet as code
	await sheet.getByRole('button', { name: /scripts\/hello\.sh/ }).click();
	await expect(sheet.getByRole('textbox', { name: 'scripts/hello.sh' })).toContainText(
		'echo "Hello, $1!"'
	);
});

test('Uploads that are not skills are refused next to the file', async ({ page }) => {
	await page.goto('/skills');
	await page.getByRole('button', { name: 'Add skill' }).click();

	// A zip without SKILL.md
	let dialog = await submitZip(
		page,
		'Add skill',
		'notes.zip',
		makeZip([{ path: 'README.md', content: 'hi' }]),
		'Add skill'
	);
	await expect(dialog).toContainText(
		'The zip must contain a SKILL.md at its root or inside a single top-level folder'
	);

	// A SKILL.md without a description
	dialog = await submitZip(
		page,
		'Add skill',
		'bad.zip',
		makeZip([{ path: 'SKILL.md', content: '---\nname: bad\n---\n' }]),
		'Add skill'
	);
	await expect(dialog).toContainText('The zip must set description in the SKILL.md frontmatter');

	// Paths that leave the skill folder and oversized uploads never reach storage
	const slip = makeZip([
		{ path: 'SKILL.md', content: '---\nname: slip\ndescription: Escapes\n---\n' },
		{ path: '../evil.sh', content: 'rm -rf /' }
	]);
	const refused = await page.request.post('/api/skills', {
		headers: { 'Content-Type': 'application/zip' },
		data: slip
	});
	expect(refused.status()).toBe(400);
	expect((await refused.json()).fields[0].code).toBe('unsafe_path');
	const huge = await page.request.post('/api/skills', {
		headers: { 'Content-Type': 'application/zip' },
		data: Buffer.alloc(9 << 20)
	});
	expect(huge.status()).toBe(413);
	expect((await (await page.request.get('/api/skills')).json()).total).toBe(0);
});

// Serves one skill zip at /greeter.skill, whose description a test can change, and 404 for anything else
async function serveSkill(description: string) {
	const served = { description };
	const server = http.createServer((req, res) => {
		if (req.url !== '/greeter.skill') {
			res.writeHead(404).end();
			return;
		}
		res
			.writeHead(200, { 'Content-Type': 'application/octet-stream' })
			.end(skillZip('greeter', served.description));
	});
	await new Promise<void>((resolve) => server.listen(0, resolve));
	const base = `http://${receiverHost}:${(server.address() as AddressInfo).port}`;
	return { served, base, close: () => server.close() };
}

test('Add a skill from a link and update it from there', async ({ page }) => {
	const fake = await serveSkill('Greets people');
	try {
		await page.goto('/skills');
		await page.getByRole('button', { name: 'Add skill' }).click();
		const dialog = page.getByRole('dialog', { name: 'Add skill' });
		await dialog.getByRole('tab', { name: 'Link' }).click();

		// A link the server doesn't know is refused next to the field
		await dialog.getByLabel('Link').fill(`${fake.base}/missing.skill`);
		await dialog.getByRole('button', { name: 'Add skill' }).click();
		await expect(dialog).toContainText('The link answered with HTTP 404');

		// A working link adds the skill and keeps the link
		const link = `${fake.base}/greeter.skill`;
		await dialog.getByLabel('Link').fill(link);
		await dialog.getByRole('button', { name: 'Add skill' }).click();
		await expect(page.getByText('Added "greeter"')).toBeVisible();
		await skillRow(page, 'greeter').getByRole('button', { name: 'greeter', exact: true }).click();
		const sheet = page.getByRole('dialog', { name: 'greeter' });
		await expect(sheet.getByRole('link', { name: link })).toBeVisible();

		// The link serves a new version, which the sheet takes from there
		fake.served.description = 'Greets people politely';
		await sheet.getByRole('button', { name: 'Update from link' }).click();
		await expect(page.getByText('Updated "greeter"')).toBeVisible();
		await expect(sheet).toContainText('Greets people politely');
		await sheet.getByRole('button', { name: 'Update from link' }).click();
		await expect(page.getByText('"greeter" is up to date')).toBeVisible();

		// Replacing a linked skill starts from its link
		await sheet.getByRole('button', { name: 'Replace' }).click();
		const replace = page.getByRole('dialog', { name: 'Replace greeter' });
		await expect(replace.getByRole('tab', { name: 'Link' })).toHaveAttribute(
			'aria-selected',
			'true'
		);
		await expect(replace.getByLabel('Link')).toHaveValue(link);
	} finally {
		fake.close();
	}
});

test('Pick skills from a GitHub repository that holds several', async ({ page }) => {
	const skillMd = (name: string, description: string) =>
		`---\nname: ${name}\ndescription: ${description}\n---\n# ${name}\n`;
	const github = await startFakeGitHub(
		page.request,
		'acme',
		'skills',
		repoTarball([
			{ path: 'README.md', content: 'Our skills' },
			{ path: 'skills/pdf/SKILL.md', content: skillMd('pdf', 'Fills PDF forms') },
			{ path: 'skills/pdf/scripts/fill.py', content: 'print(1)\n', mode: 0o755 },
			{ path: 'skills/xlsx/SKILL.md', content: skillMd('xlsx', 'Edits spreadsheets') },
			{ path: 'skills/docx/SKILL.md', content: skillMd('docx', 'Edits Word files') },
			{ path: 'skills/broken/SKILL.md', content: '---\nname: broken\n---\n' }
		])
	);
	try {
		// The workspace already has one of them
		await uploadSkill(page.request, skillZip('docx', 'Edits Word files'));

		await page.goto('/skills');
		await page.getByRole('button', { name: 'Add skill' }).click();
		const dialog = page.getByRole('dialog', { name: 'Add skill' });
		await dialog.getByRole('tab', { name: 'Link' }).click();
		await dialog.getByLabel('Link').fill('https://github.com/acme/skills');
		await dialog.getByRole('button', { name: 'Add skill' }).click();

		// The repository's skills show up to pick from, the one the workspace has and the broken one disabled
		await expect(dialog).toContainText('The folder holds 4 skills');
		await expect(dialog.getByRole('checkbox', { name: /^docx/ })).toBeDisabled();
		await expect(dialog.getByRole('checkbox', { name: /^docx/ })).toHaveAccessibleName(/Added/);
		await expect(dialog.getByRole('checkbox', { name: /^broken/ })).toBeDisabled();
		await expect(dialog).toContainText('It must set description in the SKILL.md frontmatter');

		// Nothing picked yet
		await dialog.getByRole('button', { name: 'Add skill' }).click();
		await expect(dialog).toContainText('Pick at least one skill');

		// Both skills that can be added come from one pick
		await dialog.getByRole('button', { name: 'Select all' }).click();
		await expect(dialog.getByRole('checkbox', { name: /^pdf/ })).toBeChecked();
		await dialog.getByRole('button', { name: 'Add 2 skills' }).click();
		await expect(page.getByText('Added 2 skills')).toBeVisible();
		await expect(skillRow(page, 'pdf')).toContainText('2 files');
		await expect(skillRow(page, 'xlsx')).toBeVisible();

		// Each keeps the link to its own folder
		await skillRow(page, 'pdf').getByRole('button', { name: 'pdf', exact: true }).click();
		await expect(
			page
				.getByRole('dialog', { name: 'pdf' })
				.getByRole('link', { name: 'https://github.com/acme/skills/tree/HEAD/skills/pdf' })
		).toBeVisible();
	} finally {
		github.close();
	}
});

test('Replacing a skill keeps its name', async ({ page }) => {
	const skill = await uploadSkill(page.request, skillZip('greeter', 'Greets people'));
	await page.goto('/skills');

	// A zip of another skill can't replace this one
	await skillRow(page, 'greeter').getByRole('button', { name: 'Actions for greeter' }).click();
	await page.getByRole('menuitem', { name: 'Replace' }).click();
	const dialog = await submitZip(
		page,
		'Replace greeter',
		'other.zip',
		skillZip('other', 'Something else'),
		'Replace'
	);
	await expect(dialog).toContainText('has the name "other" instead of "greeter"');

	// A new version of the same skill replaces it
	await submitZip(
		page,
		'Replace greeter',
		'greeter.zip',
		skillZip('greeter', 'Greets people politely'),
		'Replace'
	);
	await expect(page.getByText('Replaced "greeter"')).toBeVisible();
	await expect(skillRow(page, 'greeter')).toContainText('Greets people politely');
	const after = (await (await page.request.get(`/api/skills/${skill.id}`)).json()) as Skill;
	expect(after.contentHash).not.toBe(skill.contentHash);
});

test("A run gets its job's skills read-only", async ({ page }) => {
	await uploadSkill(page.request, skillZip('greeter', 'Greets people in their language'));
	const job = await runUtil.createJob(page.request, 'Greeter');

	// Attach the skill in the job's settings
	await page.goto(`/jobs/${job.id}/settings`);
	const card = page.getByRole('form', { name: 'Skills' });
	await expect(card).toContainText('No skills attached');
	await card.getByRole('button', { name: 'Attach skill' }).click();
	await page.getByRole('option', { name: /greeter/ }).click();
	await card.getByRole('button', { name: 'Save', exact: true }).click();
	await expect(card.getByRole('button', { name: 'Save', exact: true })).toBeDisabled();
	expect(await jobSkills(page.request, job.id)).toEqual(['greeter']);

	// The agent reads the skill, runs its script and can't change it
	await runUtil.scriptModel(
		page.request,
		runUtil.bashThenFinish(
			'cat /ump/skills/greeter/SKILL.md; /ump/skills/greeter/scripts/hello.sh World; touch /ump/skills/greeter/x || echo read-only',
			{ status: 'success', summary: 'Greeted' }
		)
	);
	const runId = await runUtil.startRun(page.request, job.id);
	expect(await runUtil.waitForRun(page.request, runId)).toBe('succeeded');
	const [bash] = await runUtil.toolResults(page.request, runId);
	expect(bash.content).toContain('description: Greets people in their language');
	expect(bash.content).toContain('Hello, World!');
	expect(bash.content).toContain('read-only');

	// The timeline names the skills the run got
	await page.goto(`/runs/${runId}`);
	await expect(timelineStep(page, 'Added the skill greeter')).toBeVisible();
});

test('Deleting a skill detaches it from its jobs', async ({ page }) => {
	const skill = await uploadSkill(page.request, skillZip('greeter', 'Greets people'));
	const job = await runUtil.createJob(page.request, 'Greeter');
	await attachSkills(page.request, job.id, [skill.id]);

	// The confirmation says how many jobs lose the skill
	await page.goto('/skills');
	await expect(skillRow(page, 'greeter')).toContainText('1 job');
	await skillRow(page, 'greeter').getByRole('button', { name: 'Actions for greeter' }).click();
	await page.getByRole('menuitem', { name: 'Delete' }).click();
	const confirm = page.getByRole('alertdialog');
	await expect(confirm).toContainText('1 job uses this skill and will run without it.');
	await confirm.getByRole('button', { name: 'Delete' }).click();
	await expect(page.getByText('Deleted "greeter"')).toBeVisible();
	await expect(page.getByText('No skills yet')).toBeVisible();

	expect((await page.request.get(`/api/skills/${skill.id}`)).status()).toBe(404);
	expect(await jobSkills(page.request, job.id)).toEqual([]);
});

test('Delete several skills at once', async ({ page }) => {
	const pdf = await uploadSkill(page.request, skillZip('pdf', 'Fills PDF forms'));
	const docx = await uploadSkill(page.request, skillZip('docx', 'Edits Word files'));
	await uploadSkill(page.request, skillZip('xlsx', 'Edits spreadsheets'));
	const job = await runUtil.createJob(page.request, 'Reports');
	await attachSkills(page.request, job.id, [pdf.id]);

	// Two skills are picked, one of them used by a job
	await page.goto('/skills');
	await page.getByRole('checkbox', { name: 'Select pdf' }).check();
	await page.getByRole('checkbox', { name: 'Select docx' }).check();
	await page.getByRole('button', { name: 'Delete', exact: true }).click();
	const confirm = page.getByRole('alertdialog');
	await expect(confirm).toContainText('Delete 2 skills');
	await expect(confirm).toContainText('1 of them is used by jobs, which will run without it.');
	await confirm.getByRole('button', { name: 'Delete' }).click();

	// Only the third skill stays, and the job lost the one it used
	await expect(page.getByText('Deleted 2 skills')).toBeVisible();
	await expect(skillRow(page, 'xlsx')).toBeVisible();
	await expect(skillRow(page, 'pdf')).toBeHidden();
	await expect(skillRow(page, 'docx')).toBeHidden();
	expect((await page.request.get(`/api/skills/${docx.id}`)).status()).toBe(404);
	expect(await jobSkills(page.request, job.id)).toEqual([]);
});

test('A new job gets the skills the compile step suggests', async ({ page }) => {
	await uploadSkill(page.request, skillZip('greeter', 'Greets people in their language'));
	await uploadSkill(page.request, skillZip('pdf-forms', 'Fills PDF forms'));
	await runUtil.scriptModel(page.request, [
		{
			text: JSON.stringify({
				title: 'Say hello',
				goal: 'Greet Alice',
				schedule: null,
				successCriteria: [],
				inputs: [],
				outputs: [],
				mcp: [],
				skills: ['greeter', 'made-up'],
				network: 'none',
				dockerfile: null,
				sideEffects: [],
				questions: []
			})
		}
	]);

	await page.goto('/jobs/new');
	await page
		.getByRole('textbox', { name: 'Describe the job' })
		.fill('Say hello to Alice in German.');
	await page.getByRole('button', { name: 'Compile' }).click();

	// The suggested skill is attached and marked, and the other one waits in the menu
	const attached = page.getByRole('list', { name: 'Attached skills' });
	await expect(attached.getByRole('listitem')).toHaveCount(1, { timeout: 15_000 });
	await expect(attached).toContainText('greeter');
	await expect(attached).toContainText('Suggested');

	// The menu searches names and descriptions, so a word from the description finds the skill
	await page.getByRole('button', { name: 'Attach skill' }).click();
	await page.getByPlaceholder('Search skills').fill('forms');
	await expect(page.getByRole('option')).toHaveCount(1);
	await page.getByRole('option', { name: /pdf-forms/ }).click();
	await expect(attached.getByRole('listitem')).toHaveCount(2);
	await expect(page.getByRole('button', { name: 'Attach skill' })).toBeHidden();

	await page.getByRole('button', { name: 'Save', exact: true }).click();
	await expect(page).toHaveURL(/\/jobs\/[0-9a-f-]+$/);
	const jobId = page.url().split('/').pop()!;
	expect(await jobSkills(page.request, jobId)).toEqual(['greeter', 'pdf-forms']);
});
