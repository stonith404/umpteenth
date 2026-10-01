<script lang="ts">
	import { isApiError } from '$lib/api/api-error';
	import type { Passkey } from '$lib/api/types';
	import FormInput from '$lib/components/form/form-input.svelte';
	import { Button } from '$lib/components/ui/button';
	import * as Dialog from '$lib/components/ui/dialog';
	import UserService from '$lib/services/user-service';
	import { apiErrorToast } from '$lib/utils/error-util';
	import { preventDefault } from '$lib/utils/event-util';
	import { createForm } from '$lib/utils/form-util';
	import { tryCatch } from '$lib/utils/try-catch-util';
	import { z } from 'zod/v4';

	// The dialog is open while it has a passkey to rename
	let {
		passkey = $bindable(),
		onRenamed
	}: {
		passkey: Passkey | null;
		onRenamed: () => void;
	} = $props();

	const userService = new UserService();

	const form = createForm(z.object({ name: z.string().trim().min(1).max(100) }), { name: '' });
	const inputs = form.inputs;
	let isLoading = $state(false);

	// Every opening starts from the passkey's current name
	$effect(() => {
		if (passkey) $inputs.name.value = passkey.name;
	});

	async function onSubmit() {
		const data = form.validate();
		if (!data || !passkey) return;

		isLoading = true;
		const result = await tryCatch(userService.renamePasskey(passkey.id, data.name));
		isLoading = false;
		if (result.error) {
			if (isApiError(result.error, 'validation_failed')) form.setErrors(result.error.fields);
			apiErrorToast(result.error, 'Failed to rename the passkey');
			return;
		}
		passkey = null;
		onRenamed();
	}
</script>

<Dialog.Root
	bind:open={() => !!passkey, (next) => !next && !isLoading && (passkey = null)}
	onOpenChangeComplete={(isOpen) => !isOpen && form.reset()}
>
	<Dialog.Content class="sm:max-w-md">
		<Dialog.Header>
			<Dialog.Title>Rename passkey</Dialog.Title>
			<Dialog.Description>A name that tells you where the passkey is kept.</Dialog.Description>
		</Dialog.Header>
		<form novalidate id="rename-passkey-form" onsubmit={preventDefault(onSubmit)}>
			<FormInput label="Name" placeholder="e.g. Work laptop" bind:input={$inputs.name} />
		</form>
		<Dialog.Footer>
			<Button variant="outline" disabled={isLoading} onclick={() => (passkey = null)}>Cancel</Button
			>
			<Button type="submit" form="rename-passkey-form" {isLoading}>Rename</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
