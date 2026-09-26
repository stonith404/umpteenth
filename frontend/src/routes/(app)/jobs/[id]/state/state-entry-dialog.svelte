<script lang="ts">
	import { isApiError } from '$lib/api/api-error';
	import type { JobStateEntry } from '$lib/api/types';
	import { Button } from '$lib/components/ui/button';
	import * as Dialog from '$lib/components/ui/dialog';
	import * as Field from '$lib/components/ui/field';
	import { Input } from '$lib/components/ui/input';
	import { Textarea } from '$lib/components/ui/textarea';
	import JobService from '$lib/services/job-service';
	import { apiErrorToast, getErrorMessage } from '$lib/utils/error-util';
	import { preventDefault } from '$lib/utils/event-util';
	import { tryCatch } from '$lib/utils/try-catch-util';
	import { untrack } from 'svelte';

	let {
		open = $bindable(false),
		jobId,
		entry,
		onSaved
	}: {
		open?: boolean;
		jobId: string;
		// The entry to edit, or null to add a new key
		entry: JobStateEntry | null;
		onSaved: (key: string) => void;
	} = $props();

	const MAX_KEY_LENGTH = 200;

	const jobService = new JobService();

	let key = $state('');
	let value = $state('');
	let keyError = $state<string | null>(null);
	let isLoading = $state(false);

	$effect(() => {
		if (!open) return;
		untrack(() => {
			key = entry?.key ?? '';
			value = entry?.value ?? '';
			keyError = null;
		});
	});

	function validate() {
		const trimmed = key.trim();
		if (!trimmed) keyError = 'Required';
		else if (trimmed.length > MAX_KEY_LENGTH)
			keyError = `Must be at most ${MAX_KEY_LENGTH} characters`;
		else keyError = null;
		return keyError === null;
	}

	async function onSubmit() {
		if (!validate()) return;
		isLoading = true;

		// Saving is an upsert, so adding a key that already exists would silently overwrite it
		if (!entry) {
			const exists = await tryCatch(jobService.stateKeyExists(jobId, key.trim()));
			if (exists.error) {
				apiErrorToast(exists.error, 'Failed to check whether the key exists');
				isLoading = false;
				return;
			}
			if (exists.data) {
				keyError = 'This key already exists';
				isLoading = false;
				return;
			}
		}

		const result = await tryCatch(jobService.putState(jobId, key.trim(), value));
		isLoading = false;
		if (result.error) {
			if (isApiError(result.error, 'invalid_field', 'validation_failed')) {
				keyError = getErrorMessage(result.error);
			}
			apiErrorToast(result.error, 'Failed to save the state entry');
			return;
		}
		open = false;
		onSaved(key.trim());
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content class="sm:max-w-lg">
		<Dialog.Header>
			<Dialog.Title>{entry ? 'Edit state entry' : 'Add state entry'}</Dialog.Title>
			<Dialog.Description>
				Runs read and write state with <code class="font-mono text-xs">ump state</code>, e.g. to
				remember what they already reported.
			</Dialog.Description>
		</Dialog.Header>
		<form id="state-entry-form" onsubmit={preventDefault(onSubmit)}>
			<Field.Group>
				<Field.Field data-invalid={!!keyError}>
					<Field.Label for="state-key" required>Key</Field.Label>
					<Input
						id="state-key"
						bind:value={key}
						class="font-mono"
						disabled={!!entry}
						maxlength={MAX_KEY_LENGTH}
						aria-invalid={!!keyError}
					/>
					{#if keyError}<Field.Error>{keyError}</Field.Error>{/if}
				</Field.Field>
				<Field.Field>
					<Field.Label for="state-value">Value</Field.Label>
					<Textarea id="state-value" bind:value class="max-h-80 min-h-32 font-mono text-xs" />
				</Field.Field>
			</Field.Group>
		</form>
		<Dialog.Footer>
			<Button variant="outline" onclick={() => (open = false)}>Cancel</Button>
			<Button type="submit" form="state-entry-form" {isLoading}>Save</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
