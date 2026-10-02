<script lang="ts">
	import CopyButton from '#lib/components/copy-button.svelte';
	import * as Alert from '#lib/components/ui/alert/index.js';
	import { Button } from '#lib/components/ui/button/index.js';
	import * as Dialog from '#lib/components/ui/dialog/index.js';
	import * as Field from '#lib/components/ui/field/index.js';
	import * as InputGroup from '#lib/components/ui/input-group/index.js';
	import TriangleAlertIcon from '@lucide/svelte/icons/triangle-alert';

	// The link is only known right after creation, closing the dialog forgets it for good
	let { url = $bindable() }: { url: string | null } = $props();

	function onOpenChange(open: boolean) {
		if (!open) url = null;
	}
</script>

<Dialog.Root open={!!url} {onOpenChange}>
	<Dialog.Content class="sm:max-w-lg" onOpenAutoFocus={(e) => e.preventDefault()}>
		<Dialog.Header>
			<Dialog.Title>Invite link created</Dialog.Title>
			<Dialog.Description>
				Send it to the person you're inviting. They join once they open it and sign in.
			</Dialog.Description>
		</Dialog.Header>
		{#if url}
			<Field.Field>
				<Field.Label for="invite-url">Invite link</Field.Label>
				<InputGroup.Root>
					<InputGroup.Input
						id="invite-url"
						mono="xs"
						value={url}
						readonly
						onfocus={(e) => e.currentTarget.select()}
					/>
					<InputGroup.Addon align="inline-end">
						<CopyButton value={url} label="Copy link" />
					</InputGroup.Addon>
				</InputGroup.Root>
			</Field.Field>
			<Alert.Root variant="warning">
				<TriangleAlertIcon />
				<Alert.Description>
					Copy the link now, it won't be shown again. It works once, for whoever opens it first.
				</Alert.Description>
			</Alert.Root>
		{/if}
		<Dialog.Footer>
			<Button onclick={() => onOpenChange(false)}>Done</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
