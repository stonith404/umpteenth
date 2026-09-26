<script lang="ts">
	import { getChartContext, Tooltip as TooltipPrimitive } from 'layerchart';
	import { cn, type WithElementRef, type WithoutChildren } from '$lib/utils/style.js';
	import { getPayloadConfigFromPayload, useChart, type TooltipPayload } from './chart-utils.js';
	import type { Snippet } from 'svelte';
	import type { HTMLAttributes } from 'svelte/elements';

	function defaultFormatter(value: any) {
		return `${value}`;
	}

	let {
		ref = $bindable(null),
		class: className,
		hideLabel = false,
		indicator = 'dot',
		hideIndicator = false,
		hideZero = true,
		labelKey,
		label,
		labelFormatter = defaultFormatter,
		labelClassName,
		formatter,
		valueFormatter,
		total,
		emptyLabel,
		nameKey,
		color,
		...restProps
	}: WithoutChildren<WithElementRef<HTMLAttributes<HTMLDivElement>>> & {
		hideLabel?: boolean;
		label?: string;
		indicator?: 'line' | 'dot' | 'dashed';
		nameKey?: string;
		labelKey?: string;
		hideIndicator?: boolean;
		// Leaves out series whose value is 0, so a stacked day lists only what happened on it
		hideZero?: boolean;
		labelClassName?: string;
		labelFormatter?: ((value: any, payload: TooltipPayload[]) => string | number | Snippet) | null;
		// Formats each row's value, e.g. formatCost, instead of the default toLocaleString
		valueFormatter?: (value: number) => string;
		// Adds a 'Total' row under a hairline when two or more rows show, e.g. (items) => formatCost(sum)
		total?: (payload: TooltipPayload[]) => string;
		// Shown under the label when no row is left to show, e.g. 'No runs'
		emptyLabel?: string;
		formatter?: Snippet<
			[
				{
					value: unknown;
					name: string;
					item: TooltipPayload;
					index: number;
					payload: TooltipPayload[];
				}
			]
		>;
	} = $props();

	const chart = useChart();
	const chartCtx = getChartContext();

	// Series with defined values (important for item-based charts like Pie/Arc, where only the hovered item has a value)
	const definedSeries = $derived(
		chartCtx.tooltip.series.filter((s: TooltipPayload) => s.value !== undefined)
	);

	// Rows follow the order of the chart config, which is the legend's order, while layerchart lists a stack top-down
	const visibleSeries = $derived.by(() => {
		const order = Object.keys(chart.config);
		const rank = (item: TooltipPayload) => {
			const index = order.indexOf(String(item.key));
			return index === -1 ? order.length : index;
		};
		return definedSeries
			.filter((s: TooltipPayload) => !(hideZero && typeof s.value === 'number' && s.value === 0))
			.map((item: TooltipPayload, index: number) => ({ item, index }))
			.sort((a, b) => rank(a.item) - rank(b.item) || a.index - b.index)
			.map(({ item }) => item);
	});

	// The label comes from every defined series, so a day whose rows are all hidden still shows its date
	const formattedLabel = $derived.by(() => {
		if (hideLabel || !definedSeries?.length) return null;

		const [item] = definedSeries;
		const tooltipData = chartCtx.tooltip.data;

		// Get the x-axis label value from the raw tooltip data (e.g. a Date or month string)
		const dataLabel = tooltipData != null ? chartCtx.x(tooltipData) : undefined;

		const key = labelKey ?? item?.label ?? item?.key ?? 'value';
		const itemConfig = getPayloadConfigFromPayload(
			chart.config,
			item,
			key,
			tooltipData as Record<string, unknown> | null
		);

		let value: unknown;
		if (!labelKey && typeof label === 'string') {
			value = chart.config[label as keyof typeof chart.config]?.label ?? label;
		} else if (labelKey) {
			value = itemConfig?.label ?? dataLabel;
		} else {
			value = dataLabel;
		}

		if (value === undefined) return null;
		if (!labelFormatter) return value;
		return labelFormatter(value, visibleSeries);
	});

	const nestLabel = $derived(visibleSeries.length === 1 && indicator !== 'dot');
	const totalText = $derived(total && visibleSeries.length > 1 ? total(visibleSeries) : undefined);

	function formatValue(value: unknown) {
		if (valueFormatter && typeof value === 'number') return valueFormatter(value);
		return typeof value === 'number' ? value.toLocaleString('en-US') : String(value);
	}
</script>

{#snippet TooltipLabel()}
	{#if formattedLabel}
		<div class={cn('text-foreground font-medium', labelClassName)}>
			{#if typeof formattedLabel === 'function'}
				{@render formattedLabel()}
			{:else}
				{formattedLabel}
			{/if}
		</div>
	{/if}
{/snippet}

<!-- Portaled to the body so no card clips it, and carrying the chart's data-chart id so the series colour variables scoped to it still reach the swatches -->
<TooltipPrimitive.Root variant="none">
	<div
		bind:this={ref}
		data-chart={chart.id}
		class={cn(
			// Layerchart flips a tooltip to the pointer's other side without clamping it, so on phones it stays narrow enough to land on screen either way
			'bg-popover text-popover-foreground ring-border grid max-w-[min(18rem,45vw)] min-w-32 items-start gap-1.5 rounded-lg px-2.5 py-1.5 text-xs shadow-md ring-1',
			className
		)}
		{...restProps}
	>
		{#if !nestLabel}
			{@render TooltipLabel()}
		{/if}
		{#if visibleSeries.length === 0}
			{#if emptyLabel}
				<div class="text-muted-foreground">{emptyLabel}</div>
			{/if}
		{:else}
			<div class="grid gap-1.5">
				{#each visibleSeries as item, i (item.key + i)}
					{@const key = `${nameKey || item.key || item.label || 'value'}`}
					{@const itemConfig = getPayloadConfigFromPayload(
						chart.config,
						item,
						key,
						chartCtx.tooltip.data
					)}
					{@const indicatorColor = color || item.config?.color || item.color}
					{@const name = itemConfig?.label || item.label}
					<div
						class={cn(
							'flex w-full min-w-0 items-stretch gap-2 [&>svg]:h-2.5 [&>svg]:w-2.5 [&>svg]:text-muted-foreground',
							indicator === 'dot' && 'items-center'
						)}
					>
						{#if formatter && item.value !== undefined && item.label}
							{@render formatter({
								value: item.value,
								name: item.label,
								item,
								index: i,
								payload: visibleSeries
							})}
						{:else}
							{#if itemConfig?.icon}
								<itemConfig.icon />
							{:else if !hideIndicator}
								<!-- Kumo's round series dot -->
								<div
									style="--color-bg: {indicatorColor}; --color-border: {indicatorColor};"
									class={cn('shrink-0 border-(--color-border) bg-(--color-bg)', {
										'size-2 rounded-full': indicator === 'dot',
										'h-full w-1 rounded-[2px]': indicator === 'line',
										'w-0 border-[1.5px] border-dashed bg-transparent': indicator === 'dashed',
										'my-0.5': nestLabel && indicator === 'dashed'
									})}
								></div>
							{/if}
							<div
								class={cn(
									'flex min-w-0 flex-1 justify-between gap-3 leading-none',
									nestLabel ? 'items-end' : 'items-center'
								)}
							>
								<div class="grid min-w-0 gap-1.5">
									{#if nestLabel}
										{@render TooltipLabel()}
									{/if}
									<span class="text-muted-foreground truncate" title={name}>{name}</span>
								</div>
								{#if item.value !== undefined}
									<span class="numeric text-foreground shrink-0 font-medium">
										{formatValue(item.value)}
									</span>
								{/if}
							</div>
						{/if}
					</div>
				{/each}
			</div>
		{/if}
		{#if totalText}
			<div
				class="border-hairline flex items-center justify-between gap-3 border-t pt-1.5 leading-none"
			>
				<span class="text-muted-foreground">Total</span>
				<span class="numeric text-foreground font-medium">{totalText}</span>
			</div>
		{/if}
	</div>
</TooltipPrimitive.Root>
