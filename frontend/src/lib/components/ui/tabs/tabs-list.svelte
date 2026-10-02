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
	import { cn } from '#lib/utils/style.js';

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

	// Whether underline tabs are cut off at either edge, which fades that edge so the strip reads as scrollable
	let overflowStart = $state(false);
	let overflowEnd = $state(false);
	// The trigger last scrolled into view, so the strip only moves when the active tab changes and never fights a manual scroll
	let revealedTrigger: HTMLElement | null = null;

	// The width of the edge fade, which is also the margin kept between the active tab and an edge when revealing it
	const FADE_WIDTH = 32;

	function updateOverflow() {
		if (!ref || variant !== 'line') return;
		const maxScroll = ref.scrollWidth - ref.clientWidth;
		overflowStart = ref.scrollLeft > 1;
		overflowEnd = ref.scrollLeft < maxScroll - 1;
	}

	// Scrolls the strip, never the page, until the active tab sits clear of the faded edges
	function revealTrigger(trigger: HTMLElement, force: boolean) {
		if (!ref || ref.dataset.orientation === 'vertical') return;
		if (trigger === revealedTrigger && !force) return;
		const instant =
			revealedTrigger === null || window.matchMedia('(prefers-reduced-motion: reduce)').matches;
		revealedTrigger = trigger;

		const listRect = ref.getBoundingClientRect();
		const triggerRect = trigger.getBoundingClientRect();
		let delta = 0;
		if (triggerRect.left < listRect.left + FADE_WIDTH) {
			delta = triggerRect.left - listRect.left - FADE_WIDTH;
		} else if (triggerRect.right > listRect.right - FADE_WIDTH) {
			delta = triggerRect.right - listRect.right + FADE_WIDTH;
		}
		if (Math.abs(delta) > 1) {
			ref.scrollTo({ left: ref.scrollLeft + delta, behavior: instant ? 'instant' : 'smooth' });
		}
	}

	function updateIndicator(forceReveal = false) {
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
		revealTrigger(activeTrigger, forceReveal);
		updateOverflow();
	}

	onMount(() => {
		if (!ref || variant !== 'line') return;

		// A resize can push the active tab out of view, so it is revealed again even though it did not change
		const resizeObserver = new ResizeObserver(() => updateIndicator(true));
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

		const list = ref;
		list.addEventListener('scroll', updateOverflow, { passive: true });

		return () => {
			mutationObserver.disconnect();
			resizeObserver.disconnect();
			list.removeEventListener('scroll', updateOverflow);
		};
	});
</script>

<TabsPrimitive.List
	bind:ref
	data-slot="tabs-list"
	data-variant={variant}
	data-overflow-start={overflowStart ? '' : undefined}
	data-overflow-end={overflowEnd ? '' : undefined}
	class={cn(
		tabsListVariants({ variant }),
		variant === 'line' && [
			'relative',
			// The edge fades replace the scrollbar as the cue that more tabs are off-screen
			'[scrollbar-width:none] [&::-webkit-scrollbar]:hidden',
			overflowStart &&
				overflowEnd &&
				'[mask-image:linear-gradient(to_right,transparent,black_2rem,black_calc(100%-2rem),transparent)]',
			overflowStart &&
				!overflowEnd &&
				'[mask-image:linear-gradient(to_right,transparent,black_2rem)]',
			!overflowStart &&
				overflowEnd &&
				'[mask-image:linear-gradient(to_right,black_calc(100%-2rem),transparent)]'
		],
		className
	)}
	{...restProps}
>
	{@render children?.()}
	{#if variant === 'line'}
		<span
			aria-hidden="true"
			data-slot="tabs-indicator"
			class={cn(
				'bg-primary pointer-events-none absolute opacity-0 transition-[translate,width,height,opacity] duration-200 ease-out motion-reduce:transition-none',
				indicatorOrientation === 'horizontal'
					? 'bottom-0 left-0 h-0.5 w-(--indicator-size) translate-x-(--indicator-offset)'
					: 'top-0 right-0 h-(--indicator-size) w-0.5 translate-y-(--indicator-offset)',
				indicatorVisible && 'opacity-100'
			)}
			style:--indicator-size="{indicatorSize}px"
			style:--indicator-offset="{indicatorOffset}px"
		></span>
	{/if}
</TabsPrimitive.List>
