<script lang="ts" module>
	// A change against the previous period, e.g. `+12%` or `-3.1 pts`
	export type KpiDelta = {
		label: string;
		// Whether the change is an improvement, which picks its color; null keeps it neutral
		good: boolean | null;
		direction: 'up' | 'down' | 'flat';
	};

	// Relative change, or null when there is nothing to compare against
	export function percentDelta(
		current: number,
		previous: number,
		higherIsBetter: boolean
	): KpiDelta | null {
		if (previous === 0) return null;
		const change = (current - previous) / previous;
		if (Math.abs(change) < 0.005) return { label: '0%', good: null, direction: 'flat' };
		const direction = change > 0 ? 'up' : 'down';
		return {
			label: `${change > 0 ? '+' : ''}${Math.round(change * 100)}%`,
			good: change > 0 === higherIsBetter,
			direction
		};
	}

	// Difference of two ratios in percentage points
	export function pointsDelta(current: number, previous: number): KpiDelta {
		const change = (current - previous) * 100;
		if (Math.abs(change) < 0.05) return { label: '0 pts', good: null, direction: 'flat' };
		return {
			label: `${change > 0 ? '+' : ''}${change.toFixed(1)} pts`,
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
		delta: KpiDelta | null;
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
	<Card.Header class="py-2.5">
		<Card.Description class="text-base">{label}</Card.Description>
	</Card.Header>
	<Card.Content class="flex flex-1 flex-col gap-2 text-sm">
		<p class="numeric text-[1.75rem] leading-tight font-semibold">{value}</p>
		<p class="text-muted-foreground flex flex-wrap items-center gap-1">
			{#if delta}
				<span
					class={cn(
						'inline-flex items-center gap-0.5 font-medium',
						delta.good === true && 'text-green-600 dark:text-green-400',
						delta.good === false && 'text-red-600 dark:text-red-400'
					)}
				>
					<DeltaIcon class="size-3.5" aria-hidden="true" />
					{delta.label}
				</span>
				vs {rangeLabel}
			{:else}
				Nothing to compare with the {rangeLabel}
			{/if}
		</p>
		{#if footer}
			{@render footer()}
		{/if}
	</Card.Content>
</Card.Root>
