import { expect, test, type APIRequestContext } from '@playwright/test';
import authUtil from '../utils/auth.util';
import { cleanupBackend } from '../utils/cleanup.util';
import { replaceCode } from '../utils/form.util';
import {
	getPlaybook,
	learning,
	playbookContent,
	putPlaybook,
	type Playbook,
	type PlaybookContent,
	type Script
} from '../utils/playbook.util';
import runUtil from '../utils/run.util';
import { card, toast } from '../utils/ui.util';

// Some tests run real sandboxes, so these specs get more time than the suite default
test.describe.configure({ timeout: 60_000 });

test.beforeEach(async ({ page }) => {
	await cleanupBackend(page.request);
	await authUtil.authenticate(page);
});

// A bash script whose ump: header names it and describes it, so the backend keeps the header's description
function bashScript(name: string, description: string, body: string): Script {
	return {
		name,
		lang: 'bash',
		description,
		sideEffects: false,
		content: `#!/usr/bin/env bash\n# ump:name        ${name}\n# ump:description ${description}\n# ump:side-effects none\n${body}`,
		stats: { calls: 0, failures: 0 }
	};
}

// One version of the playbook with its author, source run and reflection ops
async function getVersion(request: APIRequestContext, jobId: string, version: number) {
	const response = await request.get(`/api/jobs/${jobId}/playbook/versions/${version}`);
	expect(response.ok()).toBeTruthy();
	return (await response.json()) as Playbook;
}

test('Learnings edited, retired and restored by hand each become a version whose sheet shows the change, and retiring all of them sends the job back to Explore', async ({
	page
}) => {
	// Two active learnings make the job Assisted, and one has a kind of its own that the page shows in sentence case
	const job = await runUtil.createJob(page.request, 'Learning job');
	await putPlaybook(page.request, job.id, {
		learnings: [
			learning('L1', 'slow_source', 'Stories load slowly'),
			learning('L2', 'edge_case', 'Ask HN posts have no URL', 'listing stories')
		]
	});
	await page.goto(`/jobs/${job.id}/playbook`);
	await expect(page.getByRole('heading', { name: 'Version 1' })).toBeVisible();
	await expect(page.getByText('Runs as')).toContainText('Assisted');
	const l1 = page.getByRole('listitem', { name: 'Learning L1', exact: true });
	await expect(l1).toContainText('Slow source');
	await expect(page.getByRole('listitem', { name: 'Learning L2', exact: true })).toContainText(
		'When listing stories'
	);

	// A learning needs its text, its kind menu keeps its own kind next to the usual ones, and saving the edit writes version 2
	await page.getByRole('button', { name: 'Edit learning L1' }).click();
	const dialog = page.getByRole('dialog', { name: 'Edit learning L1' });
	await dialog.getByLabel('Text').fill('');
	await expect(dialog.getByRole('button', { name: 'Save' })).toBeDisabled();
	await dialog.getByLabel('Text').fill('Stories load slowly after midnight');
	await dialog.getByLabel('When').fill('listing stories');
	await dialog.getByLabel('Kind').click();
	await expect(page.getByRole('option', { name: 'Slow source' })).toBeVisible();
	await page.getByRole('option', { name: 'Workaround' }).click();
	await dialog.getByRole('button', { name: 'Save' }).click();
	await expect(toast(page, 'Edited learning L1')).toBeVisible();
	await expect(dialog).toBeHidden();
	await expect(page.getByRole('heading', { name: 'Version 2' })).toBeVisible();
	await expect(l1).toContainText('Workaround');
	await expect(l1).toContainText('Stories load slowly after midnight');
	await expect(l1).toContainText('When listing stories');

	// Retiring moves a learning into the collapsed retired list
	await page.getByRole('button', { name: 'Retire learning L2' }).click();
	await expect(toast(page, 'Retired learning L2')).toBeVisible();
	await expect(page.getByRole('button', { name: '1 retired' })).toBeVisible();

	// Without an active learning the playbook holds no know-how, so the next run explores again
	await page.getByRole('button', { name: 'Retire learning L1' }).click();
	await expect(toast(page, 'Retired learning L1')).toBeVisible();
	await expect(page.getByText('No active learnings yet')).toBeVisible();
	await expect(page.getByText('Runs as')).toContainText('Explore');

	// Restoring one brings the job back to Assisted
	await page.getByRole('button', { name: '2 retired' }).click();
	await page.getByRole('button', { name: 'Restore learning L1' }).click();
	await expect(toast(page, 'Restored learning L1')).toBeVisible();
	await expect(page.getByText('Runs as')).toContainText('Assisted');
	await expect(page.getByRole('button', { name: '1 retired' })).toBeVisible();

	// Every step is a manual version in the history, and only the last one is current
	const history = page.getByRole('table', { name: 'Playbook versions' });
	for (const [v, summary] of [
		[2, 'Edited learning L1'],
		[3, 'Retired learning L2'],
		[4, 'Retired learning L1'],
		[5, 'Restored learning L1']
	] as const) {
		const row = history.getByRole('row', { name: new RegExp(`v${v}.*${summary}`) });
		await expect(row).toContainText('Manual edit');
		if (v === 5) await expect(row).toContainText('Current');
		else await expect(row).not.toContainText('Current');
	}
	const playbook = await getPlaybook(page.request, job.id);
	expect(playbook.version).toBe(5);
	expect(playbook.content.learnings).toEqual([
		{
			id: 'L1',
			kind: 'workaround',
			text: 'Stories load slowly after midnight',
			when: 'listing stories',
			hits: 0,
			status: 'active'
		},
		expect.objectContaining({ id: 'L2', text: 'Ask HN posts have no URL', status: 'retired' })
	]);

	// Clicking a row shows what its edit changed, and an older version offers a rollback
	await history
		.getByRole('row', { name: /v2.*Edited learning L1/ })
		.getByText('Edited learning L1')
		.click();
	const sheet = page.getByRole('dialog', { name: 'Version 2' });
	await expect(sheet).toContainText('Manual edit');
	await expect(sheet.getByText('Edited learning L1', { exact: true })).toBeVisible();
	await expect(sheet.getByRole('heading', { name: 'Learnings' })).toBeVisible();
	await expect(sheet.getByText('Removed: Stories load slowly — slow source')).toBeAttached();
	await expect(
		sheet.getByText('Added: Stories load slowly after midnight — workaround, when listing stories')
	).toBeAttached();
	await expect(sheet.getByRole('button', { name: 'Roll back to version 2' })).toBeVisible();
	await page.keyboard.press('Escape');
	await expect(sheet).toBeHidden();

	// The current version's changes open from the page header, without a rollback to itself
	await page.getByRole('button', { name: 'Show changes' }).click();
	const current = page.getByRole('dialog', { name: 'Version 5' });
	await expect(
		current.getByText(
			'Added: Stories load slowly after midnight — workaround, when listing stories'
		)
	).toBeAttached();
	await expect(current.getByRole('button', { name: /Roll back/ })).toHaveCount(0);
});

// A script whose header names it, describes it and declares an argument and external side effects
const storiesV2 = `#!/usr/bin/env bash
# ump:name        stories
# ump:description Print the top stories
# ump:args        {"count":"integer"}
# ump:side-effects external
echo "v2"`;

test("Saving a toolkit script by hand re-reads its ump: header and refuses a bad one, and a deleted script comes back through the version sheet's rollback", async ({
	page
}) => {
	// The script's tool definition starts out as its first header says
	const job = await runUtil.createJob(page.request, 'Toolkit job');
	await putPlaybook(page.request, job.id, {
		toolkit: [bashScript('stories', 'Print the stories', 'echo "v1"')]
	});
	await page.goto(`/jobs/${job.id}/playbook`);
	const toolkit = card(page, 'Toolkit');
	await expect(toolkit).toContainText('Bash');

	// A script row expands to its code and its actions
	await toolkit.getByText('Print the stories', { exact: true }).click();
	await expect(page.getByRole('textbox', { name: 'Script stories' })).toContainText('echo "v1"');
	await page.getByRole('button', { name: 'Edit script stories' }).click();
	const dialog = page.getByRole('dialog', { name: 'Edit script stories' });
	const editor = dialog.getByRole('textbox', { name: 'Edit script stories' });

	// A header the backend can't read is refused with the reason, and the dialog keeps the code
	await replaceCode(editor, storiesV2.replace('external', 'sometimes'));
	await dialog.getByRole('button', { name: 'Save' }).click();
	await expect(
		toast(
			page,
			'Failed to save script stories',
			'content.toolkit[0].content: ump:side-effects must be none or external, not "sometimes".'
		)
	).toBeVisible();
	await expect(editor).toContainText('# ump:side-effects sometimes');

	// A header naming another script is refused too, since the name decides the tool
	await replaceCode(editor, storiesV2.replace('name        stories', 'name other'));
	await dialog.getByRole('button', { name: 'Save' }).click();
	await expect(
		toast(
			page,
			'Failed to save script stories',
			'the ump:name header says "other" but the script is named "stories"'
		)
	).toBeVisible();
	await expect(dialog).toBeVisible();
	expect((await getPlaybook(page.request, job.id)).version).toBe(1);

	// An empty script can't be saved at all
	await replaceCode(editor, '');
	await expect(dialog.getByRole('button', { name: 'Save' })).toBeDisabled();

	// A valid header becomes the tool's description, arguments and side effects
	// The expanded row still shows the code, so the fields are matched exactly to tell them apart from its header lines
	await replaceCode(editor, storiesV2);
	await dialog.getByRole('button', { name: 'Save' }).click();
	await expect(toast(page, 'Saved script stories')).toBeVisible();
	await expect(dialog).toBeHidden();
	await expect(page.getByRole('heading', { name: 'Version 2' })).toBeVisible();
	await expect(toolkit.getByText('Print the top stories', { exact: true })).toBeVisible();
	await expect(toolkit.getByText('Side effects', { exact: true })).toBeVisible();
	await expect(toolkit.getByText('args {"count":"integer"}', { exact: true })).toBeVisible();
	expect((await getPlaybook(page.request, job.id)).content.toolkit).toEqual([
		expect.objectContaining({
			name: 'stories',
			lang: 'bash',
			description: 'Print the top stories',
			args: { count: 'integer' },
			sideEffects: true,
			content: storiesV2
		})
	]);

	// Deleting the script writes version 3 without it, after a confirmation
	await page.getByRole('button', { name: 'Delete script stories' }).click();
	const confirm = page.getByRole('alertdialog', { name: 'Delete script stories' });
	await confirm.getByRole('button', { name: 'Delete' }).click();
	await expect(toast(page, 'Deleted script stories')).toBeVisible();
	await expect(page.getByRole('heading', { name: 'Version 3' })).toBeVisible();
	await expect(toolkit).toContainText('No toolkit scripts yet');

	// Version 2's sheet shows the edit as a diff of the script
	const history = page.getByRole('table', { name: 'Playbook versions' });
	await history.getByText('Edited script stories').click();
	const sheet = page.getByRole('dialog', { name: 'Version 2' });
	await expect(sheet.getByRole('heading', { name: 'Script stories' })).toBeVisible();
	await expect(sheet.getByText('Removed: echo "v1"')).toBeAttached();
	await expect(sheet.getByText('Added: echo "v2"')).toBeAttached();

	// Rolling back from the sheet writes version 4 with version 2's script
	await sheet.getByRole('button', { name: 'Roll back to version 2' }).click();
	const rollback = page.getByRole('alertdialog', { name: 'Roll back to version 2' });
	await rollback.getByRole('button', { name: 'Roll back' }).click();
	await expect(toast(page, 'Rolled back to version 2')).toBeVisible();
	await expect(sheet).toBeHidden();
	await expect(page.getByRole('heading', { name: 'Version 4' })).toBeVisible();
	const row = history.getByRole('row', { name: /v4.*Rolled back to version 2/ });
	await expect(row).toContainText('Rollback');
	await expect(row).toContainText('Current');
	await expect(toolkit.getByText('Print the top stories', { exact: true })).toBeVisible();
	const restored = await getPlaybook(page.request, job.id);
	expect(restored).toMatchObject({
		version: 4,
		author: 'rollback',
		summary: 'Rolled back to version 2'
	});
	expect(restored.content.toolkit.map((s) => s.content)).toEqual([storiesV2]);
});

// A learning added by hand
const l1 = learning('L1', 'fact', 'The API allows 10 calls a minute');

// A Python script whose shebang and header give the language and description that its stored fields get wrong
const weather = `#!/usr/bin/env python3
# ump:name weather
# ump:description Print the weather
print('sunny')`;

// Playbook JSON as the dialog shows it, with the parts the content leaves out
function json(content: Partial<PlaybookContent>) {
	return JSON.stringify(playbookContent(content), null, 2);
}

test('Edit as JSON refuses an invalid playbook inline and writes no version', async ({ page }) => {
	const job = await runUtil.createJob(page.request, 'Empty job');
	await page.goto(`/jobs/${job.id}/playbook`);

	// The editor opens on the empty playbook's JSON
	await page.getByRole('button', { name: 'Edit as JSON' }).click();
	const dialog = page.getByRole('dialog', { name: 'Edit playbook' });
	const editor = dialog.getByRole('textbox', { name: 'Playbook JSON' });
	await expect(editor).toContainText('"learnings": []');
	const save = dialog.getByRole('button', { name: 'Save as new version' });
	const error = dialog.getByRole('alert');

	// The dialog checks the shape itself before it sends anything
	await replaceCode(editor, 'not json');
	await save.click();
	await expect(error).toHaveText(/^Not valid JSON: /);
	await replaceCode(editor, '[]');
	await save.click();
	await expect(error).toHaveText('The playbook must be a JSON object');

	// The backend's checks come back as inline errors that name the field, and write no version
	const script = (name: string) => bashScript(name, 'Print the stories', 'echo hi');
	const refused: [Partial<PlaybookContent>, string][] = [
		[
			{ learnings: [l1, { ...l1, text: 'Another learning' }] },
			'content.learnings[1].id: is already used by another learning'
		],
		[
			{ toolkit: [{ ...script('stories'), name: '../evil' }] },
			'content.toolkit[0].name: must start with a letter or digit and may only contain up to 64 letters, digits, dots, dashes and underscores'
		],
		[
			{ toolkit: [script('top.stories'), script('top_stories')] },
			'content.toolkit[1].name: is used by another toolkit script, or becomes the same tool name as one'
		]
	];
	for (const [content, message] of refused) {
		await replaceCode(editor, json(content));
		await save.click();
		await expect(error).toHaveText(message);
	}
	await expect(toast(page, 'Failed to save the playbook')).toHaveCount(0);
	expect((await getPlaybook(page.request, job.id)).version).toBe(0);
});

test("An empty playbook fills up through Edit as JSON, which takes a script's language and description from its code, and an unchanged save still writes a version with no changes", async ({
	page
}) => {
	const job = await runUtil.createJob(page.request, 'Empty job');
	await page.goto(`/jobs/${job.id}/playbook`);

	// A job that doesn't learn starts with an empty playbook that says how it fills up
	await expect(page.getByRole('heading', { name: 'Empty playbook' })).toBeVisible();
	await expect(
		page.getByText(
			'What the job learns from its runs shows up here, once learning is turned on in the settings.'
		)
	).toBeVisible();
	await expect(page.getByRole('button', { name: 'Show changes' })).toHaveCount(0);

	// A valid playbook becomes version 1, with the script's language and description taken from its code
	await page.getByRole('button', { name: 'Edit as JSON' }).click();
	const dialog = page.getByRole('dialog', { name: 'Edit playbook' });
	const editor = dialog.getByRole('textbox', { name: 'Playbook JSON' });
	await expect(editor).toContainText('"learnings": []');
	await replaceCode(
		editor,
		json({
			learnings: [l1],
			toolkit: [
				{
					name: 'weather',
					lang: 'bash',
					description: '',
					sideEffects: false,
					content: weather,
					stats: { calls: 0, failures: 0 }
				}
			]
		})
	);
	await dialog.getByLabel('Summary').fill('Added by hand');
	const save = dialog.getByRole('button', { name: 'Save as new version' });
	await save.click();
	await expect(toast(page, 'Saved a new playbook version')).toBeVisible();
	await expect(dialog).toBeHidden();
	await expect(page.getByRole('heading', { name: 'Version 1' })).toBeVisible();
	const header = page.getByRole('region', { name: 'Version 1' });
	await expect(header).toContainText('Manual edit');
	await expect(header).toContainText('Added by hand');
	await expect(page.getByRole('listitem', { name: 'Learning L1', exact: true })).toContainText(
		'The API allows 10 calls a minute'
	);
	const toolkit = card(page, 'Toolkit');
	await expect(toolkit).toContainText('weather');
	await expect(toolkit).toContainText('Python');
	await expect(toolkit).toContainText('Print the weather');
	await expect(page.getByText('Runs as')).toContainText('Assisted');
	const saved = await getPlaybook(page.request, job.id);
	expect(saved).toMatchObject({ version: 1, author: 'user', summary: 'Added by hand' });
	expect(saved.content.toolkit[0]).toMatchObject({
		lang: 'python',
		description: 'Print the weather'
	});

	// Saving without a summary or a change still writes a version, which says so
	await page.getByRole('button', { name: 'Edit as JSON' }).click();
	await save.click();
	await expect(page.getByRole('heading', { name: 'Version 2' })).toBeVisible();
	const history = page.getByRole('table', { name: 'Playbook versions' });
	await history
		.getByRole('row', { name: /v2.*Edited by hand/ })
		.getByText('Edited by hand')
		.click();
	const sheet = page.getByRole('dialog', { name: 'Version 2' });
	await expect(sheet.getByText('No changes compared to the version before')).toBeVisible();
});

test('The version history gets a search once it holds more than ten versions', async ({ page }) => {
	// Ten versions written by a script, one of which stands out by its summary
	const job = await runUtil.createJob(page.request, 'Busy job');
	for (let v = 1; v <= 10; v++) {
		const summary = v === 4 ? 'Noted the weekend gap' : `Seeded version ${v}`;
		const fact = learning('L1', 'fact', `Fact number ${v}`);
		await putPlaybook(page.request, job.id, { learnings: [fact] }, { summary });
	}

	// Ten versions are short enough to scan, so the history has no search yet
	await page.goto(`/jobs/${job.id}/playbook`);
	const history = page.getByRole('table', { name: 'Playbook versions' });
	await expect(history.getByRole('row', { name: /v10.*Seeded version 10/ })).toBeVisible();
	await expect(page.getByRole('searchbox', { name: 'Search versions' })).toHaveCount(0);

	// The eleventh version adds a search that filters the history on the server and keeps the term in the URL
	const fact = learning('L1', 'fact', 'Fact number 11');
	await putPlaybook(page.request, job.id, { learnings: [fact] }, { summary: 'Seeded version 11' });
	await page.reload();
	await expect(page.getByRole('heading', { name: 'Version 11' })).toBeVisible();
	await page.getByRole('searchbox', { name: 'Search versions' }).fill('weekend');
	await expect(page).toHaveURL(/versions_search=weekend/);
	const rows = history.locator('tbody tr');
	await expect(rows).toHaveCount(1);
	await expect(rows).toContainText('Noted the weekend gap');
});

test('Learnings and scripts edited by hand are what the next run finds in its sandbox', async ({
	page
}) => {
	// The playbook starts with two learnings and a script, as reflection could have left them
	const job = await runUtil.createJob(page.request, 'Story job');
	await putPlaybook(page.request, job.id, {
		learnings: [
			learning('L1', 'fact', 'Stories load slowly'),
			learning('L2', 'edge_case', 'Ask HN posts have no URL', 'listing stories')
		],
		toolkit: [bashScript('stories', 'Print the stories', 'echo "first draft"')]
	});
	await page.goto(`/jobs/${job.id}/playbook`);

	// Rewrite one learning, retire the other and change what the script prints
	await page.getByRole('button', { name: 'Edit learning L1' }).click();
	const learningDialog = page.getByRole('dialog', { name: 'Edit learning L1' });
	await learningDialog.getByLabel('Text').fill('Stories load slowly after midnight');
	await learningDialog.getByRole('button', { name: 'Save' }).click();
	await expect(page.getByRole('heading', { name: 'Version 2' })).toBeVisible();
	await page.getByRole('button', { name: 'Retire learning L2' }).click();
	await expect(page.getByRole('heading', { name: 'Version 3' })).toBeVisible();
	await page.getByText('Print the stories', { exact: true }).click();
	await page.getByRole('button', { name: 'Edit script stories' }).click();
	const script = page.getByRole('dialog', { name: 'Edit script stories' });
	await replaceCode(
		script.getByRole('textbox', { name: 'Edit script stories' }),
		bashScript('stories', 'Print the stories', 'echo "edited by hand"').content
	);
	await script.getByRole('button', { name: 'Save' }).click();
	await expect(page.getByRole('heading', { name: 'Version 4' })).toBeVisible();

	// The run reads the rendered playbook and calls the script as a tool
	const { runId, status } = await runUtil.runScripted(page.request, job.id, [
		{ toolCalls: [{ name: 'bash', args: { command: 'cat /ump/PLAYBOOK.md' } }] },
		{ toolCalls: [{ name: 'toolkit__stories', args: {} }] },
		runUtil.finish('Used the playbook')
	]);
	expect(status).toBe('succeeded');
	expect((await runUtil.getRun(page.request, runId)).mode).toBe('assisted');
	const results = await runUtil.toolResults(page.request, runId);
	const playbookFile = results.find((r) => r.name === 'bash');
	expect(playbookFile?.content).toContain('- [L1] Stories load slowly after midnight');
	expect(playbookFile?.content).toContain('- toolkit__stories: Print the stories');
	expect(playbookFile?.content).not.toContain('Ask HN posts have no URL');
	const tool = results.find((r) => r.name === 'toolkit__stories');
	expect(tool?.content).toContain('edited by hand');
	expect(tool?.content).not.toContain('first draft');
	expect(tool?.content).toContain('exit code: 0');

	// The call counts for the script even though the job doesn't learn, while only reflection counts a learning as used
	// The stats are written after the run's final status, behind the job actor's notification, so they get the run polls' budget
	await expect
		.poll(async () => (await getPlaybook(page.request, job.id)).content.toolkit[0].stats, {
			timeout: 15_000,
			intervals: [250]
		})
		.toEqual({ calls: 1, failures: 0 });
	await page.reload();
	await expect(card(page, 'Toolkit')).toContainText('1 call · 0 failed');
	await expect(page.getByRole('listitem', { name: 'Learning L1', exact: true })).toContainText(
		'Not used yet'
	);
});

test("A reflection version's sheet explains the held proposal and links back to the run it came from", async ({
	page
}) => {
	const job = await runUtil.createJob(page.request, 'Reflecting job', { selfImprove: true });

	// Reflection adds a learning and holds back a Dockerfile on an unknown base image
	const { runId } = await runUtil.runScripted(page.request, job.id, [
		runUtil.finish('Explored'),
		runUtil.reflection('Promoted the basics', [
			{ op: 'add_learning', kind: 'fact', text: 'Stories load slowly' },
			{ op: 'set_dockerfile', content: 'FROM docker.io/somebody/image\nRUN true' }
		])
	]);
	expect((await runUtil.waitForReflection(page.request, runId)).reflectionVersion).toBe(1);
	const version = await getVersion(page.request, job.id, 1);
	expect(version).toMatchObject({ author: 'reflection', sourceRunId: runId });
	expect(version.ops?.map((op) => `${op.op}:${op.status}`)).toEqual([
		'add_learning:applied',
		'set_dockerfile:held'
	]);
	expect(version.content.dockerfile).toBeNull();

	// The page header says what reflection learned and links to the run, as does the learning it added
	await page.goto(`/jobs/${job.id}/playbook`);
	const header = page.getByRole('region', { name: 'Version 1' });
	await expect(header).toContainText('Reflection');
	await expect(header).toContainText('What reflection learned from the run');
	await expect(header).toContainText('Promoted the basics');
	await expect(header.getByRole('link', { name: 'Source run' })).toHaveAttribute(
		'href',
		`/runs/${runId}`
	);
	await expect(
		page
			.getByRole('listitem', { name: 'Learning L1', exact: true })
			.getByRole('link', { name: 'Source run' })
	).toHaveAttribute('href', `/runs/${runId}`);

	// The sheet counts the outcomes and explains each proposal, including the held one's content
	await page.getByRole('button', { name: 'Show changes' }).click();
	const sheet = page.getByRole('dialog', { name: 'Version 1' });
	await expect(sheet.getByText('1 applied · 1 held for review', { exact: true })).toBeVisible();
	const added = sheet.getByRole('article').filter({ hasText: 'Add learning L1' });
	await expect(added).toContainText('Applied');
	await expect(added).toContainText('It saves the next run work.');
	await expect(added.getByText('Added: Stories load slowly — fact')).toBeAttached();
	const held = sheet.getByRole('article').filter({ hasText: 'Set the Dockerfile' });
	await expect(held).toContainText('Held for review');
	await expect(held).toContainText(
		'Held back for review. If it is safe, apply it by hand in the playbook.'
	);
	await held.getByRole('button', { name: 'Show the proposed content' }).click();
	await expect(
		held.getByRole('textbox', { name: 'Proposed content of Set the Dockerfile' })
	).toContainText('FROM docker.io/somebody/image');
	await page.keyboard.press('Escape');
	await expect(sheet).toBeHidden();

	// The version's menu opens the source run
	await page.getByRole('button', { name: 'Actions for version 1' }).click();
	await page.getByRole('menuitem', { name: 'Open source run' }).click();
	await expect(page).toHaveURL(`/runs/${runId}`);
});
