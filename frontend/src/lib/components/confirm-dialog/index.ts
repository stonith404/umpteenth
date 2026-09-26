import { writable } from 'svelte/store';
import ConfirmDialog from './confirm-dialog.svelte';

interface ConfirmDialogState {
	open: boolean;
	title: string;
	message: string;
	confirm: {
		label: string;
		destructive: boolean;
		action: () => void | Promise<void>;
	};
}

// The dialog is mounted once in the root layout and opened from anywhere through this store
export const confirmDialogStore = writable<ConfirmDialogState>({
	open: false,
	title: '',
	message: '',
	confirm: {
		label: 'Confirm',
		destructive: false,
		action: () => {}
	}
});

function openConfirmDialog({
	title,
	message,
	confirm
}: {
	title: string;
	message: string;
	confirm: {
		label?: string;
		destructive?: boolean;
		action: () => void | Promise<void>;
	};
}) {
	confirmDialogStore.set({
		open: true,
		title,
		message,
		confirm: {
			label: confirm.label ?? 'Confirm',
			destructive: confirm.destructive ?? false,
			action: confirm.action
		}
	});
}

export { ConfirmDialog, openConfirmDialog };
