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
				<ChevronsUpDownIcon class="opacity-50" />
			</Button>
		{/snippet}
	</Popover.Trigger>
	<Popover.Content class="w-72 p-0" align="start">
		<Command.Root>
			<Command.Input placeholder="Search timezones" />
			<Command.List class="max-h-72">
				<Command.Empty>No timezone found</Command.Empty>
				<Command.Group>
					{#each zones as zone (zone)}
						<Command.Item value={zone} onSelect={() => select(zone)}>
							<CheckIcon class={cn('size-4', (value || 'UTC') !== zone && 'opacity-0')} />
							{zone}
							{#if zone === local}
								<span class="text-muted-foreground ml-auto text-xs">Local</span>
							{/if}
						</Command.Item>
					{/each}
				</Command.Group>
			</Command.List>
		</Command.Root>
	</Popover.Content>
</Popover.Root>
