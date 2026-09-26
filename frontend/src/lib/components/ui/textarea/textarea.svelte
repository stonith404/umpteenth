<script lang="ts">
	import { cn, type WithElementRef, type WithoutChildren } from '$lib/utils/style.js';
	import type { HTMLTextareaAttributes } from 'svelte/elements';

	let {
		ref = $bindable(null),
		value = $bindable(),
		class: className,
		'data-slot': dataSlot = 'textarea',
		...restProps
	}: WithoutChildren<WithElementRef<HTMLTextareaAttributes>> = $props();
</script>

<textarea
	bind:this={ref}
	data-slot={dataSlot}
	class={cn(
		'bg-card shadow-xs focus-visible:border-foreground/50 focus-visible:ring-foreground/50 aria-invalid:ring-destructive/50 aria-invalid:border-destructive resize-none rounded-lg border border-input px-3 py-2 text-base transition-[color,box-shadow,background-color] focus-visible:ring-[0.5px] placeholder:text-muted-foreground flex field-sizing-content min-h-16 w-full outline-none disabled:cursor-not-allowed disabled:opacity-50',
		className
	)}
	bind:value
	{...restProps}
></textarea>
