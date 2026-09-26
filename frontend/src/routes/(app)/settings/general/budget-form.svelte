<script lang="ts">
	import type { WorkspaceSettings, WorkspaceSettingsUpdate } from '$lib/api/types';
	import FormCard from '$lib/components/form/form-card.svelte';
	import FormInput from '$lib/components/form/form-input.svelte';
	import * as Field from '$lib/components/ui/field';
	import { formatCost } from '$lib/utils/format-util';
	import { createForm } from '$lib/utils/form-util';
	import { requiredNumber } from '$lib/utils/zod-util';
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

	const formSchema = z.object({
		dailySpendLimitUsd: requiredNumber().min(0),
		retentionDays: requiredNumber().int().min(1).max(3650)
	});

	const form = createForm(formSchema, {
		dailySpendLimitUsd: settings.dailySpendLimitUsd,
		retentionDays: settings.retentionDays
	});
	const inputs = form.inputs;
</script>

<FormCard
	title="Spend and retention"
	description="Caps what the workspace spends and how long run details are kept."
	{readOnly}
	dirty={form.isDirty()}
	saving={form.saving}
	onsubmit={() => form.submit(onSave)}
>
	{#if readOnly}
		<ReadOnlyValues
			items={[
				{
					label: 'Daily spend limit',
					value: settings.dailySpendLimitUsd
						? `${formatCost(settings.dailySpendLimitUsd)} per day`
						: 'No limit'
				},
				{ label: 'Retention', value: `${settings.retentionDays} days` }
			]}
		/>
	{:else}
		<Field.Group columns={2}>
			<FormInput
				label="Daily spend limit"
				description="Caps the workspace's LLM spend per day, 0 means no limit."
				type="number"
				step="any"
				suffix="USD"
				bind:input={$inputs.dailySpendLimitUsd}
			/>
			<FormInput
				label="Retention"
				description="Events and artifacts older than this are pruned, run records are kept."
				type="number"
				suffix="days"
				bind:input={$inputs.retentionDays}
			/>
		</Field.Group>
	{/if}
</FormCard>
