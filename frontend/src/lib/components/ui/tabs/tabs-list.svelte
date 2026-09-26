<script lang="ts" module>
	import { tv, type VariantProps } from 'tailwind-variants';

	export const tabsListVariants = tv({
		base: 'rounded-lg p-0.5 group-data-horizontal/tabs:h-9 group-data-vertical/tabs:rounded-lg data-[variant=line]:rounded-none group/tabs-list text-muted-foreground inline-flex w-fit items-center justify-center group-data-[orientation=vertical]/tabs:h-fit group-data-[orientation=vertical]/tabs:flex-col',
		variants: {
			variant: {
				// Kumo's segmented control: a recessed track with a hairline edge, for switching a view in place
				default: 'cn-tabs-list-variant-default bg-recessed ring-hairline/70 ring-1',
				// Kumo's underline tabs, for navigating between the sections of a page, scrolling sideways when they don't fit
				line: 'cn-tabs-list-variant-line w-full justify-start gap-4 overflow-x-auto overflow-y-hidden rounded-none bg-transparent p-0 pb-2 shadow-[inset_0_-1px_0_var(--hairline)] group-data-horizontal/tabs:h-auto'
			}
		},
		defaultVariants: {
			variant: 'default'
		}
	});

	export type TabsListVariant = VariantProps<typeof tabsListVariants>['variant'];
</script>

<script lang="ts">
	import { Tabs as TabsPrimitive } from 'bits-ui';
	import { onMount } from 'svelte';
	import { cn } from '$lib/utils/style.js';

	let {
		ref = $bindable(null),
		variant = 'default',
		class: className,
		children,
		...restProps
	}: TabsPrimitive.ListProps & {
		variant?: TabsListVariant;
	} = $props();

	let indicatorOffset = $state(0);
	let indicatorSize = $state(0);
	let indicatorVisible = $state(false);
	let indicatorOrientation = $state<'horizontal' | 'vertical'>('horizontal');

	function updateIndicator() {
		if (!ref || variant !== 'line') {
			indicatorVisible = false;
			return;
		}

		const activeTrigger = ref.querySelector<HTMLElement>(
			'[data-slot="tabs-trigger"][data-state="active"]'
		);
		if (!activeTrigger) {
			indicatorVisible = false;
			return;
		}

		const listRect = ref.getBoundingClientRect();
		const triggerRect = activeTrigger.getBoundingClientRect();
		indicatorOrientation = ref.dataset.orientation === 'vertical' ? 'vertical' : 'horizontal';

		if (indicatorOrientation === 'vertical') {
			indicatorOffset = triggerRect.top - listRect.top;
			indicatorSize = triggerRect.height;
		} else {
			indicatorOffset = triggerRect.left - listRect.left + ref.scrollLeft;
			indicatorSize = triggerRect.width;
		}
		indicatorVisible = true;
	}

	onMount(() => {
		if (!ref || variant !== 'line') return;

		const resizeObserver = new ResizeObserver(updateIndicator);
		const observeTriggers = () => {
			ref
				?.querySelectorAll<HTMLElement>('[data-slot="tabs-trigger"]')
				.forEach((trigger) => resizeObserver.observe(trigger));
			updateIndicator();
		};
		const mutationObserver = new MutationObserver(observeTriggers);

		resizeObserver.observe(ref);
		observeTriggers();
		mutationObserver.observe(ref, {
			attributes: true,
			attributeFilter: ['data-state'],
			childList: true,
			subtree: true
		});

		return () => {
			mutationObserver.disconnect();
			resizeObserver.disconnect();
		};
	});
</script>

<TabsPrimitive.List
	bind:ref
	data-slot="tabs-list"
	data-variant={variant}
	class={cn(tabsListVariants({ variant }), variant === 'line' && 'relative', className)}
	{...restProps}
>
	{@render children?.()}
	{#if variant === 'line'}
		<span
			aria-hidden="true"
			data-slot="tabs-indicator"
			class={cn(
				'bg-primary pointer-events-none absolute opacity-0 transition-[transform,width,height,opacity] duration-200 ease-out motion-reduce:transition-none',
				indicatorOrientation === 'horizontal' ? 'bottom-0 left-0 h-0.5' : 'top-0 right-0 w-0.5',
				indicatorVisible && 'opacity-100'
			)}
			style:width={indicatorOrientation === 'horizontal' ? `${indicatorSize}px` : undefined}
			style:height={indicatorOrientation === 'vertical' ? `${indicatorSize}px` : undefined}
			style:transform={indicatorOrientation === 'horizontal'
				? `translate3d(${indicatorOffset}px, 0, 0)`
				: `translate3d(0, ${indicatorOffset}px, 0)`}
		></span>
	{/if}
</TabsPrimitive.List>
