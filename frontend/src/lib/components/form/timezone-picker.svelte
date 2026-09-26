<script lang="ts">
	import { Button } from '$lib/components/ui/button';
	import * as Command from '$lib/components/ui/command';
	import * as Popover from '$lib/components/ui/popover';
	import { localTimezone, timezones } from '$lib/utils/job-util';
	import { cn } from '$lib/utils/style';
	import CheckIcon from '@lucide/svelte/icons/check';
	import ChevronsUpDownIcon from '@lucide/svelte/icons/chevrons-up-down';

	let {
		value = $bindable(''),
		id,
		invalid = false,
		disabled = false,
		class: className
	}: {
		value?: string;
		id?: string;
		invalid?: boolean;
		disabled?: boolean;
		class?: string;
	} = $props();

	let open = $state(false);

	// UTC and the browser's zone are listed first, since they are the most likely picks
	const local = localTimezone();
	const zones = [...new Set(['UTC', local, ...timezones()])];

	// Each zone's current offset, e.g. 'UTC+2', so zones that share a clock are easy to tell apart
	// Computed once for all zones when the list first opens, since formatting 400 zones on every keystroke would be wasted work
	let offsets = $state<Map<string, string> | null>(null);
	$effect(() => {
		if (open && !offsets) offsets = new Map(zones.map((zone) => [zone, utcOffset(zone)]));
	});

	function utcOffset(zone: string) {
		try {
			const parts = new Intl.DateTimeFormat('en-US', {
				timeZone: zone,
				timeZoneName: 'shortOffset'
			}).formatToParts(new Date());
			const name = parts.find((part) => part.type === 'timeZoneName')?.value ?? '';
			// Intl names offsets after GMT and writes no offset as GMT+0, both of which read better as UTC
			return name.replace(/^GMT/, 'UTC').replace(/^UTC[+-]0$/, 'UTC');
		} catch {
			return '';
		}
	}

	function select(zone: string) {
		value = zone;
		open = false;
	}
</script>

<Popover.Root bind:open>
	<Popover.Trigger>
		{#snippet child({ props })}
			<Button
				{...props}
				{id}
				{disabled}
				variant="outline"
				role="combobox"
				aria-expanded={open}
				aria-invalid={invalid}
				class={cn(
					'bg-card hover:bg-accent dark:bg-card dark:hover:bg-accent aria-invalid:border-destructive border-input w-full justify-between shadow-xs font-normal',
					className
				)}
			>
				<span class={cn('truncate', !value && 'text-muted-foreground')}>{value || 'UTC'}</span>
				<ChevronsUpDownIcon class="text-muted-foreground size-3.5" />
			</Button>
		{/snippet}
	</Popover.Trigger>
	<!-- As wide as the field, like the selects next to it, but never so narrow that long zone names get cut -->
	<Popover.Content sameWidth class="max-w-[calc(100vw-2rem)] min-w-72 p-0" align="start">
		<Command.Root>
			<Command.Input placeholder="Search timezones" />
			<Command.List class="max-h-72">
				<Command.Empty>No timezone found</Command.Empty>
				<Command.Group>
					{#each zones as zone (zone)}
						{@const selected = (value || 'UTC') === zone}
						<!-- The check sits at the end like in the selects, in a slot of its own so the trailing columns line up whether a row is picked or not -->
						<Command.Item
							value={zone}
							keywords={[offsets?.get(zone) ?? '']}
							onSelect={() => select(zone)}
							class="relative pr-8"
						>
							<span class="min-w-0 flex-1 truncate">{zone}</span>
							{#if zone === local}
								<span class="text-muted-foreground text-xs">Local</span>
							{/if}
							<span class="text-muted-foreground numeric w-16 shrink-0 text-right text-xs">
								{offsets?.get(zone) ?? ''}
							</span>
							{#if selected}
								<CheckIcon class="absolute end-2 size-4" />
							{/if}
						</Command.Item>
					{/each}
				</Command.Group>
			</Command.List>
		</Command.Root>
	</Popover.Content>
</Popover.Root>
