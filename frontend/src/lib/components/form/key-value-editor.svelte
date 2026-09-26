<script lang="ts" module>
	export type KeyValueEntry = { key: string; value: string };

	// Turns editable rows into the map the API stores, dropping rows without a key
	export function entriesToRecord(entries: KeyValueEntry[]): Record<string, string> {
		return Object.fromEntries(
			entries.filter((e) => e.key.trim() !== '').map((e) => [e.key.trim(), e.value])
		);
	}

	export function recordToEntries(record: Record<string, string> | null | undefined) {
		return Object.entries(record ?? {}).map(([key, value]) => ({ key, value }));
	}
</script>

<script lang="ts">
	import { Button } from '$lib/components/ui/button';
	import * as DropdownMenu from '$lib/components/ui/dropdown-menu';
	import { Input } from '$lib/components/ui/input';
	import * as InputGroup from '$lib/components/ui/input-group';
	import KeyRoundIcon from '@lucide/svelte/icons/key-round';
	import PlusIcon from '@lucide/svelte/icons/plus';
	import XIcon from '@lucide/svelte/icons/x';

	let {
		entries = $bindable([]),
		label,
		keyPlaceholder = 'KEY',
		valuePlaceholder = 'value',
		addLabel = 'Add',
		secrets
	}: {
		entries?: KeyValueEntry[];
		// Names a row for screen readers, e.g. "Header"
		label: string;
		keyPlaceholder?: string;
		valuePlaceholder?: string;
		addLabel?: string;
		// Secret names offered by the "Insert secret" menu, which is hidden when this is undefined
		secrets?: string[];
	} = $props();

	function add() {
		entries = [...entries, { key: '', value: '' }];
	}

	function remove(index: number) {
		entries = entries.filter((_, i) => i !== index);
	}

	// A reference is appended rather than replacing the value, so prefixes like "Bearer " survive
	function insertSecret(index: number, name: string) {
		entries[index].value = `${entries[index].value}{{secret:${name}}}`;
	}
</script>

<div class="flex flex-col gap-2">
	{#if entries.length > 0}
		<ul class="flex flex-col gap-2">
			{#each entries as entry, i (i)}
				<li class="grid grid-cols-[1fr_auto] gap-2 sm:grid-cols-[minmax(0,2fr)_minmax(0,3fr)_auto]">
					<Input
						bind:value={entry.key}
						placeholder={keyPlaceholder}
						class="font-mono"
						aria-label="{label} {i + 1} name"
					/>
					<InputGroup.Root class="order-last col-span-2 sm:order-none sm:col-span-1">
						<InputGroup.Input
							bind:value={entry.value}
							placeholder={valuePlaceholder}
							class="font-mono"
							aria-label="{label} {i + 1} value"
						/>
						{#if secrets}
							<InputGroup.Addon align="inline-end">
								<DropdownMenu.Root>
									<DropdownMenu.Trigger>
										{#snippet child({ props })}
											<InputGroup.Button
												{...props}
												size="icon-xs"
												aria-label="Insert a secret into {label.toLowerCase()} {i + 1}"
												title="Insert a secret"
											>
												<KeyRoundIcon />
											</InputGroup.Button>
										{/snippet}
									</DropdownMenu.Trigger>
									<DropdownMenu.Content align="end" class="max-h-72 w-56 overflow-y-auto">
										<DropdownMenu.Label>Insert secret</DropdownMenu.Label>
										{#each secrets as name (name)}
											<DropdownMenu.Item
												class="font-mono text-xs"
												onSelect={() => insertSecret(i, name)}
											>
												{name}
											</DropdownMenu.Item>
										{:else}
											<DropdownMenu.Item disabled>No secrets yet</DropdownMenu.Item>
										{/each}
										<DropdownMenu.Separator />
										<DropdownMenu.Item>
											{#snippet child({ props })}
												<a {...props} href="/settings/secrets">Manage secrets</a>
											{/snippet}
										</DropdownMenu.Item>
									</DropdownMenu.Content>
								</DropdownMenu.Root>
							</InputGroup.Addon>
						{/if}
					</InputGroup.Root>
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
