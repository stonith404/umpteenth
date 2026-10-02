<script lang="ts">
	import type { McpToolInfo } from '#lib/api/types.js';
	import { Badge } from '#lib/components/ui/badge/index.js';
	import { Button } from '#lib/components/ui/button/index.js';
	import * as Command from '#lib/components/ui/command/index.js';
	import * as Popover from '#lib/components/ui/popover/index.js';
	import { cn } from '#lib/utils/style.js';
	import CheckIcon from '@lucide/svelte/icons/check';
	import ChevronDownIcon from '@lucide/svelte/icons/chevron-down';
	import MinusIcon from '@lucide/svelte/icons/minus';

	type CheckState = 'checked' | 'unchecked' | 'indeterminate';

	// Tool names never contain spaces, so this value cannot collide with a tool's
	const ALL_ITEM_VALUE = 'All tools';

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

	let open = $state(false);
	let query = $state('');

	const summary = $derived(
		allowed === null
			? 'All tools'
			: `${tools.filter((t) => isAllowed(t.name)).length} of ${tools.length} tools`
	);

	// Tools whose name or description holds every search term, with name matches first
	const matches = $derived.by(() => {
		const terms = normalize(query).split(' ').filter(Boolean);
		if (terms.length === 0) return tools;
		const byName: McpToolInfo[] = [];
		const byDescription: McpToolInfo[] = [];
		for (const tool of tools) {
			const name = normalize(tool.name);
			const text = `${name} ${normalize(tool.description ?? '')}`;
			if (terms.every((t) => name.includes(t))) byName.push(tool);
			else if (terms.every((t) => text.includes(t))) byDescription.push(tool);
		}
		return [...byName, ...byDescription];
	});

	// Without a search the top row stands for every tool, with one it stands for the matches
	const topState = $derived.by<CheckState>(() => {
		if (!query.trim()) {
			if (allowed === null) return 'checked';
			return tools.some((t) => isAllowed(t.name)) ? 'indeterminate' : 'unchecked';
		}
		const picked = matches.filter((t) => isAllowed(t.name)).length;
		if (picked === matches.length) return 'checked';
		return picked > 0 ? 'indeterminate' : 'unchecked';
	});

	// Underscores and dashes count as spaces, so "issue comment" finds add_issue_comment
	function normalize(value: string) {
		return value
			.toLowerCase()
			.replace(/[\s_-]+/g, ' ')
			.trim();
	}

	function isAllowed(name: string) {
		return allowed === null || allowed.includes(name);
	}

	function setAllowed(names: Iterable<string>) {
		const next = [...new Set(names)];

		// Picking every tool again means "all", which also covers tools the server adds later
		// Names of tools the server has since dropped may linger in the list, so the check looks for every current tool rather than comparing counts
		allowed = tools.every((t) => next.includes(t.name)) ? null : next;
	}

	function toggle(name: string) {
		const current = allowed ?? tools.map((t) => t.name);
		setAllowed(isAllowed(name) ? current.filter((n) => n !== name) : [...current, name]);
	}

	function toggleTop() {
		// Without a search the row switches between every tool and none
		if (!query.trim()) {
			allowed = allowed === null ? [] : null;
			return;
		}

		// With a search it picks the matches, or drops them once all of them are picked
		const current = allowed ?? tools.map((t) => t.name);
		const names = new Set(matches.map((t) => t.name));
		setAllowed(
			topState === 'checked' ? current.filter((n) => !names.has(n)) : [...current, ...names]
		);
	}
</script>

<!-- Drawn like the Checkbox component, ink when checked, since a real checkbox would steal the command item's keyboard handling -->
<!-- Its end margin widens the command item's gap a little, so the label sits as far from the box as next to a real checkbox -->
{#snippet checkMark(state: CheckState)}
	<span
		aria-hidden="true"
		class={cn(
			'mt-0.5 mr-0.5 flex size-4 shrink-0 items-center justify-center rounded-sm border shadow-xs [&>svg]:size-3.5',
			state === 'unchecked'
				? 'bg-card border-input'
				: 'bg-foreground border-foreground text-background'
		)}
	>
		{#if state === 'checked'}
			<CheckIcon />
		{:else if state === 'indeterminate'}
			<MinusIcon />
		{/if}
	</span>
{/snippet}

<Popover.Root
	bind:open
	onOpenChange={(isOpen) => {
		if (!isOpen) query = '';
	}}
>
	<Popover.Trigger>
		{#snippet child({ props })}
			<Button {...props} variant="outline" size="sm" aria-label="Tools of {serverName}: {summary}">
				{summary}
				<ChevronDownIcon data-icon="inline-end" />
			</Button>
		{/snippet}
	</Popover.Trigger>
	<Popover.Content align="end" fitScreen padding="none" class="w-96">
		<Command.Root shouldFilter={false} label="Tools of {serverName}">
			<Command.Input bind:value={query} placeholder="Search tools" />
			<Command.List variant="popover">
				{#if matches.length > 0}
					<Command.Item
						value={ALL_ITEM_VALUE}
						aria-checked={topState === 'indeterminate' ? 'mixed' : topState === 'checked'}
						onSelect={toggleTop}
						class="items-start"
					>
						{@render checkMark(topState)}
						{#if query.trim()}
							<span class="flex-1">All matching tools</span>
							<span class="text-muted-foreground self-center text-xs tabular-nums"
								>{matches.length}</span
							>
						{:else}
							<span class="flex min-w-0 flex-1 flex-col">
								All tools
								<span class="text-muted-foreground text-xs"
									>Includes tools the server adds later</span
								>
							</span>
						{/if}
					</Command.Item>
					<Command.Separator />
				{:else}
					<p class="text-muted-foreground px-2 py-6 text-center text-sm">
						No tools match “{query.trim()}”
					</p>
				{/if}
				{#each matches as tool (tool.name)}
					{@const checked = isAllowed(tool.name)}
					<Command.Item
						value={tool.name}
						aria-checked={checked}
						title={tool.description || undefined}
						onSelect={() => toggle(tool.name)}
						class="items-start"
					>
						{@render checkMark(checked ? 'checked' : 'unchecked')}
						<span class="flex min-w-0 flex-1 flex-col gap-0.5">
							<span class="flex items-start gap-2">
								<!-- Long names wrap after an underscore rather than inside a word -->
								<span class="min-w-0 flex-1 font-mono text-sm leading-5 wrap-anywhere">
									{#each tool.name.split(/(?<=_)/) as part, i (i)}{part}<wbr />{/each}
								</span>
								{#if tool.readOnly}
									<Badge variant="secondary">Read-only</Badge>
								{:else if tool.destructive}
									<Badge variant="destructive">Destructive</Badge>
								{/if}
							</span>
							{#if tool.description}
								<span class="text-muted-foreground line-clamp-2 text-xs">{tool.description}</span>
							{/if}
						</span>
					</Command.Item>
				{/each}
			</Command.List>
		</Command.Root>
	</Popover.Content>
</Popover.Root>
