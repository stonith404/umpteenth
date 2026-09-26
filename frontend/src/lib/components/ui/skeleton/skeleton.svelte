<script lang="ts">
	import { cn, type WithElementRef, type WithoutChildren } from '$lib/utils/style.js';
	import type { HTMLAttributes } from 'svelte/elements';

	let {
		ref = $bindable(null),
		class: className,
		radius = 'default',
		...restProps
	}: WithoutChildren<WithElementRef<HTMLAttributes<HTMLDivElement>>> & {
		// Matches the corners of what the placeholder stands in for, like a badge ('sm'), a control or row ('md') or a card ('lg')
		radius?: 'sm' | 'md' | 'lg' | 'default';
	} = $props();
</script>

<div
	bind:this={ref}
	data-slot="skeleton"
	class={cn(
		'bg-muted rounded-2xl animate-pulse',
		radius === 'sm' && 'rounded-sm',
		radius === 'md' && 'rounded-md',
		radius === 'lg' && 'rounded-lg',
		className
	)}
	{...restProps}
></div>
