import { expect, type Locator } from '@playwright/test';

// Saves one settings card through its own Save button and waits for the request to finish
// While the request runs, its spinner names the button "Loading Save", so the locator only finds Save again once the request is done
// A saved card's values become its baseline, which disables Save, while a failed save leaves it enabled
// A save whose request waits on slow backend work, such as a question to the container engine, passes a timeout for that wait
export async function saveForm(form: Locator, { timeout }: { timeout?: number } = {}) {
	const save = form.getByRole('button', { name: 'Save', exact: true });
	await save.click();
	await expect(save).toBeDisabled({ timeout });
}

// The inline error under a field of a dialog or form, found by the field's label since the same message can show under several fields
// The label is looked up inside each field, which a locator chained from the scope would not be
export function fieldError(scope: Locator, label: string) {
	return scope
		.locator('[data-slot="field"]', { has: scope.page().getByLabel(label) })
		.locator('[data-slot="field-error"]');
}

// Replaces the text of a CodeMirror editor without saving it
// Inserting the text in one go keeps CodeMirror from auto-indenting or closing brackets
export async function replaceCode(editor: Locator, text: string) {
	const page = editor.page();
	await editor.click();
	await page.keyboard.press('ControlOrMeta+a');
	await page.keyboard.press('Delete');
	if (text) await page.keyboard.insertText(text);
}
