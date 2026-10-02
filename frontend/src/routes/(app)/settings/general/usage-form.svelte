<script lang="ts">
	import type { UsageUnit, WorkspaceSettings, WorkspaceSettingsUpdate } from '#lib/api/types.js';
	import FormCard from '#lib/components/form/form-card.svelte';
	import FormInput from '#lib/components/form/form-input.svelte';
	import * as Field from '#lib/components/ui/field/index.js';
	import * as Select from '#lib/components/ui/select/index.js';
	import { createForm } from '#lib/utils/form-util.js';
	import { z } from 'zod/v4';
	import ReadOnlyValues from './read-only-values.svelte';

	let {
		settings,
		readOnly = false,
		onSave
	}: {
		settings: WorkspaceSettings;
		// Shows the saved value as text, for members who may look but not change it
		readOnly?: boolean;
		onSave: (update: WorkspaceSettingsUpdate) => Promise<void>;
	} = $props();

	const UNITS: { value: UsageUnit; label: string; description: string }[] = [
		{ value: 'price', label: 'Price', description: 'What model calls cost, in US dollars' },
		{ value: 'tokens', label: 'Tokens', description: 'Input and output tokens of model calls' }
	];

	function unitLabel(unit: UsageUnit) {
		return UNITS.find((u) => u.value === unit)?.label ?? unit;
	}

	const formSchema = z.object({
		usageUnit: z.enum(['price', 'tokens'])
	});

	const form = createForm(formSchema, { usageUnit: settings.usageUnit });
	const inputs = form.inputs;
</script>

<FormCard
	title="Usage"
	description="How runs, jobs and the dashboard report what model calls used."
	{readOnly}
	dirty={form.isDirty()}
	saving={form.saving}
	onsubmit={() => form.submit(onSave)}
>
	{#if readOnly}
		<ReadOnlyValues items={[{ label: 'Show usage as', value: unitLabel(settings.usageUnit) }]} />
	{:else}
		<Field.Group columns={2}>
			<FormInput
				label="Show usage as"
				labelFor="usage-unit"
				description="Tokens suit local or flat-rate models. Spend limits and model prices stay in US dollars."
				input={$inputs.usageUnit}
			>
				<Select.Root
					type="single"
					bind:value={
						() => $inputs.usageUnit.value, (next) => ($inputs.usageUnit.value = next as UsageUnit)
					}
				>
					<Select.Trigger id="usage-unit" class="w-full">
						{unitLabel($inputs.usageUnit.value)}
					</Select.Trigger>
					<Select.Content align="start">
						{#each UNITS as unit (unit.value)}
							<Select.Item value={unit.value} label={unit.label}>
								<span class="flex flex-col">
									<span>{unit.label}</span>
									<span class="text-muted-foreground text-xs font-normal whitespace-normal">
										{unit.description}
									</span>
								</span>
							</Select.Item>
						{/each}
					</Select.Content>
				</Select.Root>
			</FormInput>
		</Field.Group>
	{/if}
</FormCard>
