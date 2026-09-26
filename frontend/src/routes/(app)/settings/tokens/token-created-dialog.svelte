<script lang="ts">
	import type { ApiTokenCreated } from '$lib/api/types';
	import CopyButton from '$lib/components/copy-button.svelte';
	import * as Alert from '$lib/components/ui/alert';
	import { Button } from '$lib/components/ui/button';
	import * as Dialog from '$lib/components/ui/dialog';
	import * as Field from '$lib/components/ui/field';
	import * as InputGroup from '$lib/components/ui/input-group';
	import TriangleAlertIcon from '@lucide/svelte/icons/triangle-alert';

	// The created token is only known right after creation, closing the dialog forgets it for good
	let { created = $bindable() }: { created: ApiTokenCreated | null } = $props();

	function onOpenChange(open: boolean) {
		if (!open) created = null;
	}
</script>

<Dialog.Root open={!!created} {onOpenChange}>
	<Dialog.Content class="sm:max-w-lg" onOpenAutoFocus={(e) => e.preventDefault()}>
		<Dialog.Header>
			<Dialog.Title>API token created</Dialog.Title>
			<Dialog.Description>
				Use it as a bearer token: <code class="font-mono text-xs"
					>Authorization: Bearer &lt;token&gt;</code
				>
			</Dialog.Description>
		</Dialog.Header>
		{#if created}
			<Field.Field>
				<Field.Label for="created-token">{created.apiToken.name}</Field.Label>
				<InputGroup.Root>
					<InputGroup.Input
						id="created-token"
						mono="xs"
						value={created.token}
						readonly
						onfocus={(e) => e.currentTarget.select()}
					/>
					<InputGroup.Addon align="inline-end">
						<CopyButton value={created.token} label="Copy token" />
					</InputGroup.Addon>
				</InputGroup.Root>
			</Field.Field>
			<Alert.Root variant="warning">
				<TriangleAlertIcon />
				<Alert.Description>
					Copy the token now. For security reasons it won't be shown again.
				</Alert.Description>
			</Alert.Root>
		{/if}
		<Dialog.Footer>
			<Button onclick={() => onOpenChange(false)}>Done</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
