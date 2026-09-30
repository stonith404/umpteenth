<script lang="ts">
	import type { JobServer, McpServer } from '$lib/api/types';
	import FormCard from '$lib/components/form/form-card.svelte';
	import { Badge } from '$lib/components/ui/badge';
	import { Button } from '$lib/components/ui/button';
	import * as DropdownMenu from '$lib/components/ui/dropdown-menu';
	import JobService from '$lib/services/job-service';
	import { createForm } from '$lib/utils/form-util';
	import { mergeListChanges } from '$lib/utils/job-util';
	import { transportLabel } from '$lib/utils/mcp-util';
	import PlusIcon from '@lucide/svelte/icons/plus';
	import ServerIcon from '@lucide/svelte/icons/server';
	import XIcon from '@lucide/svelte/icons/x';
	import { z } from 'zod/v4';
	import LoadError from './load-error.svelte';
	import ToolAllowList from './tool-allow-list.svelte';

	let {
		jobId,
		attached: initial,
		servers,
		serversError = null
	}: {
		jobId: string;
		attached: JobServer[];
		// Every MCP server of the workspace, or null when they could not be listed
		servers: McpServer[] | null;
		serversError?: unknown;
	} = $props();

	const jobService = new JobService();

	const pick = ({ serverId, allowedTools }: JobServer) => ({ serverId, allowedTools });

	const form = createForm(
		z.object({
			attached: z.array(
				z.object({ serverId: z.string(), allowedTools: z.array(z.string()).nullable() })
			)
		}),
		{ attached: initial.map(pick) }
	);
	const inputs = form.inputs;
	const attached = $derived($inputs.attached.value);

	const serverById = $derived(new Map((servers ?? []).map((s) => [s.id, s])));
	const available = $derived(
		(servers ?? []).filter((s) => !attached.some((a) => a.serverId === s.id))
	);

	// The attachments carry their server's name, which keeps them readable when the workspace's servers failed to load
	const attachedNames = new Map(initial.map((s) => [s.serverId, s.serverName]));

	function add(serverId: string) {
		$inputs.attached.value = [...attached, { serverId, allowedTools: null }];
	}

	function remove(serverId: string) {
		$inputs.attached.value = attached.filter((a) => a.serverId !== serverId);
	}

	// The attachments as the card last loaded or saved them, which tells the card's own changes apart from ones saved elsewhere meanwhile
	let saved = initial.map(pick);

	// Saving replaces every attachment, so the card's changes are applied to the stored attachments rather than to the ones it loaded
	async function save(values: { attached: JobServer[] }) {
		const stored = (await jobService.getMcpServers(jobId)).map(pick);
		await jobService.setMcpServers(
			jobId,
			mergeListChanges(saved, values.attached, stored, (a) => a.serverId)
		);
		saved = values.attached;
	}
</script>

<FormCard
	title="MCP servers"
	description="The job can call the tools of attached servers. Limit a server to some tools to keep the agent focused and safe."
	dirty={form.isDirty()}
	saving={form.saving}
	onsubmit={() => form.submit(save)}
>
	<div class="flex flex-col gap-4">
		{#if serversError}
			<LoadError title="Couldn't load your MCP servers" error={serversError} />
		{/if}

		{#if attached.length > 0}
			<ul class="flex flex-col gap-2" aria-label="Attached MCP servers">
				{#each attached as item (item.serverId)}
					{@const server = serverById.get(item.serverId)}
					{@const name = server?.name ?? attachedNames.get(item.serverId) ?? 'Unknown server'}
					<!-- The tool choice sits after the name and wraps under it when the row gets narrow -->
					<li class="bg-muted/40 flex items-center gap-3 rounded-lg py-2 pr-2 pl-3">
						<ServerIcon class="text-muted-foreground size-4 shrink-0" />
						<div class="flex min-w-0 flex-1 flex-wrap items-center gap-x-3 gap-y-1.5">
							<div class="mr-auto flex max-w-full min-w-0 flex-wrap items-center gap-2">
								<span class="truncate text-sm font-medium">{name}</span>
								{#if server}<Badge variant="outline">{transportLabel(server.transport)}</Badge>{/if}
								{#if server && !server.enabled}<Badge variant="secondary">Disabled</Badge>{/if}
							</div>
							{#if server?.toolsCachedAt && server.tools?.length}
								<ToolAllowList
									tools={server.tools}
									serverName={name}
									bind:allowed={item.allowedTools}
								/>
							{:else}
								<span class="text-muted-foreground text-xs">
									{item.allowedTools === null ? 'All tools' : `${item.allowedTools.length} tools`}
									{#if server}
										· <a href="/mcp" class="underline underline-offset-3">test the server</a> to pick
										tools
									{/if}
								</span>
							{/if}
						</div>
						<Button
							variant="ghost"
							size="icon-sm"
							aria-label="Detach {name}"
							onclick={() => remove(item.serverId)}
						>
							<XIcon />
						</Button>
					</li>
				{/each}
			</ul>
		{:else if servers === null || servers.length > 0}
			<p class="text-muted-foreground text-sm">No servers attached</p>
		{:else}
			<p class="text-muted-foreground text-sm">
				No MCP servers are configured yet. <a href="/mcp" class="underline underline-offset-3"
					>Add one</a
				> first.
			</p>
		{/if}
	</div>

	{#snippet footer()}
		{#if available.length > 0}
			<!-- A menu rather than a select, since picking a server attaches it instead of setting a value -->
			<DropdownMenu.Root>
				<DropdownMenu.Trigger>
					{#snippet child({ props })}
						<Button {...props} variant="outline">
							<PlusIcon data-icon="inline-start" />
							Attach server
						</Button>
					{/snippet}
				</DropdownMenu.Trigger>
				<DropdownMenu.Content align="start" class="max-h-72 min-w-56">
					{#each available as server (server.id)}
						<DropdownMenu.Item onSelect={() => add(server.id)}>
							<span class="min-w-0 flex-1 truncate">{server.name}</span>
							<span class="text-muted-foreground text-xs">{transportLabel(server.transport)}</span>
						</DropdownMenu.Item>
					{/each}
				</DropdownMenu.Content>
			</DropdownMenu.Root>
		{/if}
	{/snippet}
</FormCard>
