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

	const formSchema = z.object({
		dailySpendLimitUsd: requiredNumber().min(0),
		retentionDays: requiredNumber().int().min(1).max(3650)
	});

	const form = createForm(formSchema, {
		dailySpendLimitUsd: settings.dailySpendLimitUsd,
		retentionDays: settings.retentionDays
	});
	const inputs = form.inputs;

	trackFormChanges(() => form, onSave);
</script>

<Card.Root>
	<Card.Header>
		<Card.Title>Spend and retention</Card.Title>
		<Card.Description
			>Caps what the workspace spends and how long run details are kept.</Card.Description
		>
	</Card.Header>
	<Card.Content>
		<Field.Group class="grid grid-cols-1 gap-x-6 md:grid-cols-2">
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
	</Card.Content>
</Card.Root>
