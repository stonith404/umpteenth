import { isApiError } from '$lib/api/api-error';
import unsavedChanges, { type UnsavedSection } from '$lib/stores/unsaved-changes-store.svelte';
import { deepCopy, deepEqual } from '$lib/utils/form-util';
import { untrack } from 'svelte';
import { fromStore, type Readable } from 'svelte/store';

// Registers a section of a page (e.g. a card) with the unsaved-changes bar
// The section is unregistered when the calling component is destroyed
export function trackUnsavedSection(
	dirty: () => boolean,
	save: () => Promise<void>,
	discard: () => void,
	options: { validate?: () => boolean } = {}
) {
	const isDirty = $derived.by(dirty);
	const section: UnsavedSection = {
		get dirty() {
			return isDirty;
		},
		validate: options.validate,
		save,
		discard
	};

	$effect(() => {
		unsavedChanges.register(section);
		return () => unsavedChanges.unregister(section);
	});
}

interface TrackedForm<T> {
	inputs: Readable<unknown>;
	isDirty: () => boolean;
	validate: () => T | null;
	commit: (data: T) => void;
	reset: () => void;
	clearErrors: () => void;
	setErrors: (fields: { field: string; message: string }[]) => void;
}

// Convenience wrapper around `trackUnsavedSection` for forms built with `createForm`
export function trackFormChanges<T>(
	getForm: () => TrackedForm<T>,
	onSave: (data: T) => Promise<unknown>,
	extra: { dirty?: () => boolean; discard?: () => void; enabled?: () => boolean } = {}
) {
	const inputs = $derived(fromStore(getForm().inputs));
	const isDirty = $derived.by(() => {
		// The inputs are deeply reactive, so edits are tracked through the reads in `isDirty()`
		// Reading the store on top of that picks up `commit()`, which moves the baseline without touching the inputs
		void inputs.current;
		if (extra.enabled && !extra.enabled()) return false;
		return getForm().isDirty() || (extra.dirty?.() ?? false);
	});

	// Validation errors only matter while there is something to save, otherwise a field edited back to its saved value would keep the error of a failed save
	$effect(() => {
		if (!isDirty) untrack(() => getForm().clearErrors());
	});

	trackUnsavedSection(
		() => isDirty,
		async () => {
			const form = getForm();

			// The bar validates every section before saving, so this only fails if the inputs changed in between
			const data = form.validate();
			if (!data) throw new Error('Please fix the errors before saving');

			// Field errors from the backend are shown next to their inputs as well as in the bar
			try {
				await onSave(data);
			} catch (e) {
				if (isApiError(e, 'validation_failed')) form.setErrors(e.fields);
				throw e;
			}
			form.commit(data);
		},
		() => {
			getForm().reset();
			extra.discard?.();
		},
		{ validate: () => getForm().validate() !== null }
	);
}

// Convenience wrapper around `trackUnsavedSection` for a value that is edited in place rather than through `createForm`
export function trackUnsavedValue<T>(
	read: () => T,
	write: (value: T) => void,
	save: (value: T) => Promise<unknown>
) {
	let saved = $state.raw(deepCopy(read()));

	// Reads again rather than reusing the saved value, so a caller that replaces the value with the server's response is taken into account
	const markSaved = () => {
		saved = deepCopy(read());
	};

	trackUnsavedSection(
		() => !deepEqual(read(), saved),
		async () => {
			await save(deepCopy(read()));
			markSaved();
		},
		() => write(deepCopy(saved))
	);

	return { markSaved };
}
