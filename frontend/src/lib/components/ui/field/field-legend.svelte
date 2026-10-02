<script lang="ts">
	import { cn, type WithElementRef } from '#lib/utils/style.js';
	import type { HTMLAttributes } from 'svelte/elements';

	let {
		ref = $bindable(null),
		class: className,
		variant = 'legend',
		optional = false,
		children,
		...restProps
	}: WithElementRef<HTMLAttributes<HTMLLegendElement>> & {
		variant?: 'legend' | 'label';
		// Appends a muted '(optional)' after the legend, like Field.Label's, for sets such as a list that may stay empty
		optional?: boolean;
	} = $props();
</script>

<legend
	bind:this={ref}
	data-slot="field-legend"
	data-variant={variant}
	class={cn(
		'mb-3 font-medium data-[variant=label]:text-sm data-[variant=legend]:text-base',
		className
	)}
	{...restProps}
>
	{@render children?.()}
	{#if optional}
		<span data-slot="field-label-optional" class="text-muted-foreground font-normal"
			>(optional)</span
		>
	{/if}
</legend>
