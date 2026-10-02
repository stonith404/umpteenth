<script lang="ts">
	import { Button } from '#lib/components/ui/button/index.js';
	import * as Command from '#lib/components/ui/command/index.js';
	import * as Popover from '#lib/components/ui/popover/index.js';
	import { cn } from '#lib/utils/style.js';
	import CheckIcon from '@lucide/svelte/icons/check';
	import ChevronDownIcon from '@lucide/svelte/icons/chevron-down';
	import type { TableFilter } from './types';

	let {
		filter,
		selected,
		onChange
	}: {
		filter: TableFilter;
		selected: string[];
		onChange: (values: string[]) => void;
	} = $props();

	// Short lists are quicker to scan than to search
	const SEARCH_THRESHOLD = 6;

	let open = $state(false);

	const selectedOptions = $derived(filter.options.filter((o) => selected.includes(o.value)));

	function toggle(value: string) {
		onChange(selected.includes(value) ? selected.filter((v) => v !== value) : [...selected, value]);
	}
</script>

<Popover.Root bind:open>
	<Popover.Trigger>
		{#snippet child({ props })}
			<!-- Reads like the select triggers next to it: the label, the selection, then a chevron -->
			<Button {...props} variant="outline" class="max-w-full shrink-0">
				{filter.label}
				<!-- One value shows by name, several as a count, after a hairline that keeps them apart from the label -->
				{#if selectedOptions.length > 0}
					<span class="bg-border h-4 w-px" aria-hidden="true"></span>
					<span class="truncate font-normal">
						{selectedOptions.length === 1
							? selectedOptions[0].label
							: `${selectedOptions.length} selected`}
					</span>
				{/if}
				<ChevronDownIcon data-icon="inline-end" class="text-muted-foreground" />
			</Button>
		{/snippet}
	</Popover.Trigger>
	<Popover.Content class="w-56" padding="none" align="start">
		<Command.Root>
			{#if filter.options.length > SEARCH_THRESHOLD}
				<Command.Input placeholder="Search {filter.label.toLowerCase()}" />
			{/if}
			<Command.List>
				<Command.Empty>No results</Command.Empty>
				<Command.Group>
					{#each filter.options as option (option.value)}
						{@const isSelected = selected.includes(option.value)}
						<Command.Item value={option.label} onSelect={() => toggle(option.value)}>
							<!-- Drawn like the Checkbox component, ink when checked, since a real checkbox would steal the command item's keyboard handling -->
							<div
								class={cn(
									'flex size-4 items-center justify-center rounded-sm border shadow-xs',
									isSelected
										? 'bg-foreground border-foreground text-background'
										: 'bg-card border-input [&_svg]:invisible'
								)}
							>
								<CheckIcon class="size-3.5" />
							</div>
							{#if option.icon}
								<option.icon class="text-muted-foreground" />
							{/if}
							<span>{option.label}</span>
						</Command.Item>
					{/each}
				</Command.Group>
				{#if selected.length > 0}
					<Command.Separator />
					<Command.Group>
						<Command.Item
							class="justify-center text-center"
							onSelect={() => {
								onChange([]);
								open = false;
							}}
						>
							Clear filter
						</Command.Item>
					</Command.Group>
				{/if}
			</Command.List>
		</Command.Root>
	</Popover.Content>
</Popover.Root>
