import { expect, test, type APIRequestContext } from '@playwright/test';
import authUtil from '../utils/auth.util';
import { cleanupBackend } from '../utils/cleanup.util';
import runUtil from '../utils/run.util';
import { pageHeaderMeta, toast } from '../utils/ui.util';

const { finish, reflection } = runUtil;

// Runs use real sandboxes, so these specs get more time than the suite default
test.describe.configure({ timeout: 120_000 });

test.beforeEach(async ({ page }) => {
	await cleanupBackend(page.request);
	await authUtil.authenticate(page);
});

// The job's open suggestions, as the Overview loads them
async function listSuggestions(request: APIRequestContext, jobId: string) {
	const response = await request.get(`/api/jobs/${jobId}/suggestions`);
	expect(response.ok()).toBeTruthy();
	return (await response.json()) as { key: string; kind: string }[];
}

const workflowScope = {
	op: 'suggest',
	kind: 'credentials',
	name: 'github_token_workflow_scope',
	text: 'GITHUB_TOKEN lacks the workflow scope the push needs.'
};

test('Reflection suggestions wait on the job overview until someone applies or dismisses them', async ({
	page
}) => {
	const job = await runUtil.createJob(page.request, 'Release notes', {
		instruction: 'Push the release notes.',
		selfImprove: true,
		limits: { memoryMb: 1024 }
	});

	// Reflection asks the owner for more memory and a token scope, which it can't change itself
	const first = await runUtil.runScripted(page.request, job.id, [
		finish('Pushed the release notes'),
		reflection('Needs the owner', [
			{
				op: 'suggest',
				kind: 'limits',
				text: 'The build ran out of memory at 1024 MB.',
				content: '{"memoryMb":2048}'
			},
			workflowScope
		])
	]);
	expect(first.status).toBe('succeeded');
	const learned = await runUtil.waitForReflection(page.request, first.runId);
	expect(learned.reflection).toBe('done');
	expect(learned.reflectionVersion).toBeNull();

	// The run's Learned tab shows the suggestions as held for the owner, with the limits a limits suggestion proposes
	await page.goto(`/runs/${first.runId}?tab=learned`);
	await expect(page.getByText('Suggest new limits')).toBeVisible();
	await expect(page.getByText('Suggest a credentials fix')).toBeVisible();
	await expect(page.getByText('2 suggested', { exact: true })).toBeVisible();
	await expect(
		page.getByText("Sent to the job's owner as a suggestion on the job's Overview.").first()
	).toBeVisible();
	await expect(page.getByText('Memory 2048 MB')).toBeVisible();

	// The job's header counts them, and the Overview lists them with the limits before and after
	await page.goto(`/jobs/${job.id}`);
	await expect(pageHeaderMeta(page).getByRole('link', { name: '2 suggestions' })).toBeVisible();
	const section = page.getByRole('region', { name: 'Suggestions' });
	await expect(section.getByText('1024 MB → 2048 MB')).toBeVisible();
	await expect(
		section.getByText('GITHUB_TOKEN lacks the workflow scope the push needs.')
	).toBeVisible();

	// Applying the limits suggestion changes the job's memory
	await section
		.getByRole('article', { name: 'New limits' })
		.getByRole('button', { name: 'Apply' })
		.click();
	await expect(toast(page, 'Applied the new limits')).toBeVisible();
	expect((await runUtil.getJob(page.request, job.id)).limits.memoryMb).toBe(2048);
	await expect(pageHeaderMeta(page).getByRole('link', { name: '1 suggestion' })).toBeVisible();

	// Dismissing the last one removes the section and the header's count
	await section
		.getByRole('article', { name: 'Credentials' })
		.getByRole('button', { name: 'Dismiss' })
		.click();
	await expect(toast(page, 'Dismissed the suggestion')).toBeVisible();
	await expect(section).toHaveCount(0);
	await expect(pageHeaderMeta(page).getByRole('link', { name: /suggestion/ })).toHaveCount(0);

	// A later run's reflection can't raise the dismissed subject again
	const second = await runUtil.runScripted(page.request, job.id, [
		finish('Pushed the release notes'),
		reflection('Still needs the scope', [workflowScope])
	]);
	expect(second.status).toBe('succeeded');
	expect((await runUtil.waitForReflection(page.request, second.runId)).reflection).toBe('done');
	expect(await listSuggestions(page.request, job.id)).toEqual([]);
	await page.goto(`/runs/${second.runId}?tab=learned`);
	await expect(page.getByText('Rejected', { exact: true })).toBeVisible();
	await expect(page.getByText(/the owner dismissed this suggestion lately/)).toBeVisible();
});
