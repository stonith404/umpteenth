<script lang="ts">
	import { isApiError } from '$lib/api/api-error';
	import FormInput from '$lib/components/form/form-input.svelte';
	import { Button } from '$lib/components/ui/button';
	import * as Dialog from '$lib/components/ui/dialog';
	import * as Field from '$lib/components/ui/field';
	import WorkspaceService from '$lib/services/workspace-service';
	import { apiErrorToast } from '$lib/utils/error-util';
	import { preventDefault } from '$lib/utils/event-util';
	import { createForm } from '$lib/utils/form-util';
	import { tryCatch } from '$lib/utils/try-catch-util';
	import { enterWorkspace } from '$lib/utils/workspace-util';
	import { toast } from 'svelte-sonner';
	import { z } from 'zod/v4';

	let { open = $bindable(false) }: { open?: boolean } = $props();

	const workspaceService = new WorkspaceService();

	const form = createForm(z.object({ name: z.string().min(1, 'Required').max(100) }), { name: '' });
	const inputs = form.inputs;

	let isLoading = $state(false);

	async function onSubmit() {
		const data = form.validate();
		if (!data) return;

		// Creating the workspace also moves the session into it
		isLoading = true;
		const result = await tryCatch(workspaceService.create(data.name));
		isLoading = false;
		if (result.error) {
			if (isApiError(result.error, 'validation_failed')) form.setErrors(result.error.fields);
			apiErrorToast(result.error, 'Failed to create the workspace');
			return;
		}

		open = false;
		toast.success(`Created ${result.data.name}`);
		await enterWorkspace();
	}
</script>

<!-- Closing empties the form, so the next open starts fresh whatever was typed before -->
<Dialog.Root bind:open onOpenChangeComplete={(isOpen) => !isOpen && form.reset()}>
	<Dialog.Content class="sm:max-w-md">
		<Dialog.Header>
			<Dialog.Title>Create workspace</Dialog.Title>
			<Dialog.Description>
				A workspace has its own jobs, runs, secrets, providers and members. You'll be its owner.
			</Dialog.Description>
		</Dialog.Header>
		<form novalidate id="create-workspace-form" onsubmit={preventDefault(onSubmit)}>
			<Field.Group>
				<FormInput label="Name" placeholder="e.g. Platform team" bind:input={$inputs.name} />
			</Field.Group>
		</form>
		<Dialog.Footer>
			<Button variant="outline" onclick={() => (open = false)}>Cancel</Button>
			<Button type="submit" form="create-workspace-form" {isLoading}>Create workspace</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
