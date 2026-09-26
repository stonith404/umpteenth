<script lang="ts">
	import { Badge } from '$lib/components/ui/badge';
	import { Button } from '$lib/components/ui/button';
	import * as Command from '$lib/components/ui/command';
	import * as Popover from '$lib/components/ui/popover';
	import { Separator } from '$lib/components/ui/separator';
	import { cn } from '$lib/utils/style';
	import CheckIcon from '@lucide/svelte/icons/check';
	import CirclePlusIcon from '@lucide/svelte/icons/circle-plus';
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

	let open = $state(false);

	const selectedOptions = $derived(filter.options.filter((o) => selected.includes(o.value)));

	function toggle(value: string) {
		onChange(selected.includes(value) ? selected.filter((v) => v !== value) : [...selected, value]);
	}
</script>

<Popover.Root bind:open>
	<Popover.Trigger>
		{#snippet child({ props })}
			<Button {...props} variant="outline" class="border-dashed">
				<CirclePlusIcon data-icon="inline-start" />
				{filter.label}
				{#if selectedOptions.length > 0}
					<Separator orientation="vertical" class="mx-1 h-4" />
					<Badge variant="secondary" class="rounded-sm px-1 font-normal lg:hidden">
						{selectedOptions.length}
					</Badge>
					<div class="hidden gap-1 lg:flex">
						{#if selectedOptions.length > 2}
							<Badge variant="secondary" class="rounded-sm px-1 font-normal">
								{selectedOptions.length} selected
							</Badge>
						{:else}
							{#each selectedOptions as option (option.value)}
								<Badge variant="secondary" class="rounded-sm px-1 font-normal">
									{option.label}
								</Badge>
							{/each}
						{/if}
					</div>
				{/if}
			</Button>
		{/snippet}
	</Popover.Trigger>
	<Popover.Content class="w-56 p-0" align="start">
		<Command.Root>
			<Command.Input placeholder={filter.label} />
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
