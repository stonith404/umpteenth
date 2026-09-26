<script lang="ts">
	import type { JobServer, McpServer } from '$lib/api/types';
	import { Badge } from '$lib/components/ui/badge';
	import { Button } from '$lib/components/ui/button';
	import * as Card from '$lib/components/ui/card';
	import * as Select from '$lib/components/ui/select';
	import JobService from '$lib/services/job-service';
	import { trackUnsavedValue } from '$lib/utils/unsaved-changes-util.svelte';
	import ServerIcon from '@lucide/svelte/icons/server';
	import XIcon from '@lucide/svelte/icons/x';
	import ToolAllowList from './tool-allow-list.svelte';

	let {
		jobId,
		attached: initial,
		servers
	}: {
		jobId: string;
		attached: JobServer[];
		// Every MCP server of the workspace
		servers: McpServer[];
	} = $props();

	const jobService = new JobService();

	let attached = $state(initial.map(({ serverId, allowedTools }) => ({ serverId, allowedTools })));

	const serverById = $derived(new Map(servers.map((s) => [s.id, s])));
	const available = $derived(servers.filter((s) => !attached.some((a) => a.serverId === s.id)));

	// Attachments are saved through the unsaved-changes bar together with the rest of the settings
	trackUnsavedValue(
		() => attached,
		(value) => (attached = value),
		async (value) => {
			const saved = await jobService.setMcpServers(jobId, value);
			attached = (saved ?? []).map(({ serverId, allowedTools }) => ({ serverId, allowedTools }));
		}
	);

	function add(serverId: string) {
		attached = [...attached, { serverId, allowedTools: null }];
	}

	function remove(serverId: string) {
		attached = attached.filter((a) => a.serverId !== serverId);
	}
</script>

<Card.Root>
	<Card.Header>
		<Card.Title>MCP servers</Card.Title>
		<Card.Description>
			The job can call the tools of attached servers. Limit a server to some tools to keep the agent
			focused and safe.
		</Card.Description>
	</Card.Header>
	<Card.Content class="flex flex-col gap-4">
		{#if attached.length > 0}
			<ul class="flex flex-col gap-2" aria-label="Attached MCP servers">
				{#each attached as item (item.serverId)}
					{@const server = serverById.get(item.serverId)}
					<li class="bg-muted/40 flex flex-wrap items-center gap-3 rounded-2xl px-4 py-3">
						<ServerIcon class="text-muted-foreground size-4" />
						<div class="flex min-w-0 flex-1 flex-wrap items-center gap-2">
							<span class="font-mono text-sm font-medium">{server?.name ?? 'Unknown server'}</span>
							{#if server}<Badge variant="outline" class="font-normal">{server.transport}</Badge
								>{/if}
							{#if server && !server.enabled}<Badge variant="secondary">Disabled</Badge>{/if}
						</div>
						{#if server && server.toolsCachedAt && server.tools && server.tools.length > 0}
							<ToolAllowList
								tools={server.tools}
								serverName={server.name}
								bind:allowed={item.allowedTools}
							/>
						{:else}
							<span class="text-muted-foreground text-xs">
								{item.allowedTools === null ? 'All tools' : `${item.allowedTools.length} tools`} ·
								<a href="/mcp" class="underline underline-offset-3">test the server</a> to pick tools
							</span>
						{/if}
						<Button
							variant="ghost"
							size="icon-sm"
							aria-label="Detach {server?.name ?? 'server'}"
							onclick={() => remove(item.serverId)}
						>
							<XIcon />
						</Button>
					</li>
				{/each}
			</ul>
		{:else}
			<p class="text-muted-foreground text-sm">No servers attached.</p>
		{/if}

		{#if available.length > 0}
			<Select.Root type="single" value="" onValueChange={add}>
				<Select.Trigger class="w-full sm:w-64" aria-label="Attach a server">
					<span class="text-muted-foreground">Attach a server…</span>
				</Select.Trigger>
				<Select.Content>
					{#each available as server (server.id)}
						<Select.Item value={server.id} label={server.name}>
							<span class="font-mono">{server.name}</span>
							<span class="text-muted-foreground text-xs font-normal">{server.transport}</span>
						</Select.Item>
					{/each}
				</Select.Content>
			</Select.Root>
		{:else if servers.length === 0}
			<p class="text-muted-foreground text-sm">
				No MCP servers are configured yet. <a href="/mcp" class="underline underline-offset-3"
					>Add one</a
				> first.
			</p>
		{/if}
	</Card.Content>
</Card.Root>
