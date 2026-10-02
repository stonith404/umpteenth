<script lang="ts">
	import type { McpServer } from '#lib/api/types.js';
	import RelativeTime from '#lib/components/relative-time.svelte';
	import { Badge } from '#lib/components/ui/badge/index.js';
	import { Button } from '#lib/components/ui/button/index.js';
	import * as Sheet from '#lib/components/ui/sheet/index.js';
	import { authDescription, canLogIn, transportLabel } from '#lib/utils/mcp-util.js';
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

	let content = $state<HTMLElement | null>(null);

	// A server waiting for its login can't be tested yet, so the login is the one primary action then
	const needsLogin = $derived(server?.auth.status === 'not_logged_in');

	// The first focusable element is often a relative time, whose exact-time tooltip would pop open on its own
	// Focusing the sheet itself still moves focus into it for screen readers, and Tab goes on to its first control
	function focusSheet(event: Event) {
		event.preventDefault();
		content?.focus();
	}

	const variables = $derived(
		Object.entries((server?.transport === 'http' ? server?.headers : server?.env) ?? {})
	);
</script>

<Sheet.Root open={server !== null} onOpenChange={(open) => !open && (server = null)}>
	<Sheet.Content
		bind:ref={content}
		tabindex={-1}
		focusable
		class="data-[side=right]:sm:max-w-2xl"
		onOpenAutoFocus={focusSheet}
	>
		{#if server}
			<Sheet.Header>
				<Sheet.Title>
					<span class="flex flex-wrap items-center gap-2">
						<!-- Names wrap after their dashes first, and split inside a word only when one alone is too long for the line -->
						<span class="min-w-0 wrap-anywhere">{server.name}</span>
						<Badge variant="outline">{transportLabel(server.transport)}</Badge>
						{#if !server.enabled}<Badge variant="secondary">Disabled</Badge>{/if}
					</span>
				</Sheet.Title>
				{#if server.description}
					<Sheet.Description>{server.description}</Sheet.Description>
				{/if}
			</Sheet.Header>
			<Sheet.Body>
				<section class="flex flex-col gap-2">
					<h3 class="text-sm font-medium">
						{server.transport === 'http' ? 'Endpoint' : 'Command'}
					</h3>
					<pre
						class="bg-muted/50 overflow-x-auto rounded-lg px-3 py-2.5 font-mono text-xs">{server.transport ===
						'http'
							? server.url
							: [server.command, ...(server.args ?? [])].join(' ')}</pre>
				</section>
				{#if server.transport === 'http'}
					<section class="flex flex-col gap-2">
						<h3 class="text-sm font-medium">Authentication</h3>
						<div
							class="flex flex-wrap items-center justify-between gap-3 rounded-lg border px-3 py-2.5"
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
										variant={needsLogin ? 'default' : 'outline'}
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
						<dl class="grid grid-cols-label-value gap-x-4 gap-y-1.5 font-mono text-xs">
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
			</Sheet.Body>
			<Sheet.Footer class="flex-row justify-end">
				<Button variant="outline" onclick={() => server && onEdit(server)}>
					<PencilIcon data-icon="inline-start" />
					Edit
				</Button>
				<Button
					variant={needsLogin ? 'outline' : 'default'}
					onclick={() => server && onTest(server)}
				>
					<PlugZapIcon data-icon="inline-start" />
					Test
				</Button>
			</Sheet.Footer>
		{/if}
	</Sheet.Content>
</Sheet.Root>
