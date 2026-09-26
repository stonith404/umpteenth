<script lang="ts">
	import { Badge } from '$lib/components/ui/badge';
	import { Button } from '$lib/components/ui/button';
	import * as Popover from '$lib/components/ui/popover';
	import { RangeCalendar } from '$lib/components/ui/range-calendar';
	import { Separator } from '$lib/components/ui/separator';
	import { cn } from '$lib/utils/style';
	import { getLocalTimeZone, today } from '@internationalized/date';
	import CalendarIcon from '@lucide/svelte/icons/calendar';
	import type { DateRange } from 'bits-ui';
	import { DATE_RANGE_PRESETS, dateRangeLabel, parseIsoDate } from './date-range';

	let {
		selected,
		onChange,
		label = 'Date'
	}: {
		// The filter's URL values, see `date-range.ts`
		selected: string[];
		onChange: (values: string[]) => void;
		label?: string;
	} = $props();

	let open = $state(false);

	const activeLabel = $derived(dateRangeLabel(selected));
	const activePreset = $derived(selected.length === 1 ? selected[0] : null);
	// Future days can't be picked, and "today" is read again on every open so a page left open overnight moves on to the new day
	let maxValue = $state.raw(today(getLocalTimeZone()));

	// The calendar edits a draft, so picking the start date doesn't already filter the table
	let draft = $state<DateRange | undefined>();

	$effect(() => {
		if (!open) return;
		maxValue = today(getLocalTimeZone());
		const start = parseIsoDate(selected[0]);
		const end = parseIsoDate(selected[1] ?? selected[0]);
		draft = start && end ? { start, end } : undefined;
	});

	function applyPreset(value: string) {
		onChange([value]);
		open = false;
	}

	// A range is applied as soon as both ends are picked
	function onRangeChange(range: DateRange | undefined) {
		draft = range;
		if (!range?.start || !range.end) return;
		onChange([range.start.toString(), range.end.toString()]);
		open = false;
	}

	function clear() {
		onChange([]);
		open = false;
	}
</script>

<Popover.Root bind:open>
	<Popover.Trigger>
		{#snippet child({ props })}
			<Button {...props} variant="outline" class="border-dashed">
				<CalendarIcon data-icon="inline-start" />
				{label}
				{#if activeLabel}
					<Separator orientation="vertical" class="mx-1 h-4" />
					<Badge variant="secondary" class="rounded-sm px-1 font-normal">{activeLabel}</Badge>
				{/if}
			</Button>
		{/snippet}
	</Popover.Trigger>
	<Popover.Content class="w-auto p-0" align="start">
		<div class="flex flex-col sm:flex-row">
			<div
				class="flex flex-row flex-wrap gap-1 border-b p-2 sm:w-40 sm:flex-col sm:border-r sm:border-b-0"
			>
				{#each DATE_RANGE_PRESETS as preset (preset.value)}
					<Button
						variant="ghost"
						size="sm"
						class={cn('justify-start', activePreset === preset.value && 'bg-muted')}
						onclick={() => applyPreset(preset.value)}
					>
						{preset.label}
					</Button>
				{/each}
				{#if selected.length > 0}
					<Button
						variant="ghost"
						size="sm"
						class="text-muted-foreground justify-start"
						onclick={clear}
					>
						Clear
					</Button>
				{/if}
			</div>
			<RangeCalendar
				bind:value={() => draft, onRangeChange}
				{maxValue}
				numberOfMonths={1}
				class="p-2"
			/>
		</div>
	</Popover.Content>
</Popover.Root>
