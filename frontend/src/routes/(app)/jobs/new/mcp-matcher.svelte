<script lang="ts">
	import type { JobMcpNeed, McpServer } from '$lib/api/types';
	import { Badge } from '$lib/components/ui/badge';
	import { Checkbox } from '$lib/components/ui/checkbox';
	import * as Field from '$lib/components/ui/field';
	import { transportLabel } from '$lib/utils/mcp-util';
	import { cn } from '$lib/utils/style';
	import CircleCheckIcon from '@lucide/svelte/icons/circle-check';
	import TriangleAlertIcon from '@lucide/svelte/icons/triangle-alert';

	let {
		needs,
		servers,
		compiled = true,
		selected = $bindable([])
	}: {
		// The services the compiled spec says the job talks to
		needs: JobMcpNeed[];
		// Whether the spec came from the compiler, since one filled in by hand says nothing about the services the job needs
		compiled?: boolean;
		// Every MCP server configured in the workspace
		servers: McpServer[];
		// IDs of the servers to attach to the new job
		selected?: string[];
	} = $props();

	function matchFor(need: JobMcpNeed) {
		return servers.find((s) => s.name.toLowerCase() === need.server.toLowerCase());
	}

	// The compiler writes the reason as a lowercase fragment, which reads as a sentence once capitalized
	function sentence(text: string) {
		return text.charAt(0).toUpperCase() + text.slice(1);
	}

	function toggle(id: string, checked: boolean) {
		selected = checked ? [...selected, id] : selected.filter((s) => s !== id);
	}
</script>

<div class="flex flex-col gap-6">
	<!-- Only a compiled spec lists the services the job needs, so a spec filled in by hand makes no claim about them either way -->
	{#if compiled && needs.length > 0}
		<ul class="flex flex-col gap-2" aria-label="Services the job needs">
			{#each needs as need, i (i)}
				{@const match = matchFor(need)}
				<li class="bg-muted/40 flex items-start gap-3 rounded-lg px-3 py-2.5">
					{#if match}
						<CircleCheckIcon class="text-success mt-0.5 size-4 shrink-0" />
					{:else}
						<TriangleAlertIcon class="text-warning-foreground mt-0.5 size-4 shrink-0" />
					{/if}
					<div class="flex min-w-0 flex-col gap-0.5">
						<span class="flex flex-wrap items-center gap-2">
							<span class="text-sm font-medium">{need.server}</span>
							{#if match}
								<Badge variant="secondary">Configured</Badge>
							{:else}
								<Badge variant="outline">Not configured</Badge>
							{/if}
						</span>
						{#if need.why}
							<span class="text-muted-foreground text-sm">{sentence(need.why)}</span>
						{/if}
						{#if !match}
							<span class="text-muted-foreground text-xs">
								Add a server named <span class="font-mono">{need.server}</span> on the
								<a href="/mcp" target="_blank" class="underline underline-offset-3">MCP servers</a> page,
								then attach it in the job's settings.
							</span>
						{/if}
					</div>
				</li>
			{/each}
		</ul>
	{:else if compiled}
		<p class="text-muted-foreground text-sm">The job doesn't need any external services</p>
	{/if}

	{#if servers.length > 0}
		<!-- Without needs above them, the card's own description already says what attaching does, so the heading only names the group for screen readers -->
		<Field.Set>
			<Field.Legend variant="label" class={cn(!compiled && 'sr-only')}>Attach servers</Field.Legend>
			{#if compiled}
				<Field.Description>
					The job's agent and scripts can use the tools of attached servers.
				</Field.Description>
			{/if}
			<Field.Group variant="choices">
				{#each servers as server (server.id)}
					<Field.Field orientation="horizontal">
						<Checkbox
							id="attach-{server.id}"
							checked={selected.includes(server.id)}
							onCheckedChange={(checked) => toggle(server.id, checked)}
						/>
						<Field.Label for="attach-{server.id}" variant="choice">
							{server.name}
							<Badge variant="outline">{transportLabel(server.transport)}</Badge>
							{#if !server.enabled}
								<Badge variant="secondary">Disabled</Badge>
							{/if}
							{#if server.toolsCachedAt}
								<span class="text-muted-foreground text-xs">
									{server.tools?.length ?? 0}
									{server.tools?.length === 1 ? 'tool' : 'tools'}
								</span>
							{/if}
						</Field.Label>
					</Field.Field>
				{/each}
			</Field.Group>
		</Field.Set>
	{:else}
		<p class="text-muted-foreground text-sm">
			No MCP servers are configured yet. <a href="/mcp" class="underline underline-offset-3"
				>Add one</a
			> to give jobs more tools.
		</p>
	{/if}
</div>
