<script lang="ts">
	import { goto } from '$app/navigation';
	import type { Job, JobIOField } from '$lib/api/types';
	import CodeEditor from '$lib/components/code/code-editor.svelte';
	import { Button } from '$lib/components/ui/button';
	import * as Dialog from '$lib/components/ui/dialog';
	import * as Field from '$lib/components/ui/field';
	import { Textarea } from '$lib/components/ui/textarea';
	import BracesIcon from '@lucide/svelte/icons/braces';
	import JobService from '$lib/services/job-service';
	import { apiErrorToast } from '$lib/utils/error-util';
	import { preventDefault } from '$lib/utils/event-util';
	import { tryCatch } from '$lib/utils/try-catch-util';
	import { tick, untrack } from 'svelte';
	import { toast } from 'svelte-sonner';

	let {
		open = $bindable(false),
		job
	}: {
		open?: boolean;
		job: Pick<Job, 'id' | 'name'> & { spec?: Pick<Job['spec'], 'inputs'> };
	} = $props();

	const jobService = new JobService();

	// Only an open dialog renders its editor and only one dialog is open at a time, so a fixed ID stays unique on pages with several of them
	const INPUT_EDITOR_ID = 'run-now-input';

	let instructions = $state('');
	let input = $state('');
	let inputError = $state<string | null>(null);
	let isLoading = $state(false);
	// Jobs that declare no inputs hide the JSON editor behind a button, since most runs need nothing but a click
	let showInput = $state(false);

	const inputFields = $derived(job.spec?.inputs ?? []);

	// A fresh dialog starts from a template of the job's declared inputs, so users see what the job expects
	$effect(() => {
		if (!open) return;
		untrack(() => {
			instructions = '';
			inputError = null;
			input = inputFields.length > 0 ? inputTemplate(inputFields) : '';
			showInput = inputFields.length > 0;
		});
	});

	// The editor replaces the button that revealed it, so it takes the focus instead of leaving it nowhere
	async function revealInput() {
		showInput = true;
		await tick();
		document.getElementById(INPUT_EDITOR_ID)?.focus();
	}

	function inputTemplate(fields: JobIOField[]) {
		const example: Record<string, unknown> = {};
		for (const field of fields) {
			example[field.name] = exampleValue(field.type);
		}
		return JSON.stringify(example, null, 2);
	}

	function exampleValue(type: string) {
		switch (type) {
			case 'integer':
			case 'number':
				return 0;
			case 'boolean':
				return false;
			case 'object':
				return {};
			case 'array':
				return [];
			default:
				return '';
		}
	}

	// The input must be JSON, since it lands in the sandbox as /ump/input.json
	function parseInput(): { ok: true; value: unknown } | { ok: false } {
		if (!input.trim()) return { ok: true, value: undefined };
		try {
			return { ok: true, value: JSON.parse(input) };
		} catch (e) {
			inputError = `Not valid JSON: ${e instanceof Error ? e.message : 'parse error'}`;
			return { ok: false };
		}
	}

	async function onSubmit() {
		inputError = null;
		const parsed = parseInput();
		if (!parsed.ok) return;

		isLoading = true;
		const result = await tryCatch(
			jobService.runNow(job.id, {
				instructions: instructions.trim() || undefined,
				input: parsed.value
			})
		);
		isLoading = false;

		if (result.error) {
			apiErrorToast(result.error, 'Failed to start a run');
			return;
		}
		open = false;
		if (result.data.status === 'skipped') {
			toast.info('Run skipped', { description: 'Another run of this job is still active.' });
		} else {
			toast.success(`Started a run of "${job.name}"`);
		}
		await goto(`/runs/${result.data.runId}`);
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content class="sm:max-w-xl">
		<Dialog.Header>
			<Dialog.Title>Run now</Dialog.Title>
			<Dialog.Description>
				Starts a run of “{job.name}” right away. What you add here only applies to this run.
			</Dialog.Description>
		</Dialog.Header>
		<form novalidate id="run-now-form" onsubmit={preventDefault(onSubmit)}>
			<Field.Group>
				<Field.Field>
					<Field.Label for="run-instructions" optional>Extra instructions</Field.Label>
					<Textarea
						id="run-instructions"
						bind:value={instructions}
						placeholder="e.g. Only look at pull requests opened this week"
						class="min-h-20"
					/>
				</Field.Field>
				{#if showInput}
					<Field.Field data-invalid={!!inputError}>
						<Field.Label optional={inputFields.length === 0}>Input</Field.Label>
						<CodeEditor
							bind:value={input}
							id={INPUT_EDITOR_ID}
							language="json"
							label="Run input as JSON"
							placeholder={'{ "key": "value" }'}
							invalid={!!inputError}
							class="h-40"
						/>
						{#if inputError}
							<Field.Error>{inputError}</Field.Error>
						{:else}
							<Field.Description>
								JSON the run can read from <code class="font-mono text-xs">/ump/input.json</code>.
							</Field.Description>
						{/if}
					</Field.Field>
				{:else}
					<div>
						<Button variant="ghost" size="sm" class="-ml-2" onclick={revealInput}>
							<BracesIcon data-icon="inline-start" />
							Add input JSON
						</Button>
					</div>
				{/if}
			</Field.Group>
		</form>
		<Dialog.Footer>
			<Button variant="outline" onclick={() => (open = false)}>Cancel</Button>
			<Button type="submit" form="run-now-form" {isLoading}>Run now</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
