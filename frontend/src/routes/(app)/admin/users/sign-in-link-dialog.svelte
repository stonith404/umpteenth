<script lang="ts">
	import type { SignInLink } from '$lib/api/types';
	import CopyButton from '$lib/components/copy-button.svelte';
	import * as Alert from '$lib/components/ui/alert';
	import { Button } from '$lib/components/ui/button';
	import * as Dialog from '$lib/components/ui/dialog';
	import * as Field from '$lib/components/ui/field';
	import * as InputGroup from '$lib/components/ui/input-group';
	import { formatRelative } from '$lib/utils/format-util';
	import TriangleAlertIcon from '@lucide/svelte/icons/triangle-alert';

	// The link is only known right after creation, closing the dialog forgets it for good
	let {
		link = $bindable(),
		name
	}: {
		link: SignInLink | null;
		// Whose account the link signs in to
		name: string;
	} = $props();

	function onOpenChange(open: boolean) {
		if (!open) link = null;
	}
</script>

<Dialog.Root open={!!link} {onOpenChange}>
	<Dialog.Content class="sm:max-w-lg" onOpenAutoFocus={(e) => e.preventDefault()}>
		<Dialog.Header>
			<Dialog.Title>Sign-in link created</Dialog.Title>
			<Dialog.Description>
				Send it to {name}. It signs them in once, and then they add a passkey to sign in from then
				on.
			</Dialog.Description>
		</Dialog.Header>
		{#if link}
			<Field.Field>
				<Field.Label for="sign-in-link">Sign-in link</Field.Label>
				<InputGroup.Root>
					<InputGroup.Input
						id="sign-in-link"
						mono="xs"
						value={link.url}
						readonly
						onfocus={(e) => e.currentTarget.select()}
					/>
					<InputGroup.Addon align="inline-end">
						<CopyButton value={link.url} label="Copy link" />
					</InputGroup.Addon>
				</InputGroup.Root>
				<Field.Description>Expires {formatRelative(link.expiresAt)}.</Field.Description>
			</Field.Field>
			<Alert.Root variant="warning">
				<TriangleAlertIcon />
				<Alert.Description>
					Copy the link now, it won't be shown again. Whoever opens it is signed in as {name}, so
					only send it to them.
				</Alert.Description>
			</Alert.Root>
		{/if}
		<Dialog.Footer>
			<Button onclick={() => onOpenChange(false)}>Done</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
