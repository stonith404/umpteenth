<script lang="ts">
	import type { WorkspaceSettings, WorkspaceSettingsUpdate } from '$lib/api/types';
	import FormInput from '$lib/components/form/form-input.svelte';
	import * as Card from '$lib/components/ui/card';
	import * as Field from '$lib/components/ui/field';
	import { createForm } from '$lib/utils/form-util';
	import { trackFormChanges } from '$lib/utils/unsaved-changes-util.svelte';
	import { requiredNumber } from '$lib/utils/zod-util';
	import { z } from 'zod/v4';

	let {
		settings,
		onSave
	}: {
		settings: WorkspaceSettings;
		onSave: (update: WorkspaceSettingsUpdate) => Promise<void>;
	} = $props();

	// The bounds mirror the backend's `settings.Limits` validation, so most mistakes are caught before saving
	const formSchema = z.object({
		defaultImage: z.string().min(1, 'Required').max(500),
		timeoutSeconds: requiredNumber().int().min(30).max(86400),
		maxTurns: requiredNumber().int().min(1).max(1000),
		maxCostUsd: requiredNumber().min(0),
		cpus: requiredNumber().min(0.1).max(64),
		memoryMb: requiredNumber().int().min(64).max(262144),
		pidsLimit: requiredNumber().int().min(16).max(65536)
	});

	const form = createForm(formSchema, {
		defaultImage: settings.defaultImage,
		...settings.defaultLimits
	});
	const inputs = form.inputs;

	trackFormChanges(
		() => form,
		({ defaultImage, ...defaultLimits }) => onSave({ defaultImage, defaultLimits })
	);
</script>

<Card.Root>
	<Card.Header>
		<Card.Title>Sandbox defaults</Card.Title>
		<Card.Description>
			The image and limits every run uses unless its job overrides them.
		</Card.Description>
	</Card.Header>
	<Card.Content>
		<Field.Group>
			<FormInput
				label="Default image"
				description="The base image for job environments."
				inputClass="font-mono"
				bind:input={$inputs.defaultImage}
			/>
			<div class="grid grid-cols-1 gap-x-6 gap-y-7 md:grid-cols-3">
				<FormInput
					label="Timeout"
					type="number"
					suffix="seconds"
					bind:input={$inputs.timeoutSeconds}
				/>
				<FormInput label="Max turns" type="number" bind:input={$inputs.maxTurns} />
				<FormInput
					label="Max cost per run"
					type="number"
					step="any"
					suffix="USD"
					bind:input={$inputs.maxCostUsd}
				/>
				<FormInput label="CPUs" type="number" step="any" bind:input={$inputs.cpus} />
				<FormInput label="Memory" type="number" suffix="MB" bind:input={$inputs.memoryMb} />
				<FormInput label="Process limit" type="number" bind:input={$inputs.pidsLimit} />
			</div>
		</Field.Group>
	</Card.Content>
</Card.Root>
