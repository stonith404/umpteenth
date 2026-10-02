<script lang="ts">
	import { cn, type WithElementRef } from '#lib/utils/style.js';
	import type { HTMLAttributes } from 'svelte/elements';
	import { Dialog as DialogPrimitive } from 'bits-ui';
	import { Button } from '#lib/components/ui/button/index.js';

	let {
		ref = $bindable(null),
		class: className,
		children,
		showCloseButton = false,
		...restProps
	}: WithElementRef<HTMLAttributes<HTMLDivElement>> & {
		showCloseButton?: boolean;
	} = $props();

	// 'body' while a scrolling body above the footer overflows, 'content' while the whole dialog scrolls, unset otherwise
	let overflow = $state<'body' | 'content'>();

	// Kumo's layer dialog separates its footer from a scrolling body with a hairline, which also shows that there is more above
	$effect(() => {
		const footer = ref;
		const content = footer?.closest<HTMLElement>('[data-slot=dialog-content]');
		if (!footer || !content) return;

		const scrolls = (el: Element) =>
			el.scrollHeight - el.clientHeight > 1 && /auto|scroll/.test(getComputedStyle(el).overflowY);
		const update = () => {
			const siblings = Array.from(content.children).filter((el) => el !== footer);
			overflow = siblings.some(scrolls) ? 'body' : scrolls(content) ? 'content' : undefined;
		};

		// Watch the dialog and the children of each of its parts, since a growing form resizes those but not the scroller itself
		const resize = new ResizeObserver(update);
		const observe = () => {
			resize.disconnect();
			resize.observe(content);
			for (const part of content.children) {
				resize.observe(part);
				for (const child of part.children) resize.observe(child);
			}
		};

		// Fields that appear or disappear, such as validation messages or added rows, change what needs watching
		const mutations = new MutationObserver(() => {
			observe();
			update();
		});
		mutations.observe(content, { childList: true, subtree: true });

		observe();
		update();
		return () => {
			resize.disconnect();
			mutations.disconnect();
		};
	});
</script>

<div
	bind:this={ref}
	data-slot="dialog-footer"
	data-overflow={overflow}
	class={cn(
		'flex flex-col-reverse gap-2 sm:flex-row sm:justify-end',
		// The hairline spans the dialog edge to edge, 16px above the buttons like Kumo's layer footer
		'data-overflow:border-hairline data-overflow:-mx-6 data-overflow:-mt-2 data-overflow:border-t data-overflow:px-6 data-overflow:pt-4',
		// When the whole dialog scrolls, the footer sticks to its bottom edge so the actions stay in reach
		'data-[overflow=content]:bg-card data-[overflow=content]:sticky data-[overflow=content]:-bottom-6 data-[overflow=content]:z-10 data-[overflow=content]:-mb-6 data-[overflow=content]:pb-6',
		className
	)}
	{...restProps}
>
	{@render children?.()}
	{#if showCloseButton}
		<DialogPrimitive.Close>
			{#snippet child({ props })}
				<Button variant="outline" {...props}>Close</Button>
			{/snippet}
		</DialogPrimitive.Close>
	{/if}
</div>
