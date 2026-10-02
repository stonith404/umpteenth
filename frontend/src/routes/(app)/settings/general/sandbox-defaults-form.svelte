<script lang="ts">
	import type { WorkspaceSettings, WorkspaceSettingsUpdate } from '#lib/api/types.js';
	import FormCard from '#lib/components/form/form-card.svelte';
	import FormInput from '#lib/components/form/form-input.svelte';
	import * as Field from '#lib/components/ui/field/index.js';
	import { formatCost } from '#lib/utils/format-util.js';
	import { createForm } from '#lib/utils/form-util.js';
	import { requiredNumber } from '#lib/utils/zod-util.js';
	import { z } from 'zod/v4';
	import ReadOnlyValues from './read-only-values.svelte';

	let {
		settings,
		readOnly = false,
		onSave
	}: {
		settings: WorkspaceSettings;
		// Shows the saved values as text, for members who may look but not change them
		readOnly?: boolean;
		onSave: (update: WorkspaceSettingsUpdate) => Promise<void>;
	} = $props();

	// The bounds mirror the backend's `settings.Limits` validation, so most mistakes are caught before saving
	const formSchema = z.object({
		defaultImage: z.string().min(1, 'Required').max(500),
		timeoutSeconds: requiredNumber().int().min(30).max(86400),
		maxTurns: requiredNumber().int().min(1).max(1000),
		maxCostUsd: requiredNumber().min(0),
		cpus: requiredNumber().min(0.1).max(64),
		memoryMb: requiredNumber().int().min(64).max(262144)
	});

	const form = createForm(formSchema, {
		defaultImage: settings.defaultImage,
		...settings.defaultLimits
	});
	const inputs = form.inputs;

	const limits = $derived(settings.defaultLimits);
</script>

<FormCard
	title="Sandbox defaults"
	description="The image and limits every run uses unless its job overrides them."
	{readOnly}
	dirty={form.isDirty()}
	saving={form.saving}
	onsubmit={() =>
		form.submit(({ defaultImage, ...defaultLimits }) => onSave({ defaultImage, defaultLimits }))}
>
	{#if readOnly}
		<div class="flex flex-col gap-4">
			<ReadOnlyValues
				items={[{ label: 'Default image', value: settings.defaultImage, mono: true }]}
			/>
			<ReadOnlyValues
				columns={3}
				items={[
					{ label: 'Timeout', value: `${limits.timeoutSeconds} seconds` },
					{ label: 'Max turns', value: String(limits.maxTurns) },
					{ label: 'Max cost per run', value: formatCost(limits.maxCostUsd) },
					{ label: 'CPUs', value: `${limits.cpus} ${limits.cpus === 1 ? 'core' : 'cores'}` },
					{ label: 'Memory', value: `${limits.memoryMb} MB` }
				]}
			/>
		</div>
	{:else}
		<Field.Group>
			<!-- The image takes two of the three columns below, so its right edge lines up with theirs -->
			<div class="grid grid-cols-1 gap-x-6 md:grid-cols-3">
				<FormInput
					label="Default image"
					description="The base image for job environments."
					monospace
					class="md:col-span-2"
					bind:input={$inputs.defaultImage}
				/>
			</div>
			<div class="grid grid-cols-1 gap-x-6 gap-y-7 md:grid-cols-3">
				<FormInput
					label="Timeout"
					type="number"
					suffix="seconds"
					bind:input={$inputs.timeoutSeconds}
				/>
				<FormInput
					label="Max turns"
					description="Agent steps before a run stops."
					type="number"
					bind:input={$inputs.maxTurns}
				/>
				<FormInput
					label="Max cost per run"
					type="number"
					step="any"
					suffix="USD"
					bind:input={$inputs.maxCostUsd}
				/>
				<FormInput label="CPUs" type="number" step="any" suffix="cores" bind:input={$inputs.cpus} />
				<FormInput label="Memory" type="number" suffix="MB" bind:input={$inputs.memoryMb} />
			</div>
		</Field.Group>
	{/if}
</FormCard>
