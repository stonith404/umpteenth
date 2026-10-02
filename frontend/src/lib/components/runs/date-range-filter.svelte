<script lang="ts">
	import { Button } from '#lib/components/ui/button/index.js';
	import * as Popover from '#lib/components/ui/popover/index.js';
	import { RangeCalendar } from '#lib/components/ui/range-calendar/index.js';
	import { getLocalTimeZone, today } from '@internationalized/date';
	import ChevronDownIcon from '@lucide/svelte/icons/chevron-down';
	import type { DateRange } from 'bits-ui';
	import { DATE_RANGE_PRESETS, dateRangeLabel, parseIsoDate } from './date-range';

	let {
		selected,
		onChange
	}: {
		// The filter's URL values, see `date-range.ts`
		selected: string[];
		onChange: (values: string[]) => void;
	} = $props();

	let open = $state(false);

	const activeLabel = $derived(dateRangeLabel(selected));
	const activePreset = $derived(selected.length === 1 ? selected[0] : null);
	// Future days can't be picked, and "today" is read again on every open so a page left open overnight moves on to the new day
	let maxValue = $state.raw(today(getLocalTimeZone()));

	// The calendar edits a draft, so picking the start date doesn't already filter the table
	let draft = $state<DateRange | undefined>();

	// Opening starts the draft from the applied range
	function onOpenChange(next: boolean) {
		if (!next) return;
		maxValue = today(getLocalTimeZone());
		const start = parseIsoDate(selected[0]);
		const end = parseIsoDate(selected[1] ?? selected[0]);
		draft = start && end ? { start, end } : undefined;
	}

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

<Popover.Root bind:open {onOpenChange}>
	<Popover.Trigger>
		{#snippet child({ props })}
			<!-- Reads like the faceted filters next to it: the label, the range after a hairline, then a chevron -->
			<Button {...props} variant="outline" class="max-w-full shrink-0">
				Date
				{#if activeLabel}
					<span class="bg-border h-4 w-px" aria-hidden="true"></span>
					<span class="truncate font-normal">{activeLabel}</span>
				{/if}
				<ChevronDownIcon data-icon="inline-end" class="text-muted-foreground" />
			</Button>
		{/snippet}
	</Popover.Trigger>
	<Popover.Content class="w-auto" padding="none" align="start">
		<div class="flex flex-col sm:flex-row">
			<!-- Phones get the presets in two columns above the calendar, so the popover stays as narrow as the calendar -->
			<div
				class="grid grid-cols-2 gap-1 border-b p-2 sm:flex sm:w-40 sm:flex-col sm:border-r sm:border-b-0"
			>
				{#each DATE_RANGE_PRESETS as preset (preset.value)}
					{@const active = activePreset === preset.value}
					<!-- The applied preset is pressed, so the ghost variant gives it the hover fill, which unlike the muted fill still shows on the dark popover -->
					<Button
						variant="ghost"
						size="sm"
						class="justify-start"
						aria-pressed={active}
						onclick={() => applyPreset(preset.value)}
					>
						{preset.label}
					</Button>
				{/each}
				{#if selected.length > 0}
					<Button variant="ghost-muted" size="sm" class="justify-start" onclick={clear}>
						Clear
					</Button>
				{/if}
			</div>
			<RangeCalendar
				bind:value={() => draft, onRangeChange}
				{maxValue}
				numberOfMonths={1}
				padding="compact"
			/>
		</div>
	</Popover.Content>
</Popover.Root>
