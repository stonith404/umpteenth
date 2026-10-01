<script lang="ts">
	import FormCard from '$lib/components/form/form-card.svelte';
	import FormInput from '$lib/components/form/form-input.svelte';
	import * as Field from '$lib/components/ui/field';
	import { createForm } from '$lib/utils/form-util';
	import { passkeyAccountSchema } from '$lib/utils/passkey-util';

	let {
		name,
		email,
		onSave
	}: {
		name: string;
		email: string;
		onSave: (name: string, email: string) => Promise<void>;
	} = $props();

	const form = createForm(passkeyAccountSchema, { name, email });
	const inputs = form.inputs;
</script>

<FormCard
	title="Profile"
	description="How you show up to the people you work with."
	dirty={form.isDirty()}
	saving={form.saving}
	onsubmit={() => form.submit((data) => onSave(data.name, data.email))}
>
	<Field.Group columns={2}>
		<FormInput label="Name" bind:input={$inputs.name} autocomplete="name" />
		<FormInput label="Email" type="email" bind:input={$inputs.email} autocomplete="email" />
	</Field.Group>
</FormCard>
