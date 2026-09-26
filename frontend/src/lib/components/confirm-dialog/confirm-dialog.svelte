<script lang="ts" module>
	type ConfirmOptions = {
		title: string;
		message: string;
		// Text the user has to type before the confirm button enables, e.g. a workspace's name before deleting it
		confirmText?: string;
		confirm: {
			label?: string;
			destructive?: boolean;
			action: () => void | Promise<void>;
		};
	};

	// The dialog is mounted once in the root layout and opened from anywhere through `openConfirmDialog`
	const dialog = $state({
		open: false,
		title: '',
		message: '',
		confirmText: '',
		typed: '',
		label: 'Confirm',
		destructive: false,
		action: (): void | Promise<void> => {}
	});

	export function openConfirmDialog({ title, message, confirmText, confirm }: ConfirmOptions) {
		Object.assign(dialog, {
			open: true,
			title,
			message,
			confirmText: confirmText ?? '',
			typed: '',
			label: confirm.label ?? 'Confirm',
			destructive: confirm.destructive ?? false,
			action: confirm.action
		});
	}
</script>

<script lang="ts">
	import * as AlertDialog from '$lib/components/ui/alert-dialog';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import { preventDefault } from '$lib/utils/event-util';

	const inputId = $props.id();

	let isLoading = $state(false);

	// Without a confirm text there is nothing to type, otherwise it has to match, ignoring surrounding spaces
	const matches = $derived(
		!dialog.confirmText || dialog.typed.trim() === dialog.confirmText.trim()
	);

	// Async actions keep the dialog open with a spinner until they settle, so a failed delete doesn't look like it succeeded
	async function onConfirm() {
		if (!matches) return;
		isLoading = true;
		try {
			await dialog.action();
		} finally {
			isLoading = false;
			dialog.open = false;
		}
	}
</script>

<AlertDialog.Root bind:open={dialog.open}>
	<AlertDialog.Content>
		<AlertDialog.Header>
			<AlertDialog.Title>{dialog.title}</AlertDialog.Title>
			<AlertDialog.Description>{dialog.message}</AlertDialog.Description>
		</AlertDialog.Header>
		{#if dialog.confirmText}
			<!-- Like the Cloudflare dashboard, a destructive action on something big asks for its name, so it can't be confirmed by reflex -->
			<form class="flex flex-col gap-2" onsubmit={preventDefault(onConfirm)}>
				<Label for={inputId} sentence class="block">
					Type <span class="font-semibold">{dialog.confirmText}</span> to confirm
				</Label>
				<Input
					id={inputId}
					bind:value={dialog.typed}
					autocomplete="off"
					autocapitalize="off"
					spellcheck={false}
					disabled={isLoading}
				/>
			</form>
		{/if}
		<AlertDialog.Footer>
			<AlertDialog.Cancel disabled={isLoading}>Cancel</AlertDialog.Cancel>
			<AlertDialog.Action>
				{#snippet child()}
					<Button
						variant={dialog.destructive ? 'destructive' : 'default'}
						disabled={!matches}
						{isLoading}
						onclick={onConfirm}
					>
						{dialog.label}
					</Button>
				{/snippet}
			</AlertDialog.Action>
		</AlertDialog.Footer>
	</AlertDialog.Content>
</AlertDialog.Root>
