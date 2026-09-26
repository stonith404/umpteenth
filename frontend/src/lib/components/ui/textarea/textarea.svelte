<script lang="ts">
	import { cn, type WithElementRef, type WithoutChildren } from '$lib/utils/style.js';
	import type { HTMLTextareaAttributes } from 'svelte/elements';

	let {
		ref = $bindable(null),
		value = $bindable(),
		class: className,
		mono = false,
		locked = false,
		'data-slot': dataSlot = 'textarea',
		...restProps
	}: WithoutChildren<WithElementRef<HTMLTextareaAttributes>> & {
		// Sets the value in the monospace font, for keys and code, and 'xs' also shrinks it for long machine values
		mono?: boolean | 'xs';
		// Greys the value out while it is read-only because something is working on it, e.g. a description that is compiling
		locked?: boolean;
	} = $props();
</script>

<textarea
	bind:this={ref}
	data-slot={dataSlot}
	class={cn(
		'bg-card shadow-xs focus-visible:border-foreground/50 focus-visible:ring-foreground/50 aria-invalid:ring-destructive/50 aria-invalid:border-destructive resize-none rounded-lg border border-input px-3 py-2 text-base transition-[color,box-shadow,background-color] focus-visible:ring-[0.5px] placeholder:text-muted-foreground flex field-sizing-content min-h-16 w-full outline-none disabled:cursor-not-allowed disabled:opacity-50',
		mono && 'font-mono',
		mono === 'xs' && 'text-xs',
		locked && 'bg-recessed text-muted-foreground cursor-default',
		className
	)}
	bind:value
	{...restProps}
></textarea>
