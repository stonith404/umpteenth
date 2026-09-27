<script lang="ts">
	import { isApiError } from '$lib/api/api-error';
	import type { PlaybookContent } from '$lib/api/types';
	import CodeEditor from '$lib/components/code/code-editor.svelte';
	import { Button } from '$lib/components/ui/button';
	import * as Dialog from '$lib/components/ui/dialog';
	import * as Field from '$lib/components/ui/field';
	import { Input } from '$lib/components/ui/input';
	import { apiErrorToast, getErrorMessage } from '$lib/utils/error-util';
	import { preventDefault } from '$lib/utils/event-util';
	import { playbookToJson } from '$lib/utils/playbook-util';
	import { untrack } from 'svelte';

	let {
		open = $bindable(false),
		content,
		version,
		onSave
	}: {
		open?: boolean;
		content: PlaybookContent;
		// The version that content comes from
		version: number;
		// Gets the version the dialog opened with, so a version written while it was open makes the save fail instead of being overwritten
		onSave: (content: PlaybookContent, summary: string, baseVersion: number) => Promise<void>;
	} = $props();

	let json = $state('');
	let baseVersion = 0;
	let summary = $state('');
	let error = $state<string | null>(null);
	let isLoading = $state(false);

	// The page follows reflection while the dialog is open, so the JSON and its version are copied together only when it opens
	$effect(() => {
		if (!open) return;
		untrack(() => {
			json = playbookToJson(content);
			baseVersion = version;
			summary = '';
			error = null;
		});
	});

	// Only the shape is checked here, the backend validates the content itself
	function parse(): PlaybookContent | null {
		let value: unknown;
		try {
			value = JSON.parse(json);
		} catch (e) {
			error = `Not valid JSON: ${e instanceof Error ? e.message : 'parse error'}`;
			return null;
		}
		if (typeof value !== 'object' || value === null || Array.isArray(value)) {
			error = 'The playbook must be a JSON object';
			return null;
		}
		return value as PlaybookContent;
	}

	async function onSubmit() {
		error = null;
		const parsed = parse();
		if (!parsed) return;

		isLoading = true;
		try {
			await onSave(parsed, summary.trim(), baseVersion);
			open = false;
		} catch (e) {
			if (isApiError(e, 'validation_failed')) error = getErrorMessage(e);
			else apiErrorToast(e, 'Failed to save the playbook');
		} finally {
			isLoading = false;
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content class="sm:max-w-3xl">
		<Dialog.Header>
			<Dialog.Title>Edit playbook</Dialog.Title>
			<Dialog.Description>
				Saving creates a new version. A changed Dockerfile also starts an image build.
			</Dialog.Description>
		</Dialog.Header>
		<form
			novalidate
			id="playbook-json-form"
			class="flex min-w-0 flex-col gap-4"
			onsubmit={preventDefault(onSubmit)}
		>
			<Field.Field data-invalid={!!error}>
				<CodeEditor
					bind:value={json}
					language="json"
					label="Playbook JSON"
					invalid={!!error}
					class="h-dialog-editor"
				/>
				{#if error}<Field.Error>{error}</Field.Error>{/if}
			</Field.Field>
			<Field.Field>
				<Field.Label for="playbook-summary" optional>Summary</Field.Label>
				<Input
					id="playbook-summary"
					bind:value={summary}
					maxlength={500}
					placeholder="Edited by hand"
				/>
			</Field.Field>
		</form>
		<Dialog.Footer>
			<Button variant="outline" onclick={() => (open = false)}>Cancel</Button>
			<Button type="submit" form="playbook-json-form" {isLoading}>Save as new version</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
