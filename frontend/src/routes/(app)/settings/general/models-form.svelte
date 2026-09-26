<script lang="ts">
	import type { Model, WorkspaceSettings, WorkspaceSettingsUpdate } from '$lib/api/types';
	import FormCard from '$lib/components/form/form-card.svelte';
	import FormInput from '$lib/components/form/form-input.svelte';
	import ModelSelect from '$lib/components/model-select.svelte';
	import * as Field from '$lib/components/ui/field';
	import { createForm } from '$lib/utils/form-util';
	import { z } from 'zod/v4';
	import ReadOnlyValues from './read-only-values.svelte';

	let {
		settings,
		models,
		readOnly = false,
		onSave
	}: {
		settings: WorkspaceSettings;
		models: Model[];
		// Shows the saved values as text, for members who may look but not change them
		readOnly?: boolean;
		onSave: (update: WorkspaceSettingsUpdate) => Promise<void>;
	} = $props();

	// An empty value is sent as-is, which the backend stores as "not set"
	const formSchema = z.object({
		agentModelId: z.string().max(200),
		utilityModelId: z.string().max(200),
		reflectionModelId: z.string().max(200)
	});

	const form = createForm(formSchema, {
		agentModelId: settings.agentModelId ?? '',
		utilityModelId: settings.utilityModelId ?? '',
		reflectionModelId: settings.reflectionModelId ?? ''
	});
	const inputs = form.inputs;

	const roles = [
		{ key: 'agentModelId', label: 'Agent', description: 'Drives the agent loop.', none: 'Not set' },
		{
			key: 'utilityModelId',
			label: 'Utility',
			description: 'Compiles jobs and verifies results.',
			none: 'Same as the agent model'
		},
		{
			key: 'reflectionModelId',
			label: 'Reflection',
			description: 'Turns runs into playbook updates.',
			none: 'Not set'
		}
	] as const;

	// A model as the pickers name it, with its provider, or its bare ID when it was deleted since
	function modelName(id: string | null | undefined) {
		if (!id) return null;
		const model = models.find((m) => m.id === id);
		return model ? `${model.label || model.model} · ${model.providerName}` : id;
	}
</script>

<FormCard
	title="Default models"
	{readOnly}
	dirty={form.isDirty()}
	saving={form.saving}
	onsubmit={() => form.submit(onSave)}
>
	{#snippet description()}
		The models each role uses unless a job picks its own. Manage them under
		<a href="/settings/providers" class="underline underline-offset-3">Providers & models</a>.
	{/snippet}

	{#if readOnly}
		<!-- Model names are long, so phones list them one per row -->
		<ReadOnlyValues
			columns={3}
			class="grid-cols-1"
			items={roles.map((role) => ({
				label: role.label,
				value: modelName(settings[role.key]),
				empty: role.none
			}))}
		/>
	{:else}
		<Field.Group class="grid grid-cols-1 gap-x-6 md:grid-cols-3">
			{#each roles as role (role.key)}
				<FormInput
					label={role.label}
					labelFor="default-model-{role.key}"
					description={role.description}
					input={$inputs[role.key]}
				>
					<ModelSelect
						id="default-model-{role.key}"
						{models}
						noneLabel={role.none}
						invalid={!!$inputs[role.key].error}
						bind:value={$inputs[role.key].value}
					/>
				</FormInput>
			{/each}
		</Field.Group>
	{/if}
</FormCard>
