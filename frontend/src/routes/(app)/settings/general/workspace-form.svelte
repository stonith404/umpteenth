<script lang="ts">
	import FormCard from '$lib/components/form/form-card.svelte';
	import FormInput from '$lib/components/form/form-input.svelte';
	import * as Field from '$lib/components/ui/field';
	import { createForm } from '$lib/utils/form-util';
	import { z } from 'zod/v4';
	import ReadOnlyValues from './read-only-values.svelte';

	let {
		name,
		readOnly = false,
		onSave
	}: {
		name: string;
		// Shows the saved values as text, for members who may look but not change them
		readOnly?: boolean;
		onSave: (name: string) => Promise<void>;
	} = $props();

	const form = createForm(z.object({ name: z.string().min(1, 'Required').max(100) }), { name });
	const inputs = form.inputs;
</script>

<FormCard
	title="Workspace"
	description="How the workspace shows up in the switcher and in invites."
	{readOnly}
	dirty={form.isDirty()}
	saving={form.saving}
	onsubmit={() => form.submit((data) => onSave(data.name))}
>
	{#if readOnly}
		<ReadOnlyValues items={[{ label: 'Name', value: name }]} />
	{:else}
		<Field.Group columns={2}>
			<FormInput label="Name" bind:input={$inputs.name} />
		</Field.Group>
	{/if}
</FormCard>
