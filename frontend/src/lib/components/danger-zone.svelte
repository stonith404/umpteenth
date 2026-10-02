<!--
	The card at the bottom of a settings page that holds its irreversible actions, such as deleting a job or leaving a workspace
	Each action is a row that says what it does, with its trigger on the right, and the solid red confirm lives in the dialog the trigger opens
-->
<script lang="ts" module>
	import type { Component, Snippet } from 'svelte';

	export type DangerZoneAction = {
		// What the action does to the thing, e.g. 'Delete this job'
		title: string;
		// What happens as a consequence, which a snippet can extend with a link
		description: string | Snippet;
		// The trigger's label, e.g. 'Delete job', which the confirm dialog's button repeats
		label: string;
		icon?: Component;
		onclick: () => void;
	};
</script>

<script lang="ts">
	import { Button } from '#lib/components/ui/button/index.js';
	import * as Card from '#lib/components/ui/card/index.js';

	let { actions }: { actions: DangerZoneAction[] } = $props();

	const id = $props.id();
</script>

<Card.Root variant="danger">
	<Card.Header>
		<Card.Title>Danger zone</Card.Title>
	</Card.Header>
	<Card.Content class="flex flex-col">
		{#each actions as action, i (action.label)}
			<!-- The trigger moves under its text on phones, where the two don't fit side by side -->
			<div
				class="border-border flex flex-col items-start gap-4 not-first:mt-4 not-first:border-t not-first:pt-4 sm:flex-row sm:items-center sm:justify-between"
			>
				<div class="flex min-w-0 flex-col gap-0.5">
					<p class="text-sm font-medium">{action.title}</p>
					<!-- The same leading as the card descriptions, since the sentence wraps on phones -->
					<p id="{id}-{i}" class="text-muted-foreground text-sm leading-snug">
						{#if typeof action.description === 'string'}
							{action.description}
						{:else}
							{@render action.description()}
						{/if}
					</p>
				</div>
				<!-- The row's sentence describes the trigger, so screen readers announce the consequence along with the label -->
				<Button
					variant="destructive-outline"
					class="shrink-0"
					aria-describedby="{id}-{i}"
					onclick={action.onclick}
				>
					{#if action.icon}
						<action.icon data-icon="inline-start" />
					{/if}
					{action.label}
				</Button>
			</div>
		{/each}
	</Card.Content>
</Card.Root>
