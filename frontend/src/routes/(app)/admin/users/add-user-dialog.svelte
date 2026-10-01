<script lang="ts">
	import { isApiError } from '$lib/api/api-error';
	import type { AdminUserCreated } from '$lib/api/types';
	import FormInput from '$lib/components/form/form-input.svelte';
	import { Button } from '$lib/components/ui/button';
	import { Checkbox } from '$lib/components/ui/checkbox';
	import * as Dialog from '$lib/components/ui/dialog';
	import * as Field from '$lib/components/ui/field';
	import AdminService from '$lib/services/admin-service';
	import { apiErrorToast } from '$lib/utils/error-util';
	import { preventDefault } from '$lib/utils/event-util';
	import { createForm } from '$lib/utils/form-util';
	import { passkeyAccountSchema } from '$lib/utils/passkey-util';
	import { tryCatch } from '$lib/utils/try-catch-util';

	let {
		open = $bindable(false),
		onCreated
	}: {
		open?: boolean;
		onCreated: (created: AdminUserCreated) => void;
	} = $props();

	const adminService = new AdminService();

	const form = createForm(passkeyAccountSchema, { name: '', email: '' });
	const inputs = form.inputs;
	let isAdmin = $state(false);
	let isLoading = $state(false);

	async function onSubmit() {
		const data = form.validate();
		if (!data) return;

		isLoading = true;
		const result = await tryCatch(
			adminService.createUser({ name: data.name, email: data.email || undefined, isAdmin })
		);
		isLoading = false;
		if (result.error) {
			if (isApiError(result.error, 'validation_failed')) form.setErrors(result.error.fields);
			apiErrorToast(result.error, 'Failed to add the user');
			return;
		}
		open = false;
		onCreated(result.data);
	}

	function reset() {
		form.reset();
		isAdmin = false;
	}
</script>

<!-- Closing empties the form, so the next open starts fresh whatever was typed before -->
<!-- A pending save keeps the dialog open, so its completion can't close or report on whatever the dialog shows next -->
<Dialog.Root
	bind:open={() => open, (next) => (next || !isLoading) && (open = next)}
	onOpenChangeComplete={(isOpen) => !isOpen && reset()}
>
	<Dialog.Content class="sm:max-w-md">
		<Dialog.Header>
			<Dialog.Title>Add user</Dialog.Title>
			<Dialog.Description>
				Creates an account that signs in with a passkey. You get a sign-in link to send them, which
				lets them add their first passkey.
			</Dialog.Description>
		</Dialog.Header>
		<form novalidate id="add-user-form" onsubmit={preventDefault(onSubmit)}>
			<Field.Group>
				<FormInput label="Name" bind:input={$inputs.name} />
				<FormInput
					label="Email"
					type="email"
					placeholder="name@example.com"
					description="Counts as verified, so workspace invites sent to it reach the account."
					bind:input={$inputs.email}
				/>
				<Field.Field orientation="horizontal">
					<Checkbox id="add-user-admin" bind:checked={isAdmin} />
					<Field.Label for="add-user-admin" variant="choice">Instance admin</Field.Label>
				</Field.Field>
			</Field.Group>
		</form>
		<Dialog.Footer>
			<Button variant="outline" disabled={isLoading} onclick={() => (open = false)}>Cancel</Button>
			<Button type="submit" form="add-user-form" {isLoading}>Add user</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
