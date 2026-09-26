import { tick } from 'svelte';

// Brings the first field that failed validation into view and focuses it, so the error shows where the user has to act instead of in a toast
// A field inside an inactive tab gets its tab selected first, at every level of nested tabs
export async function revealFirstInvalidField(root: ParentNode = document) {
	// The errors render on the next tick, after the validation that caused them
	await tick();
	const field = root.querySelector<HTMLElement>('[aria-invalid="true"], [data-slot="field-error"]');
	if (!field) return;

	// Selects every tab that hides the field, so it is visible before it is scrolled to
	for (
		let panel = field.closest<HTMLElement>('[role="tabpanel"]');
		panel;
		panel = panel.parentElement?.closest<HTMLElement>('[role="tabpanel"]') ?? null
	) {
		const value = panel.dataset.value;
		if (value === undefined) continue;
		panel
			.closest('[data-slot="tabs"]')
			?.querySelector<HTMLElement>(`[role="tab"][data-value="${CSS.escape(value)}"]`)
			?.click();
	}

	// Centering keeps the field clear of the sticky bars at the top and bottom of the window
	await tick();
	const reduceMotion = window.matchMedia('(prefers-reduced-motion: reduce)').matches;
	field.scrollIntoView({ block: 'center', behavior: reduceMotion ? 'auto' : 'smooth' });
	field.focus({ preventScroll: true });
}
