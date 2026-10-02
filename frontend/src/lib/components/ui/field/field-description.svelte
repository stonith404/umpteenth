<script lang="ts">
	import { cn, type WithElementRef } from '#lib/utils/style.js';
	import type { HTMLAttributes } from 'svelte/elements';

	let {
		ref = $bindable(null),
		class: className,
		tone = 'muted',
		children,
		...restProps
	}: WithElementRef<HTMLAttributes<HTMLParagraphElement>> & {
		// 'warning' flags input that is accepted but won't work, 'strong' reads the value back as a confirmed result rather than a hint
		tone?: 'muted' | 'warning' | 'strong';
	} = $props();
</script>

<p
	bind:this={ref}
	data-slot="field-description"
	class={cn(
		'text-muted-foreground text-left text-sm [[data-variant=legend]+&]:-mt-1.5 leading-normal font-normal group-has-[[data-orientation=horizontal]]/field:text-balance',
		'last:mt-0 nth-last-2:-mt-1',
		'[&>a:hover]:text-primary [&>a]:underline [&>a]:underline-offset-4',
		tone === 'warning' && 'text-warning-foreground',
		tone === 'strong' && 'text-foreground font-medium',
		className
	)}
	{...restProps}
>
	{@render children?.()}
</p>
