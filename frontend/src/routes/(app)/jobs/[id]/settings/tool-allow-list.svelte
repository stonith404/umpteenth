<script lang="ts">
	import type { McpToolInfo } from '$lib/api/types';
	import { Badge } from '$lib/components/ui/badge';
	import { Button } from '$lib/components/ui/button';
	import { Checkbox } from '$lib/components/ui/checkbox';
	import { Label } from '$lib/components/ui/label';
	import * as Popover from '$lib/components/ui/popover';
	import { Separator } from '$lib/components/ui/separator';
	import ChevronDownIcon from '@lucide/svelte/icons/chevron-down';

	let {
		tools,
		allowed = $bindable(null),
		serverName
	}: {
		// The server's cached tools from its last test
		tools: McpToolInfo[];
		// Allowed tool names, or null for every tool including ones the server adds later
		allowed?: string[] | null;
		serverName: string;
	} = $props();

	const id = $props.id();

	const summary = $derived(
		allowed === null ? 'All tools' : `${allowed.length} of ${tools.length} tools`
	);

	function setAll(all: boolean) {
		allowed = all ? null : [];
	}

	function toggle(name: string, checked: boolean) {
		const current = allowed ?? tools.map((t) => t.name);
		const next = checked ? [...new Set([...current, name])] : current.filter((n) => n !== name);

		// Picking every tool again means "all", which also covers tools the server adds later
		allowed = next.length === tools.length ? null : next;
	}
</script>

<Popover.Root>
	<Popover.Trigger>
		{#snippet child({ props })}
			<Button {...props} variant="outline" size="sm" aria-label="Tools of {serverName}: {summary}">
				{summary}
				<ChevronDownIcon data-icon="inline-end" />
			</Button>
		{/snippet}
	</Popover.Trigger>
	<Popover.Content align="end" class="w-80 p-0">
		<div class="flex items-center gap-2 px-4 py-3">
			<Checkbox
				id="{id}-all"
				checked={allowed === null}
				onCheckedChange={(checked) => setAll(checked)}
			/>
			<Label for="{id}-all" class="flex-1">All tools</Label>
			<span class="text-muted-foreground text-xs">Includes tools added later</span>
		</div>
		<Separator />
		<ul class="flex max-h-72 flex-col gap-1 overflow-y-auto p-2">
			{#each tools as tool (tool.name)}
				<li class="hover:bg-muted/50 flex items-start gap-2 rounded-xl px-2 py-1.5">
					<Checkbox
						id="{id}-{tool.name}"
						class="mt-0.5"
						checked={allowed === null || allowed.includes(tool.name)}
						onCheckedChange={(checked) => toggle(tool.name, checked)}
					/>
					<Label for="{id}-{tool.name}" class="flex min-w-0 flex-1 flex-col items-start gap-0.5">
						<span class="flex flex-wrap items-center gap-1.5 font-mono text-xs">
							{tool.name}
							{#if tool.readOnly}<Badge variant="secondary" class="font-sans">read-only</Badge>{/if}
							{#if tool.destructive}<Badge variant="destructive" class="font-sans"
									>destructive</Badge
								>{/if}
						</span>
						{#if tool.description}
							<span class="text-muted-foreground line-clamp-2 text-xs font-normal">
								{tool.description}
							</span>
						{/if}
					</Label>
				</li>
			{/each}
		</ul>
	</Popover.Content>
</Popover.Root>
