<script lang="ts">
	import { isApiError } from '#lib/api/api-error.js';
	import type { ApiTokenCreated } from '#lib/api/types.js';
	import FormInput from '#lib/components/form/form-input.svelte';
	import { Button } from '#lib/components/ui/button/index.js';
	import * as Dialog from '#lib/components/ui/dialog/index.js';
	import * as Field from '#lib/components/ui/field/index.js';
	import ApiTokenService from '#lib/services/api-token-service.js';
	import { apiErrorToast } from '#lib/utils/error-util.js';
	import { preventDefault } from '#lib/utils/event-util.js';
	import { createForm } from '#lib/utils/form-util.js';
	import { tryCatch } from '#lib/utils/try-catch-util.js';
	import { z } from 'zod/v4';

	let {
		open = $bindable(false),
		onCreated
	}: {
		open?: boolean;
		onCreated: (created: ApiTokenCreated) => void;
	} = $props();

	const apiTokenService = new ApiTokenService();

	const formSchema = z.object({
		name: z.string().min(1, 'Required').max(100),
		expiresAt: z
			.date()
			.refine((d) => d.getTime() > Date.now(), 'Must be in the future')
			.optional()
	});

	const form = createForm(formSchema, { name: '', expiresAt: undefined });
	const inputs = form.inputs;

	let isLoading = $state(false);

	async function onSubmit() {
		const data = form.validate();
		if (!data) return;

		isLoading = true;
		const result = await tryCatch(
			apiTokenService.create({ name: data.name, expiresAt: data.expiresAt?.getTime() })
		);
		isLoading = false;

		if (result.error) {
			if (isApiError(result.error, 'validation_failed')) form.setErrors(result.error.fields);
			apiErrorToast(result.error, 'Failed to create the API token');
			return;
		}

		open = false;
		onCreated(result.data);
	}
</script>

<!-- Closing empties the form, so the next open starts fresh whatever was typed before -->
<!-- A pending save keeps the dialog open, so its completion can't close or report on whatever the dialog shows next -->
<Dialog.Root
	bind:open={() => open, (next) => (next || !isLoading) && (open = next)}
	onOpenChangeComplete={(isOpen) => !isOpen && form.reset()}
>
	<Dialog.Content class="sm:max-w-md">
		<Dialog.Header>
			<Dialog.Title>Create API token</Dialog.Title>
			<Dialog.Description>
				API tokens authenticate scripts and automation against the Umpteenth API.
			</Dialog.Description>
		</Dialog.Header>
		<form novalidate id="create-token-form" onsubmit={preventDefault(onSubmit)}>
			<Field.Group>
				<FormInput
					label="Name"
					description="Helps you recognize what uses the token."
					placeholder="e.g. CI pipeline"
					bind:input={$inputs.name}
				/>
				<FormInput
					label="Expires"
					description="Leave empty for a token that never expires."
					type="date"
					futureOnly
					bind:input={$inputs.expiresAt}
				/>
			</Field.Group>
		</form>
		<Dialog.Footer>
			<Button variant="outline" disabled={isLoading} onclick={() => (open = false)}>Cancel</Button>
			<Button type="submit" form="create-token-form" {isLoading}>Create API token</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
