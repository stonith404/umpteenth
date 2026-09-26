<script lang="ts">
	import { isApiError } from '$lib/api/api-error';
	import type { Secret } from '$lib/api/types';
	import { Button } from '$lib/components/ui/button';
	import * as Dialog from '$lib/components/ui/dialog';
	import * as Field from '$lib/components/ui/field';
	import { Input } from '$lib/components/ui/input';
	import { Textarea } from '$lib/components/ui/textarea';
	import SecretService from '$lib/services/secret-service';
	import { apiErrorToast, getErrorMessage } from '$lib/utils/error-util';
	import { preventDefault } from '$lib/utils/event-util';
	import { tryCatch } from '$lib/utils/try-catch-util';

	let {
		open = $bindable(false),
		secret,
		onSaved
	}: {
		open?: boolean;
		// The secret whose value is replaced, or null to create one
		secret: Secret | null;
		onSaved: (name: string, created: boolean) => void;
	} = $props();

	// Mirrors the backend's validation of secret names
	const NAME_PATTERN = /^[A-Za-z0-9_.-]{1,100}$/;
	const MAX_VALUE_LENGTH = 65536;

	const secretService = new SecretService();

	let name = $state('');
	let value = $state('');
	let nameError = $state<string | null>(null);
	let valueError = $state<string | null>(null);
	let isLoading = $state(false);

	// Closing forgets what was typed, so the next open starts empty and the value doesn't linger in memory
	function reset() {
		name = '';
		value = '';
		nameError = null;
		valueError = null;
	}

	function validate() {
		nameError = null;
		valueError = null;
		if (!secret) {
			if (!name.trim()) nameError = 'Required';
			else if (!NAME_PATTERN.test(name.trim())) {
				nameError = 'Letters, digits, dots, dashes and underscores only';
			}
		}
		if (!value) valueError = 'Required';
		else if (value.length > MAX_VALUE_LENGTH) valueError = 'Must be at most 64 KiB';
		return !nameError && !valueError;
	}

	async function onSubmit() {
		if (!validate()) return;
		isLoading = true;
		const result = await tryCatch(
			secret ? secretService.update(secret.id, value) : secretService.create(name.trim(), value)
		);
		isLoading = false;
		if (result.error) {
			if (isApiError(result.error, 'already_in_use', 'validation_failed')) {
				nameError = getErrorMessage(result.error);
			}
			apiErrorToast(result.error, 'Failed to save the secret');
			return;
		}
		open = false;
		onSaved(secret?.name ?? name.trim(), !secret);
	}
</script>

<Dialog.Root bind:open onOpenChangeComplete={(isOpen) => !isOpen && reset()}>
	<Dialog.Content class="sm:max-w-md">
		<Dialog.Header>
			<Dialog.Title>{secret ? `Update ${secret.name}` : 'Create secret'}</Dialog.Title>
			<Dialog.Description>
				{secret
					? 'The new value replaces the old one for every job and MCP server that uses it.'
					: 'Values are encrypted at rest and can never be read back through the API.'}
			</Dialog.Description>
		</Dialog.Header>
		<form novalidate id="secret-form" onsubmit={preventDefault(onSubmit)}>
			<Field.Group>
				{#if !secret}
					<Field.Field data-invalid={!!nameError}>
						<Field.Label for="secret-name">Name</Field.Label>
						<Input
							id="secret-name"
							bind:value={name}
							mono
							placeholder="GITHUB_TOKEN"
							maxlength={100}
							aria-invalid={!!nameError}
						/>
						{#if nameError}
							<Field.Error>{nameError}</Field.Error>
						{:else}
							<Field.Description>
								Referenced as <code class="font-mono text-xs"
									>{`{{secret:${name.trim() || 'NAME'}}}`}</code
								> in MCP server settings.
							</Field.Description>
						{/if}
					</Field.Field>
				{/if}
				<Field.Field data-invalid={!!valueError}>
					<Field.Label for="secret-value">Value</Field.Label>
					<Textarea
						id="secret-value"
						bind:value
						class="max-h-60 min-h-20"
						mono="xs"
						autocomplete="off"
						spellcheck={false}
						aria-invalid={!!valueError}
					/>
					{#if valueError}<Field.Error>{valueError}</Field.Error>{/if}
				</Field.Field>
			</Field.Group>
		</form>
		<Dialog.Footer>
			<Button variant="outline" onclick={() => (open = false)}>Cancel</Button>
			<Button type="submit" form="secret-form" {isLoading}>
				{secret ? 'Update value' : 'Create secret'}
			</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
