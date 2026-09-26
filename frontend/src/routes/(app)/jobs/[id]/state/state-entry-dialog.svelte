<script lang="ts">
	import { isApiError } from '$lib/api/api-error';
	import type { JobStateEntry } from '$lib/api/types';
	import CodeEditor from '$lib/components/code/code-editor.svelte';
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
	// JSON values get the code editor with highlighting, decided once per opening so the field never swaps while typing
	let json = $state(false);

	$effect(() => {
		if (!open) return;
		untrack(() => {
			key = entry?.key ?? '';
			value = entry?.value ?? '';
			keyError = null;
			json = isJsonDocument(value);
		});
	});

	// Objects and arrays read better with highlighting, while plain numbers and strings stay in a text field
	function isJsonDocument(text: string) {
		const trimmed = text.trim();
		if (!trimmed.startsWith('{') && !trimmed.startsWith('[')) return false;
		try {
			JSON.parse(trimmed);
			return true;
		} catch {
			return false;
		}
	}

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
			<Dialog.Title>
				{#if entry}
					<!-- Keys can be up to 200 characters without a space, so a long one breaks anywhere instead of widening the dialog -->
					Edit <code class="font-mono [overflow-wrap:anywhere]">{entry.key}</code>
				{:else}
					Add state entry
				{/if}
			</Dialog.Title>
			<Dialog.Description>
				Runs read and write state with <code class="font-mono text-xs">ump state</code>, e.g. to
				remember what they already reported.
			</Dialog.Description>
		</Dialog.Header>
		<form id="state-entry-form" onsubmit={preventDefault(onSubmit)}>
			<Field.Group>
				<!-- An existing entry's key can't change, so editing shows it in the title instead of a disabled field -->
				{#if !entry}
					<Field.Field data-invalid={!!keyError}>
						<Field.Label for="state-key">Key</Field.Label>
						<Input
							id="state-key"
							bind:value={key}
							class="font-mono"
							maxlength={MAX_KEY_LENGTH}
							aria-invalid={!!keyError}
						/>
						{#if keyError}<Field.Error>{keyError}</Field.Error>{/if}
					</Field.Field>
				{/if}
				<Field.Field>
					{#if json}
						<Field.Label>Value</Field.Label>
						<CodeEditor
							bind:value
							language="json"
							label="Value"
							lineWrapping
							class="h-auto max-h-80 min-h-32"
						/>
					{:else}
						<Field.Label for="state-value">Value</Field.Label>
						<Textarea id="state-value" bind:value class="max-h-80 min-h-32 font-mono text-xs" />
					{/if}
				</Field.Field>
			</Field.Group>
		</form>
		<Dialog.Footer>
			<Button variant="outline" onclick={() => (open = false)}>Cancel</Button>
			<Button type="submit" form="state-entry-form" {isLoading}>Save</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
