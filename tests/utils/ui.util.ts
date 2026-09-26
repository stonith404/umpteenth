import type { Locator, Page } from '@playwright/test';

// A toast found by its title and, when given, the description that tells it apart from earlier toasts with the same title
export function toast(page: Page, title: string, description?: string | RegExp) {
	const toasts = page
		.getByRole('region', { name: /^Notifications/ })
		.getByRole('listitem')
		.filter({ hasText: title });
	return description === undefined ? toasts : toasts.filter({ hasText: description });
}

// A card found by its exact title, including the header actions that sit outside a settings card's form
export function card(page: Page, title: string) {
	return page
		.locator('[data-slot="card"]')
		.filter({ has: page.getByRole('heading', { name: title, exact: true }) });
}

// The value next to a term of a description list whose terms sit in rows of their own, such as a run detail or a structured output
export function definition(list: Locator, term: string) {
	return list
		.locator('div')
		.filter({ has: list.page().getByRole('term').getByText(term, { exact: true }) })
		.getByRole('definition');
}

// The facts under the page's title, such as a job's schedule, next run, last run and mode
export function pageHeaderMeta(page: Page) {
	return page.locator('[data-slot="page-header-meta"]');
}

// Opens the path and waits until the page listens to the workspace's live events, since an event sent before the stream is open never reaches the page
export async function gotoListening(page: Page, path: string) {
	const listening = page.waitForResponse((r) => new URL(r.url()).pathname === '/api/events');
	await page.goto(path);
	await listening;
}
