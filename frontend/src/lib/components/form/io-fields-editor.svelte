<script lang="ts">
	import type { JobIOField } from '$lib/api/types';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import * as Select from '$lib/components/ui/select';
	import { IO_FIELD_TYPES } from '$lib/utils/job-util';
	import PlusIcon from '@lucide/svelte/icons/plus';
	import XIcon from '@lucide/svelte/icons/x';

	let {
		fields = $bindable([]),
		label,
		addLabel = 'Add field'
	}: {
		fields?: JobIOField[];
		// Names a row for screen readers, e.g. "Output"
		label: string;
		addLabel?: string;
	} = $props();

	function add() {
		fields = [...fields, { name: '', type: 'string', description: '' }];
	}

	function remove(index: number) {
		fields = fields.filter((_, i) => i !== index);
	}
</script>

<!-- Rows follow the width of the card they sit in rather than the window, since the same editor sits next to a rail on wide screens -->
<!-- Wide rows put name, type and description in columns under a header, narrow ones stack the description under the name and type -->
<div class="flex flex-col gap-2 @container">
	{#if fields.length > 0}
		<div
			aria-hidden="true"
			class="text-muted-foreground hidden grid-cols-[minmax(8rem,12rem)_7.5rem_minmax(0,1fr)_2rem] gap-2 text-xs font-medium @xl:grid"
		>
			<span>Name</span>
			<span>Type</span>
			<span>Description</span>
		</div>
		<ul class="flex flex-col gap-3 @xl:gap-2">
			{#each fields as field, i (i)}
				<li
					class="grid grid-cols-[minmax(0,1fr)_7.5rem_auto] gap-2 not-last:border-b not-last:pb-3 @xl:grid-cols-[minmax(8rem,12rem)_7.5rem_minmax(0,1fr)_auto] @xl:not-last:border-b-0 @xl:not-last:pb-0"
				>
					<Input
						bind:value={field.name}
						placeholder="name"
						class="font-mono"
						aria-label="{label} {i + 1} name"
					/>
					<Select.Root type="single" bind:value={field.type}>
						<Select.Trigger class="w-full" aria-label="{label} {i + 1} type">
							{field.type}
						</Select.Trigger>
						<Select.Content align="start">
							{#each IO_FIELD_TYPES as type (type)}
								<Select.Item value={type} label={type} />
							{/each}
						</Select.Content>
					</Select.Root>
					<Input
						bind:value={field.description}
						placeholder="What it holds"
						class="order-last col-span-3 @xl:order-none @xl:col-span-1"
						aria-label="{label} {i + 1} description"
					/>
					<Button
						variant="ghost"
						size="icon-sm"
						class="self-center"
						aria-label="Remove {label.toLowerCase()} {i + 1}"
						onclick={() => remove(i)}
					>
						<XIcon />
					</Button>
				</li>
			{/each}
		</ul>
	{/if}
	<Button variant="outline" size="sm" class="w-fit" onclick={add}>
		<PlusIcon data-icon="inline-start" />
		{addLabel}
	</Button>
</div>
