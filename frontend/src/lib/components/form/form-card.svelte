<!--
	A settings card that saves on its own: its fields form one <form>, named after the card's title, with a Save button at the bottom right
	Save stays disabled until the values differ from the saved ones, and there is no cancel, since leaving the page simply drops the edits
-->
<script lang="ts">
	import { revealFirstInvalidField } from '$lib/components/form/reveal-invalid-field';
	import { Button } from '$lib/components/ui/button';
	import * as Card from '$lib/components/ui/card';
	import type { Snippet } from 'svelte';

	let {
		title,
		description,
		actions,
		footer,
		dirty,
		saving = false,
		readOnly = false,
		onsubmit,
		children
	}: {
		title: string;
		description?: string | Snippet;
		// Buttons in the header, e.g. 'Send test', which act right away rather than on Save, and which the read-only view leaves out
		actions?: Snippet;
		// Controls at the start of the Save row, e.g. the button that adds an item to the card's list
		footer?: Snippet;
		// Whether the values differ from the saved ones
		dirty: boolean;
		saving?: boolean;
		// Shows the card without its form, Save button and header actions, for members who may look but not change anything
		readOnly?: boolean;
		onsubmit: () => unknown;
		children: Snippet;
	} = $props();

	const titleId = $props.id();
	let form = $state<HTMLFormElement>();

	// Enter in a field submits too, but only while Save is enabled, since a form with a disabled default button ignores it
	async function submit(event: SubmitEvent) {
		event.preventDefault();
		await onsubmit();

		// A failed validation or a field the backend rejected gets the focus, which stays inside this card
		await revealFirstInvalidField(form);
	}
</script>

<Card.Root>
	<Card.Header>
		<Card.Title id={titleId}>{title}</Card.Title>
		{#if typeof description === 'string'}
			<Card.Description>{description}</Card.Description>
		{:else if description}
			<Card.Description>{@render description()}</Card.Description>
		{/if}
		{#if actions && !readOnly}
			<Card.Action>{@render actions()}</Card.Action>
		{/if}
	</Card.Header>
	<Card.Content>
		{#if readOnly}
			{@render children()}
		{:else}
			<!-- The browser's own validation bubbles are off, the schema's messages show next to the fields instead -->
			<form
				bind:this={form}
				aria-labelledby={titleId}
				novalidate
				class="flex flex-col gap-7"
				onsubmit={submit}
			>
				{@render children()}
				<div class="flex flex-wrap items-center justify-end gap-2">
					{#if footer}
						<div class="mr-auto">{@render footer()}</div>
					{/if}
					<Button type="submit" disabled={!dirty} isLoading={saving}>Save</Button>
				</div>
			</form>
		{/if}
	</Card.Content>
</Card.Root>
