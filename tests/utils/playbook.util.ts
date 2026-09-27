import { expect, type APIRequestContext, type Page } from '@playwright/test';
import { replaceCode } from './form.util';

// The workspace's default image, which a Dockerfile builds on in seconds since its layers are local
// Every build pulls the tag to pin its digest, so the suite's first build downloads the image when CI built it locally instead of pulling it
export const sandboxImage = 'ghcr.io/stonith404/umpteenth-sandbox:latest';

// A learning in the shape the playbook API requires, since it rejects learnings without hits and status
export type Learning = {
	id: string;
	kind: string;
	text: string;
	when?: string;
	hits: number;
	status: 'active' | 'retired';
};

// A toolkit script in the shape the playbook API requires
export type Script = {
	name: string;
	lang: string;
	description: string;
	args?: Record<string, string>;
	sideEffects: boolean;
	content: string;
	stats: { calls: number; failures: number };
};

// The content of one playbook version, leaving out the verify checks no spec uses
export type PlaybookContent = {
	learnings: Learning[];
	toolkit: Script[];
	dockerfile: string | null;
	setup: string | null;
	main: string | null;
};

// The fields of GET /api/jobs/{id}/playbook and of one version that specs check
export type Playbook = {
	version: number;
	author: string;
	summary: string | null;
	sourceRunId: string | null;
	ops?: { op: string; status: string }[];
	content: PlaybookContent;
};

// An active learning that no run has used yet
export function learning(id: string, kind: string, text: string, when?: string): Learning {
	return { id, kind, text, ...(when ? { when } : {}), hits: 0, status: 'active' };
}

// A whole playbook from the parts a spec sets, since the API requires every part
export function playbookContent(content: Partial<PlaybookContent>): PlaybookContent {
	return { learnings: [], toolkit: [], dockerfile: null, setup: null, main: null, ...content };
}

// A Dockerfile on the default image with one RUN line per command
export function dockerfile(...commands: string[]) {
	return `FROM ${sandboxImage}\n${commands.map((command) => `RUN ${command}\n`).join('')}`;
}

// The job's current playbook
export async function getPlaybook(request: APIRequestContext, jobId: string) {
	const response = await request.get(`/api/jobs/${jobId}/playbook`);
	expect(response.ok()).toBeTruthy();
	return (await response.json()) as Playbook;
}

// Writes a playbook version as a script or another tab would, filling in the parts the content leaves out, and returns its number
// A base version refuses the write once the playbook moved past it, as the page's own saves do
export async function putPlaybook(
	request: APIRequestContext,
	jobId: string,
	content: Partial<PlaybookContent>,
	{ summary = 'Seeded by the test', baseVersion }: { summary?: string; baseVersion?: number } = {}
) {
	const response = await request.put(`/api/jobs/${jobId}/playbook`, {
		data: { content: playbookContent(content), summary, baseVersion }
	});
	expect(response.ok()).toBeTruthy();
	return ((await response.json()) as Playbook).version;
}

// One image build of a job, as GET /api/jobs/{id}/images/{imageId} returns it
export type ImageBuild = {
	id: string;
	status: string;
	ref: string | null;
	error: string | null;
	dockerfile: string;
	log: string;
};

// The job's image builds with their logs, newest first
export async function listImages(request: APIRequestContext, jobId: string) {
	const response = await request.get(`/api/jobs/${jobId}/images`, {
		params: { sort: '-createdAt' }
	});
	expect(response.ok()).toBeTruthy();
	const { items } = (await response.json()) as { items: { id: string }[] };
	return Promise.all(
		items.map(async ({ id }) => {
			const image = await request.get(`/api/jobs/${jobId}/images/${id}`);
			expect(image.ok()).toBeTruthy();
			return (await image.json()) as ImageBuild;
		})
	);
}

// Waits until no build of the job is queued or building and returns the builds, newest first
// Builds share one queue with the next test's builds, so specs let theirs settle before they end
export async function waitForBuilds(request: APIRequestContext, jobId: string) {
	let images: ImageBuild[] = [];
	await expect
		.poll(
			async () => {
				images = await listImages(request, jobId);
				return images.every((image) => image.status === 'ready' || image.status === 'failed');
			},
			{ timeout: 30_000, intervals: [500] }
		)
		.toBe(true);
	return images;
}

// Replaces the text of the Environment tab's Dockerfile editor without saving it
export async function fillDockerfile(page: Page, text: string) {
	await replaceCode(page.getByRole('textbox', { name: 'Dockerfile', exact: true }), text);
}

// Replaces the editor's Dockerfile and saves it through Save & build
// Rebuild only shows while the editor holds the saved Dockerfile, so it appears once the page has loaded this save's version, unlike the toast, which an earlier save may still show
export async function saveDockerfile(page: Page, text: string) {
	await fillDockerfile(page, text);
	await page.getByRole('button', { name: 'Save & build' }).click();
	await expect(page.getByRole('button', { name: 'Rebuild' })).toBeVisible();
}
