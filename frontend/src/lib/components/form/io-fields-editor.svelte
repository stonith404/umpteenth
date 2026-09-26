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

<div class="flex flex-col gap-2">
	{#if fields.length > 0}
		<ul class="flex flex-col gap-2">
			{#each fields as field, i (i)}
				<li class="grid grid-cols-[1fr_auto] gap-2 sm:grid-cols-[10rem_8rem_1fr_auto]">
					<Input
						bind:value={field.name}
						placeholder="name"
						class="font-mono"
						aria-label="{label} {i + 1} name"
					/>
					<div class="order-last col-span-2 sm:order-none sm:col-span-1">
						<Select.Root type="single" bind:value={field.type}>
							<Select.Trigger class="w-full" aria-label="{label} {i + 1} type">
								{field.type}
							</Select.Trigger>
							<Select.Content>
								{#each IO_FIELD_TYPES as type (type)}
									<Select.Item value={type} label={type} />
								{/each}
							</Select.Content>
						</Select.Root>
					</div>
					<Input
						bind:value={field.description}
						placeholder="What it holds"
						class="order-last col-span-2 sm:order-none sm:col-span-1"
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
