import { expect, type Locator } from '@playwright/test';

// Saves one settings card through its own Save button and waits for the request to finish
// A saved card's values become its baseline, so Save ends up disabled again without its spinner, while a failed save leaves it enabled
export async function saveForm(form: Locator) {
	const save = form.getByRole('button', { name: 'Save', exact: true });
	await save.click();
	await expect(save).toBeDisabled();
	await expect(save.getByRole('status')).toHaveCount(0);

	// The button is disabled while the request runs too, so only its state after the spinner is gone tells success from failure
	await expect(save).toBeDisabled();
}
