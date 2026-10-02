<script lang="ts">
	import { invalidate } from '$app/navigation';
	import type { Job, JobIOField } from '#lib/api/types.js';
	import FormCard from '#lib/components/form/form-card.svelte';
	import IoFieldsEditor from '#lib/components/form/io-fields-editor.svelte';
	import StringListEditor from '#lib/components/form/string-list-editor.svelte';
	import * as Alert from '#lib/components/ui/alert/index.js';
	import * as Field from '#lib/components/ui/field/index.js';
	import { Textarea } from '#lib/components/ui/textarea/index.js';
	import JobService from '#lib/services/job-service.js';
	import { createForm } from '#lib/utils/form-util.js';
	import { cleanSpec } from '#lib/utils/job-util.js';
	import TriangleAlertIcon from '@lucide/svelte/icons/triangle-alert';
	import { untrack } from 'svelte';
	import { z } from 'zod/v4';

	let { job }: { job: Job } = $props();

	const jobService = new JobService();

	const ioField = z.object({ name: z.string(), type: z.string(), description: z.string() });
	const formSchema = z.object({
		goal: z.string().max(2000),
		successCriteria: z.array(z.string()),
		inputs: z.array(ioField),
		outputs: z.array(ioField),
		sideEffects: z.array(z.string())
	});

	// Descriptions the API leaves out start as empty text, so an untouched field doesn't count as an edit
	const withDescriptions = (fields: JobIOField[] | null | undefined) =>
		(fields ?? []).map((f) => ({ name: f.name, type: f.type, description: f.description ?? '' }));

	const form = createForm(formSchema, {
		goal: job.spec.goal,
		successCriteria: [...(job.spec.successCriteria ?? [])],
		inputs: withDescriptions(job.spec.inputs),
		outputs: withDescriptions(job.spec.outputs),
		sideEffects: [...(job.spec.sideEffects ?? [])]
	});
	const inputs = form.inputs;

	// These fields aren't wrapped in FormInput, so their errors are cleared on edit here, like FormInput does for its fields
	// The store also emits when an error is set, so only a value that differs from the last one seen counts as an edit
	for (const key of ['goal', 'inputs', 'outputs'] as const) {
		let seen = JSON.stringify($inputs[key].value);
		$effect(() => {
			const current = JSON.stringify($inputs[key].value);
			if (current === seen) return;
			seen = current;
			untrack(() => {
				if ($inputs[key].error) $inputs[key].error = null;
			});
		});
	}

	// A script only knows the outputs it was written for, so a scripted job is warned before its outputs change
	const outputsChanged = $derived.by(() => {
		const key = (fields: { name: string; type: string }[]) =>
			JSON.stringify(fields.filter((f) => f.name.trim()).map((f) => [f.name.trim(), f.type]));
		return key($inputs.outputs.value) !== key(withDescriptions(job.spec.outputs));
	});

	// Empty rows are dropped rather than refused, like on the new-job page
	async function save(values: z.infer<typeof formSchema>) {
		const clean = (items: string[]) => items.map((s) => s.trim()).filter(Boolean);
		const cleanFields = (fields: JobIOField[]) =>
			fields
				.filter((f) => f.name.trim())
				.map((f) => ({ ...f, name: f.name.trim(), description: f.description?.trim() }));

		await jobService.updateSpec(job.id, (spec) =>
			cleanSpec({
				...spec,
				goal: values.goal,
				successCriteria: clean(values.successCriteria),
				inputs: cleanFields(values.inputs),
				outputs: cleanFields(values.outputs),
				sideEffects: clean(values.sideEffects)
			})
		);
		await invalidate('app:job');
	}
</script>

<FormCard
	title="Spec"
	description="What the job has to achieve. The agent works towards it, and runs are judged by it."
	dirty={form.isDirty()}
	saving={form.saving}
	onsubmit={() => form.submit(save)}
>
	<Field.Group>
		<Field.Field data-invalid={!!$inputs.goal.error}>
			<Field.Label for="job-goal" optional>Goal</Field.Label>
			<Textarea
				id="job-goal"
				bind:value={$inputs.goal.value}
				placeholder="One sentence describing the outcome"
				aria-invalid={!!$inputs.goal.error}
			/>
			{#if $inputs.goal.error}<Field.Error>{$inputs.goal.error}</Field.Error>{/if}
		</Field.Field>

		<Field.Set>
			<Field.Legend variant="label">Success criteria</Field.Legend>
			<Field.Description
				>Checkable statements that decide whether a run succeeded.</Field.Description
			>
			<StringListEditor
				bind:items={$inputs.successCriteria.value}
				label="Success criterion"
				placeholder="e.g. Exactly one message is posted to #eng"
				addLabel="Add criterion"
			/>
		</Field.Set>

		<Field.Set data-invalid={!!$inputs.inputs.error}>
			<Field.Legend variant="label">Inputs</Field.Legend>
			<Field.Description>
				Values a run starts with, which arrive as <code class="font-mono text-xs"
					>/ump/input.json</code
				>.
			</Field.Description>
			<IoFieldsEditor bind:fields={$inputs.inputs.value} label="Input" addLabel="Add input" />
			{#if $inputs.inputs.error}<Field.Error>{$inputs.inputs.error}</Field.Error>{/if}
		</Field.Set>

		<Field.Set data-invalid={!!$inputs.outputs.error}>
			<Field.Legend variant="label">Outputs</Field.Legend>
			<Field.Description>Small values each run reports, tracked across runs.</Field.Description>
			<IoFieldsEditor bind:fields={$inputs.outputs.value} label="Output" addLabel="Add output" />
			{#if $inputs.outputs.error}<Field.Error>{$inputs.outputs.error}</Field.Error>{/if}
		</Field.Set>
		<!-- Outside the outputs' set, since Chromium leaves the container-query list above it at zero height when the banner appears inside the same set -->
		{#if job.nextMode === 'scripted' && outputsChanged}
			<Alert.Root variant="warning">
				<TriangleAlertIcon />
				<Alert.Description>
					This job runs as a script, which has to report every output. Runs fall back to the agent
					until the script reports a new one, and repeated fallbacks take the job back to Assisted.
				</Alert.Description>
			</Alert.Root>
		{/if}

		<Field.Set>
			<Field.Legend variant="label">Side effects</Field.Legend>
			<Field.Description>What the job changes outside its sandbox.</Field.Description>
			<StringListEditor
				bind:items={$inputs.sideEffects.value}
				label="Side effect"
				placeholder="e.g. Posts to Slack"
				addLabel="Add side effect"
			/>
		</Field.Set>
	</Field.Group>
</FormCard>
