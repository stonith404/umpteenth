<script lang="ts">
	import type { Model, WorkspaceSettings, WorkspaceSettingsUpdate } from '$lib/api/types';
	import FormInput from '$lib/components/form/form-input.svelte';
	import ModelSelect from '$lib/components/model-select.svelte';
	import * as Card from '$lib/components/ui/card';
	import * as Field from '$lib/components/ui/field';
	import { createForm } from '$lib/utils/form-util';
	import { trackFormChanges } from '$lib/utils/unsaved-changes-util.svelte';
	import { z } from 'zod/v4';

	let {
		settings,
		models,
		onSave
	}: {
		settings: WorkspaceSettings;
		models: Model[];
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

	trackFormChanges(() => form, onSave);

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
</script>

<Card.Root>
	<Card.Header>
		<Card.Title>Default models</Card.Title>
		<Card.Description>
			The models each role uses unless a job picks its own. Manage them under
			<a href="/settings/providers" class="underline underline-offset-3">Providers & models</a>.
		</Card.Description>
	</Card.Header>
	<Card.Content>
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
	</Card.Content>
</Card.Root>
