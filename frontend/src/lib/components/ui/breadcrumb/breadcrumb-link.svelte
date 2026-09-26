<script lang="ts">
	import { cn, type WithElementRef } from '$lib/utils/style.js';
	import type { Snippet } from 'svelte';
	import type { HTMLAnchorAttributes } from 'svelte/elements';

	let {
		ref = $bindable(null),
		class: className,
		href = undefined,
		child,
		children,
		...restProps
	}: WithElementRef<HTMLAnchorAttributes> & {
		child?: Snippet<[{ props: HTMLAnchorAttributes }]>;
	} = $props();

	const attrs = $derived({
		'data-slot': 'breadcrumb-link',
		// The list clips overflow, which would cut off a focus ring, so focus shows as an underline instead
		// A long ancestor such as a job name truncates once its item is capped, rather than pushing the current page out
		class: cn(
			'truncate hover:text-foreground focus-visible:text-foreground underline-offset-4 transition-colors outline-none focus-visible:underline',
			className
		),
		href,
		...restProps
	});
</script>

{#if child}
	{@render child({ props: attrs })}
{:else}
	<a bind:this={ref} {...attrs}>
		{@render children?.()}
	</a>
{/if}
