import type { Page } from '@playwright/test';
import { definition } from './ui.util';

// The run page's header, which holds the title, the status, the alert that says why a run ended, the actions and the run details
export function runHeader(page: Page) {
	return page.locator('header', { has: page.getByRole('heading', { level: 1 }) });
}

// The status badge of the run itself, ahead of any other badge in the header
export function runStatus(page: Page) {
	return runHeader(page).locator('[data-slot="status-badge"]').first();
}

// The value of one figure of the run's details, such as Turns or Cost
export function runDetail(page: Page, label: string) {
	return definition(page.getByLabel('Run details'), label);
}

// The steps of the run's timeline that contain the text anywhere
export function timelineStep(page: Page, text: string | RegExp) {
	return page.getByTestId('run-timeline').locator('[data-slot="timeline-step"]', { hasText: text });
}

// The output of the bash step whose command contains the fragment
export function bashOutput(page: Page, commandFragment: string) {
	return timelineStep(page, commandFragment).getByRole('region', { name: 'Output of bash' });
}
