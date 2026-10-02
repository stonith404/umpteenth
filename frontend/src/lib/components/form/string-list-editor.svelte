<script lang="ts">
	import { Button } from '#lib/components/ui/button/index.js';
	import { Input } from '#lib/components/ui/input/index.js';
	import PlusIcon from '@lucide/svelte/icons/plus';
	import XIcon from '@lucide/svelte/icons/x';
	import { tick } from 'svelte';

	let {
		items = $bindable([]),
		label,
		placeholder,
		addLabel = 'Add'
	}: {
		items?: string[];
		// Names the list for screen readers, e.g. "Success criterion"
		label: string;
		placeholder?: string;
		addLabel?: string;
	} = $props();

	let list: HTMLUListElement;

	// The new row is focused right away, so adding several entries doesn't need the mouse
	async function add() {
		items = [...items, ''];
		await tick();
		list.querySelector<HTMLInputElement>('li:last-child input')?.focus();
	}

	function remove(index: number) {
		items = items.filter((_, i) => i !== index);
	}

	function onKeydown(event: KeyboardEvent, index: number) {
		if (event.key === 'Enter') {
			event.preventDefault();
			if (index === items.length - 1) void add();
		}
	}
</script>

<div class="flex flex-col gap-2">
	<ul bind:this={list} class="flex flex-col gap-2">
		{#each items, i}
			<li class="flex items-center gap-2">
				<Input
					bind:value={items[i]}
					{placeholder}
					aria-label="{label} {i + 1}"
					onkeydown={(e) => onKeydown(e, i)}
				/>
				<Button
					variant="ghost"
					size="icon-sm"
					aria-label="Remove {label.toLowerCase()} {i + 1}"
					onclick={() => remove(i)}
				>
					<XIcon />
				</Button>
			</li>
		{/each}
	</ul>
	<Button variant="outline" size="sm" class="w-fit" onclick={add}>
		<PlusIcon data-icon="inline-start" />
		{addLabel}
	</Button>
</div>
