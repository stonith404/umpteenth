<script lang="ts">
	import { Calendar } from '$lib/components/ui/calendar';
	import * as Popover from '$lib/components/ui/popover';
	import { cn } from '$lib/utils/style';
	import {
		CalendarDate,
		DateFormatter,
		getLocalTimeZone,
		today,
		type DateValue
	} from '@internationalized/date';
	import CalendarIcon from '@lucide/svelte/icons/calendar';
	import XIcon from '@lucide/svelte/icons/x';
	import type { HTMLAttributes } from 'svelte/elements';

	type Props = {
		value?: Date;
		id?: string;
		clearable?: boolean;
		futureOnly?: boolean;
		invalid?: boolean;
	} & HTMLAttributes<HTMLDivElement>;

	let {
		value = $bindable(),
		id,
		clearable = false,
		futureOnly = false,
		invalid = false,
		...restProps
	}: Props = $props();

	let open = $state(false);

	const calendarDate = $derived(value ? toCalendarDate(value) : undefined);
	const minValue = $derived(futureOnly ? today(getLocalTimeZone()).add({ days: 1 }) : undefined);

	const formatter = new DateFormatter('en-US', { dateStyle: 'long' });

	function toCalendarDate(d: Date): CalendarDate {
		return new CalendarDate(d.getFullYear(), d.getMonth() + 1, d.getDate());
	}

	// The calendar reports an empty value when it mounts without one, which must neither clear the date nor close the popover
	// Clearing goes through the clear button instead, since deselecting is disabled
	function onSelect(next: DateValue | undefined) {
		if (!next) return;
		open = false;
		value = next.toDate(getLocalTimeZone());
	}
</script>

<div class="relative w-full" {...restProps}>
	<Popover.Root bind:open>
		<Popover.Trigger {id} class="w-full">
			{#snippet child({ props })}
				<button
					{...props}
					type="button"
					data-invalid={invalid}
					class={cn(
						'bg-card shadow-xs hover:bg-accent focus-visible:ring-ring data-[invalid=true]:ring-destructive/20 data-[invalid=true]:border-destructive flex h-9 w-full items-center justify-start rounded-lg border border-input px-3 text-base font-normal transition-field outline-none focus-visible:ring-2 data-[invalid=true]:ring-3',
						!value && 'text-muted-foreground'
					)}
				>
					<CalendarIcon class="mr-2 size-4" />
					{value ? formatter.format(value) : 'Select a date'}
				</button>
			{/snippet}
		</Popover.Trigger>
		<Popover.Content class="w-auto" padding="none" align="start">
			<Calendar
				type="single"
				bind:value={() => calendarDate, onSelect}
				{minValue}
				preventDeselect
				initialFocus
			/>
		</Popover.Content>
	</Popover.Root>
	{#if clearable && value}
		<button
			type="button"
			class="text-muted-foreground hover:text-foreground absolute top-1/2 right-3 -translate-y-1/2"
			aria-label="Clear date"
			onclick={() => (value = undefined)}
		>
			<XIcon class="size-4" />
		</button>
	{/if}
</div>
