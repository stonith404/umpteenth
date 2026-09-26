<script lang="ts">
	import * as AlertDialog from '$lib/components/ui/alert-dialog';
	import { Button } from '$lib/components/ui/button';
	import { confirmDialogStore } from '.';

	let isLoading = $state(false);

	// Async actions keep the dialog open with a spinner until they settle, so a failed delete doesn't look like it succeeded
	async function onConfirm() {
		isLoading = true;
		try {
			await $confirmDialogStore.confirm.action();
		} finally {
			isLoading = false;
			$confirmDialogStore.open = false;
		}
	}
</script>

<AlertDialog.Root bind:open={$confirmDialogStore.open}>
	<AlertDialog.Content>
		<AlertDialog.Header>
			<AlertDialog.Title>{$confirmDialogStore.title}</AlertDialog.Title>
			<AlertDialog.Description>{$confirmDialogStore.message}</AlertDialog.Description>
		</AlertDialog.Header>
		<AlertDialog.Footer>
			<AlertDialog.Cancel disabled={isLoading}>Cancel</AlertDialog.Cancel>
			<AlertDialog.Action>
				{#snippet child()}
					<Button
						variant={$confirmDialogStore.confirm.destructive ? 'destructive' : 'default'}
						{isLoading}
						onclick={onConfirm}
					>
						{$confirmDialogStore.confirm.label}
					</Button>
				{/snippet}
			</AlertDialog.Action>
		</AlertDialog.Footer>
	</AlertDialog.Content>
</AlertDialog.Root>
