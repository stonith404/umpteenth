<script lang="ts">
	import type { JobStateEntry } from '#lib/api/types.js';
	import * as AlertDialog from '#lib/components/ui/alert-dialog/index.js';
	import { Button } from '#lib/components/ui/button/index.js';

	let {
		entry = $bindable(),
		onDelete
	}: {
		// The entry to delete, the dialog is open while it is set
		entry: JobStateEntry | null;
		// Deletes the entry, the dialog shows a spinner until it settles
		onDelete: (entry: JobStateEntry) => Promise<void>;
	} = $props();

	// The shared confirm dialog only takes plain text, and this one shows the key in mono like the edit dialog's title does
	// The last entry stays rendered after closing, so the dialog animates out with its text instead of an empty title
	let shown = $state<JobStateEntry | null>(null);
	let deleting = $state(false);

	$effect.pre(() => {
		if (entry) shown = entry;
	});

	async function confirm() {
		if (!shown) return;
		deleting = true;
		try {
			await onDelete(shown);
		} finally {
			deleting = false;
			entry = null;
		}
	}
</script>

<AlertDialog.Root
	open={!!entry}
	onOpenChange={(open) => {
		if (!open) entry = null;
	}}
>
	<AlertDialog.Content>
		<AlertDialog.Header>
			<AlertDialog.Title class="min-w-0">
				Delete <code class="font-mono wrap-anywhere">{shown?.key}</code>
			</AlertDialog.Title>
			<AlertDialog.Description>
				Runs that rely on this entry start without it. This can't be undone.
			</AlertDialog.Description>
		</AlertDialog.Header>
		<AlertDialog.Footer>
			<AlertDialog.Cancel disabled={deleting}>Cancel</AlertDialog.Cancel>
			<AlertDialog.Action>
				{#snippet child()}
					<Button variant="destructive" isLoading={deleting} onclick={confirm}>Delete</Button>
				{/snippet}
			</AlertDialog.Action>
		</AlertDialog.Footer>
	</AlertDialog.Content>
</AlertDialog.Root>
