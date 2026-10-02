<script lang="ts">
	import type { HTMLAttributes } from 'svelte/elements';
	import { cn, type WithElementRef } from '#lib/utils/style.js';

	let {
		ref = $bindable(null),
		class: className,
		children,
		size = 'default',
		variant = 'default',
		...restProps
	}: WithElementRef<HTMLAttributes<HTMLDivElement>> & {
		// 'tile' is a figure tile like a dashboard KPI: a short header strip, and a small card's gutters on phones so two tiles fit side by side
		size?: 'default' | 'sm' | 'tile';
		// 'danger' tints the frame red, for a card of destructive actions like a danger zone
		// 'dock' floats over the page like a popover below xl, for actions docked to the bottom of the window such as the new job's save bar
		variant?: 'default' | 'danger' | 'dock';
	} = $props();
</script>

<div
	bind:this={ref}
	data-slot="card"
	data-size={size}
	data-variant={variant}
	class={cn(
		'bg-layer text-card-foreground ring-hairline gap-0 overflow-hidden rounded-lg text-base ring-1 group/card flex flex-col',
		variant === 'danger' && 'ring-destructive/30',
		variant === 'dock' && 'max-xl:bg-popover max-xl:ring-border max-xl:shadow-lg',
		className
	)}
	{...restProps}
>
	{@render children?.()}
</div>
