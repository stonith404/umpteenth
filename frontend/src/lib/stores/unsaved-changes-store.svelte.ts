import { getErrorMessage } from '$lib/utils/error-util';
import { SvelteSet } from 'svelte/reactivity';

// A part of a page (e.g. a form card) whose edits the unsaved-changes bar saves or discards
export interface UnsavedSection {
	readonly dirty: boolean;
	validate?: () => boolean;
	save: () => Promise<void>;
	discard: () => void;
}

export type SaveResult = 'saved' | 'invalid' | 'failed';

const STATUS_DISPLAY_MS = 3000;

const sections = new SvelteSet<UnsavedSection>();
let saving = $state(false);
let status = $state<{ type: 'success' | 'error'; message: string } | null>(null);
let statusTimeout: ReturnType<typeof setTimeout> | undefined;

const dirtyCount = $derived([...sections].filter((section) => section.dirty).length);
const hasChanges = $derived(dirtyCount > 0);

function register(section: UnsavedSection) {
	sections.add(section);
}

function unregister(section: UnsavedSection) {
	sections.delete(section);
}

function clearStatus() {
	clearTimeout(statusTimeout);
	status = null;
}

function showStatus(type: 'success' | 'error', message: string) {
	clearTimeout(statusTimeout);
	status = { type, message };
	statusTimeout = setTimeout(() => (status = null), STATUS_DISPLAY_MS);
}

function errorMessage(e: unknown) {
	return getErrorMessage(e, e instanceof Error ? e.message : undefined);
}

async function saveAll(): Promise<SaveResult> {
	const dirtySections = [...sections].filter((section) => section.dirty);
	if (dirtySections.length === 0) return 'saved';

	// Every section is validated before anything is persisted, so a validation error in one section can't leave the page half-saved
	const allValid = dirtySections.map((section) => section.validate?.() ?? true).every(Boolean);
	if (!allValid) {
		showStatus('error', 'Please fix the errors before saving');
		return 'invalid';
	}

	// Sections are saved one after another rather than in parallel, because several sections of a page can target the same endpoint
	saving = true;
	let failure: { error: unknown } | undefined;
	for (const section of dirtySections) {
		try {
			await section.save();
		} catch (error) {
			failure ??= { error };
		}
	}
	saving = false;

	if (failure) {
		showStatus('error', errorMessage(failure.error));
		return 'failed';
	}
	showStatus('success', 'Changes saved');
	return 'saved';
}

function discardAll() {
	clearStatus();
	for (const section of sections) {
		if (section.dirty) section.discard();
	}
}

export default {
	get hasChanges() {
		return hasChanges;
	},
	get dirtyCount() {
		return dirtyCount;
	},
	get saving() {
		return saving;
	},
	get status() {
		return status;
	},
	register,
	unregister,
	saveAll,
	discardAll,
	clearStatus
};
