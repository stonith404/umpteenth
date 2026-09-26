<script lang="ts">
	import { invalidate } from '$app/navigation';
	import type { Job, Model, WorkspaceSettings } from '$lib/api/types';
	import FormCard from '$lib/components/form/form-card.svelte';
	import FormInput from '$lib/components/form/form-input.svelte';
	import ModelSelect from '$lib/components/model-select.svelte';
	import * as Field from '$lib/components/ui/field';
	import { Switch } from '$lib/components/ui/switch';
	import { Textarea } from '$lib/components/ui/textarea';
	import JobService from '$lib/services/job-service';
	import { createForm } from '$lib/utils/form-util';
	import { cleanSpec } from '$lib/utils/job-util';
	import { z } from 'zod/v4';
	import LoadError from './load-error.svelte';

	let {
		job,
		models,
		modelsError = null,
		settings
	}: {
		job: Job;
		models: Model[];
		// Why the workspace's models could not be listed, which leaves the model picker read-only
		modelsError?: unknown;
		settings: WorkspaceSettings;
	} = $props();

	const jobService = new JobService();

	const form = createForm(
		z.object({
			name: z.string().min(1, 'Required').max(200),
			instruction: z.string().min(1, 'Required').max(20000),
			modelId: z.string()
		}),
		{ name: job.name, instruction: job.instruction, modelId: job.modelId ?? '' }
	);
	const inputs = form.inputs;

	// Without a model the backend can't compile, so a changed instruction leaves the spec as it is and there is nothing to choose
	const canCompile = $derived(!!(settings.utilityModelId || settings.agentModelId));
	const instructionChanged = $derived($inputs.instruction.value.trim() !== job.instruction.trim());

	// A spec edited by hand in the Spec card would be thrown away by a rebuild, so the user decides whether it happens
	let rebuildSpec = $state(true);

	// The spec repeats the name as its title, so it goes along with the new name
	// A rebuilt spec takes as long as a compile, so saving a changed instruction takes that long too
	async function save(values: { name: string; instruction: string; modelId: string }) {
		await jobService.update(job.id, {
			...values,
			spec: cleanSpec({ ...job.spec, title: values.name }),
			rebuildSpec
		});
		await invalidate('app:job');
		rebuildSpec = true;
	}

	const agentModel = $derived(models.find((m) => m.id === settings.agentModelId));
</script>

<FormCard
	title="General"
	description="What the job does and which model does it."
	dirty={form.isDirty()}
	saving={form.saving}
	onsubmit={() => form.submit(save)}
>
	<Field.Group>
		<FormInput label="Name" bind:input={$inputs.name} />
		<FormInput
			label="Instruction"
			labelFor="job-instruction"
			input={$inputs.instruction}
			description="The agent follows this text on every run."
		>
			<Textarea
				id="job-instruction"
				bind:value={$inputs.instruction.value}
				aria-invalid={!!$inputs.instruction.error}
				class="min-h-32"
			/>
		</FormInput>
		<!-- Only asked once the instruction changed, since saving anything else never touches the spec -->
		{#if canCompile && instructionChanged}
			<Field.Field orientation="horizontal">
				<Switch id="job-rebuild-spec" bind:checked={rebuildSpec} />
				<Field.Content>
					<Field.Label for="job-rebuild-spec">Rebuild the spec</Field.Label>
					<Field.Description>
						Compiles the goal, success criteria, inputs, outputs and side effects again from the new
						instruction, replacing the current ones. Turn it off to keep a spec you edited.
					</Field.Description>
				</Field.Content>
			</Field.Field>
		{/if}
		<!-- The picker always shows a choice, the workspace default included, so it is not marked optional -->
		<FormInput
			label="Model"
			labelFor="job-model"
			input={$inputs.modelId}
			optional={false}
			description={modelsError ? undefined : 'The model that drives the agent for this job.'}
		>
			<ModelSelect
				id="job-model"
				{models}
				disabled={!!modelsError}
				bind:value={$inputs.modelId.value}
				noneLabel={agentModel
					? `Workspace default (${agentModel.label || agentModel.model})`
					: 'Workspace default'}
			/>
			{#if modelsError}
				<LoadError title="Couldn't load the models" error={modelsError} />
			{/if}
		</FormInput>
	</Field.Group>
</FormCard>
