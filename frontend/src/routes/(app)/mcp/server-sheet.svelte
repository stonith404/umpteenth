<script lang="ts">
	import type { McpServer } from '$lib/api/types';
	import RelativeTime from '$lib/components/relative-time.svelte';
	import { Badge } from '$lib/components/ui/badge';
	import { Button } from '$lib/components/ui/button';
	import * as Sheet from '$lib/components/ui/sheet';
	import { authDescription, canLogIn } from '$lib/utils/mcp-util';
	import LogInIcon from '@lucide/svelte/icons/log-in';
	import LogOutIcon from '@lucide/svelte/icons/log-out';
	import PencilIcon from '@lucide/svelte/icons/pencil';
	import PlugZapIcon from '@lucide/svelte/icons/plug-zap';
	import AuthBadge from './auth-badge.svelte';
	import ToolList from './tool-list.svelte';

	let {
		server = $bindable(null),
		onTest,
		onEdit,
		onLogIn,
		onLogOut
	}: {
		// The server to show, the sheet is open while it is set
		server: McpServer | null;
		onTest: (server: McpServer) => void;
		onEdit: (server: McpServer) => void;
		onLogIn: (server: McpServer) => void;
		onLogOut: (server: McpServer) => void;
	} = $props();

	const variables = $derived(
		Object.entries((server?.transport === 'http' ? server?.headers : server?.env) ?? {})
	);
</script>

<Sheet.Root open={server !== null} onOpenChange={(open) => !open && (server = null)}>
	<Sheet.Content class="w-full data-[side=right]:sm:max-w-2xl">
		{#if server}
			<Sheet.Header>
				<Sheet.Title class="flex flex-wrap items-center gap-2">
					<span class="font-mono">{server.name}</span>
					<Badge variant="outline" class="font-normal">{server.transport}</Badge>
					{#if !server.enabled}<Badge variant="secondary">Disabled</Badge>{/if}
				</Sheet.Title>
				<Sheet.Description>{server.description || 'No description'}</Sheet.Description>
			</Sheet.Header>
			<div class="flex min-h-0 flex-1 flex-col gap-6 overflow-y-auto px-4 pb-4">
				<section class="flex flex-col gap-2">
					<h3 class="text-sm font-medium">
						{server.transport === 'http' ? 'Endpoint' : 'Command'}
					</h3>
					<pre
						class="bg-muted/50 overflow-x-auto rounded-2xl px-4 py-3 font-mono text-xs">{server.transport ===
						'http'
							? server.url
							: [server.command, ...(server.args ?? [])].join(' ')}</pre>
				</section>
				{#if server.transport === 'http'}
					<section class="flex flex-col gap-2">
						<h3 class="text-sm font-medium">Authentication</h3>
						<div
							class="flex flex-wrap items-center justify-between gap-3 rounded-2xl border px-4 py-3"
						>
							<div class="flex min-w-0 flex-1 flex-col items-start gap-1.5">
								<span class="flex flex-wrap items-center gap-2">
									<AuthBadge auth={server.auth} />
									{#if server.auth.status === 'oauth' && server.auth.loggedInAt}
										<span class="text-muted-foreground text-xs">
											since <RelativeTime value={server.auth.loggedInAt} />
										</span>
									{/if}
								</span>
								<p class="text-muted-foreground text-xs">{authDescription(server.auth)}</p>
							</div>
							<div class="flex gap-2">
								{#if server.auth.loggedInAt}
									<Button variant="outline" size="sm" onclick={() => server && onLogOut(server)}>
										<LogOutIcon data-icon="inline-start" />
										Log out
									</Button>
								{/if}
								{#if canLogIn(server.auth)}
									<Button
										variant={server.auth.status === 'not_logged_in' ? 'default' : 'outline'}
										size="sm"
										onclick={() => server && onLogIn(server)}
									>
										<LogInIcon data-icon="inline-start" />
										{server.auth.status === 'oauth' ? 'Log in again' : 'Log in'}
									</Button>
								{/if}
							</div>
						</div>
					</section>
				{/if}
				{#if variables.length > 0}
					<section class="flex flex-col gap-2">
						<h3 class="text-sm font-medium">
							{server.transport === 'http' ? 'Headers' : 'Environment'}
						</h3>
						<dl class="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5 font-mono text-xs">
							{#each variables as [key, value] (key)}
								<dt class="text-muted-foreground">{key}</dt>
								<dd class="break-all">{value}</dd>
							{/each}
						</dl>
					</section>
				{/if}
				<section class="flex flex-col gap-2">
					<div class="flex items-baseline justify-between gap-2">
						<h3 class="text-sm font-medium">Tools</h3>
						{#if server.toolsCachedAt}
							<span class="text-muted-foreground text-xs">
								Tested <RelativeTime value={server.toolsCachedAt} />
							</span>
						{/if}
					</div>
					{#if server.toolsCachedAt}
						<ToolList tools={server.tools ?? []} />
					{:else}
						<p class="text-muted-foreground text-sm">
							Not tested yet. Test the server to see its tools.
						</p>
					{/if}
				</section>
			</div>
			<Sheet.Footer class="flex-row justify-end gap-2">
				<Button variant="outline" onclick={() => server && onEdit(server)}>
					<PencilIcon data-icon="inline-start" />
					Edit
				</Button>
				<Button onclick={() => server && onTest(server)}>
					<PlugZapIcon data-icon="inline-start" />
					Test
				</Button>
			</Sheet.Footer>
		{/if}
	</Sheet.Content>
</Sheet.Root>
