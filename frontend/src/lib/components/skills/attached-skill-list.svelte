<script lang="ts">
	import { Badge } from '#lib/components/ui/badge/index.js';
	import { Button } from '#lib/components/ui/button/index.js';
	import PuzzleIcon from '@lucide/svelte/icons/puzzle';
	import XIcon from '@lucide/svelte/icons/x';

	let {
		items,
		onRemove
	}: {
		// The attached skills, with a description when the workspace's skills could be loaded and a mark for the ones the compile step suggested
		items: { id: string; name: string; description?: string; suggested?: boolean }[];
		onRemove: (id: string) => void;
	} = $props();
</script>

<ul class="flex flex-col gap-2" aria-label="Attached skills">
	{#each items as item (item.id)}
		<li class="bg-muted/40 flex items-center gap-3 rounded-lg py-2 pr-2 pl-3">
			<PuzzleIcon class="text-muted-foreground size-4 shrink-0" />
			<div class="flex min-w-0 flex-1 flex-col">
				<span class="flex min-w-0 items-center gap-2">
					<span class="truncate text-sm font-medium">{item.name}</span>
					{#if item.suggested}<Badge variant="secondary">Suggested</Badge>{/if}
				</span>
				{#if item.description}
					<span class="text-muted-foreground truncate text-xs" title={item.description}>
						{item.description}
					</span>
				{/if}
			</div>
			<Button
				variant="ghost"
				size="icon-sm"
				aria-label="Detach {item.name}"
				onclick={() => onRemove(item.id)}
			>
				<XIcon />
			</Button>
		</li>
	{/each}
</ul>
