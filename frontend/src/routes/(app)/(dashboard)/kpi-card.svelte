<script lang="ts" module>
	// A change against the previous period, e.g. `+12%` or `−3 pts`
	export type KpiDelta = {
		label: string;
		// Whether the change is an improvement, which picks its colour; null keeps it neutral
		good: boolean | null;
		direction: 'up' | 'down' | 'flat';
	};

	// A true minus sign, which lines up with the plus in tabular figures where a hyphen looks short
	const MINUS = '−';

	function signed(value: string, change: number) {
		return change > 0 ? `+${value}` : `${MINUS}${value}`;
	}

	// Relative change, or null when there is nothing to compare against
	// `higherIsBetter` null is for figures that are neither good nor bad, like the number of runs
	export function percentDelta(
		current: number,
		previous: number,
		higherIsBetter: boolean | null
	): KpiDelta | null {
		if (previous === 0) return null;
		const change = (current - previous) / previous;
		if (Math.abs(change) < 0.005) return { label: '0%', good: null, direction: 'flat' };
		return {
			label: signed(`${Math.abs(Math.round(change * 100))}%`, change),
			good: higherIsBetter === null ? null : change > 0 === higherIsBetter,
			direction: change > 0 ? 'up' : 'down'
		};
	}

	// Difference of two ratios in percentage points, whole points unless the change is smaller than one
	export function pointsDelta(current: number, previous: number): KpiDelta {
		const change = (current - previous) * 100;
		if (Math.abs(change) < 0.05) return { label: '0 pts', good: null, direction: 'flat' };
		const points =
			Math.abs(change) < 1 ? Math.abs(change).toFixed(1) : Math.round(Math.abs(change));
		return {
			label: signed(`${points} pts`, change),
			good: change > 0,
			direction: change > 0 ? 'up' : 'down'
		};
	}
</script>

<script lang="ts">
	import * as Card from '$lib/components/ui/card';
	import { cn } from '$lib/utils/style';
	import ArrowDownRightIcon from '@lucide/svelte/icons/arrow-down-right';
	import ArrowRightIcon from '@lucide/svelte/icons/arrow-right';
	import ArrowUpRightIcon from '@lucide/svelte/icons/arrow-up-right';
	import type { Snippet } from 'svelte';

	let {
		label,
		value,
		delta,
		rangeLabel,
		footer
	}: {
		label: string;
		value: string;
		// The change, null when the previous period has nothing to compare with, undefined when the figure itself has no value this period
		delta: KpiDelta | null | undefined;
		// Names the comparison period, e.g. "previous 7 days"
		rangeLabel: string;
		footer?: Snippet;
	} = $props();

	const DeltaIcon = $derived(
		delta?.direction === 'up'
			? ArrowUpRightIcon
			: delta?.direction === 'down'
				? ArrowDownRightIcon
				: ArrowRightIcon
	);
</script>

<!-- Like the summary cells of the Cloudflare dashboard: the label sits in the card's strip, the value on its white panel -->
<Card.Root data-testid="kpi-{label.toLowerCase().replaceAll(' ', '-')}">
	<!-- Phones fit two cards side by side, so the gutters shrink to a small card's to leave the figures room -->
	<Card.Header class="py-2.5 max-sm:px-3">
		<Card.Description class="truncate text-sm sm:text-base">{label}</Card.Description>
	</Card.Header>
	<Card.Content class="flex flex-1 flex-col gap-3 text-sm max-sm:p-3">
		<!-- The change sits next to the value, the page description names the period it compares with -->
		<div class="flex flex-wrap items-baseline gap-x-2 gap-y-1">
			<p class="numeric text-2xl leading-tight font-semibold sm:text-[1.75rem]">{value}</p>
			{#if delta}
				<span
					class={cn(
						'numeric inline-flex items-center gap-0.5 font-medium whitespace-nowrap',
						delta.good === true && 'text-success-foreground',
						delta.good === false && 'text-destructive',
						delta.good === null && 'text-muted-foreground'
					)}
					title="vs {rangeLabel}"
				>
					<DeltaIcon class="size-3.5 self-center" aria-hidden="true" />
					{delta.label}
					<span class="sr-only">vs {rangeLabel}</span>
				</span>
			{:else if delta === null}
				<!-- A line of its own, so it never breaks under a wide value in one card while it sits beside a narrow one in the next -->
				<span
					class="text-muted-foreground basis-full"
					title="Nothing to compare with the {rangeLabel}"
				>
					No earlier data
				</span>
			{/if}
		</div>
		<!-- Pushed to the bottom, so the footers of cards in one row line up whatever sits above them -->
		<!-- Every footer is as tall as the success rate's bar, so cards in a row of their own are as tall as the loading skeleton -->
		{#if footer}
			<div class="text-muted-foreground numeric mt-auto min-h-[1.125rem] text-xs sm:text-sm">
				{@render footer()}
			</div>
		{/if}
	</Card.Content>
</Card.Root>
