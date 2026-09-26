<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import type { McpServer, McpServerBody } from '$lib/api/types';
	import { openConfirmDialog } from '$lib/components/confirm-dialog';
	import {
		DataTable,
		RowActions,
		actionsColumn,
		renderSnippet,
		type RowAction,
		type TableQuery
	} from '$lib/components/data-table';
	import PageHeader from '$lib/components/page-header.svelte';
	import RelativeTime from '$lib/components/relative-time.svelte';
	import { Badge } from '$lib/components/ui/badge';
	import { Button } from '$lib/components/ui/button';
	import * as Empty from '$lib/components/ui/empty';
	import { Switch } from '$lib/components/ui/switch';
	import McpService from '$lib/services/mcp-service';
	import { apiErrorToast } from '$lib/utils/error-util';
	import { canLogIn, transportLabel } from '$lib/utils/mcp-util';
	import { tryCatch } from '$lib/utils/try-catch-util';
	import GlobeIcon from '@lucide/svelte/icons/globe';
	import LogInIcon from '@lucide/svelte/icons/log-in';
	import LogOutIcon from '@lucide/svelte/icons/log-out';
	import PencilIcon from '@lucide/svelte/icons/pencil';
	import PlugZapIcon from '@lucide/svelte/icons/plug-zap';
	import PlusIcon from '@lucide/svelte/icons/plus';
	import ServerIcon from '@lucide/svelte/icons/server';
	import SquareTerminalIcon from '@lucide/svelte/icons/square-terminal';
	import ToggleLeftIcon from '@lucide/svelte/icons/toggle-left';
	import ToggleRightIcon from '@lucide/svelte/icons/toggle-right';
	import Trash2Icon from '@lucide/svelte/icons/trash-2';
	import type { ColumnDef } from '@tanstack/table-core';
	import { onMount } from 'svelte';
	import { MediaQuery } from 'svelte/reactivity';
	import { toast } from 'svelte-sonner';
	import AuthBadge from './auth-badge.svelte';
	import { startOAuthLogin } from './oauth-login';
	import ServerDialog from './server-dialog.svelte';
	import ServerSheet from './server-sheet.svelte';
	import ServerTestDialog from './server-test-dialog.svelte';

	const mcpService = new McpService();

	let dataTable: ReturnType<typeof DataTable<McpServer>> | undefined = $state();
	let dialogOpen = $state(false);
	let editing = $state<McpServer | null>(null);
	let testing = $state<McpServer | null>(null);
	let viewing = $state<McpServer | null>(null);
	let toggling = $state<Record<string, boolean>>({});
	let noServers = $state(false);

	// Tailwind's sm breakpoint, below which the auth and enabled columns make way for the name
	const wide = new MediaQuery('min-width: 40rem', true);

	// The transport shows as an icon next to the command or URL, and the filter still narrows by it
	// On phones only the name and the actions stay, the name cell then shows the auth and enabled states and the menu the switch
	const columns: ColumnDef<McpServer>[] = [
		{
			accessorKey: 'name',
			header: 'Name',
			meta: { sortKey: 'name', cellClass: 'w-full max-w-0' },
			cell: ({ row }) => renderSnippet(nameCell, row.original)
		},
		{
			id: 'auth',
			header: 'Auth',
			meta: { hideBelow: 'sm' },
			cell: ({ row }) => renderSnippet(authCell, row.original)
		},
		{
			id: 'target',
			header: 'Command or URL',
			meta: { hideBelow: 'xl', cellClass: 'max-w-72' },
			cell: ({ row }) => renderSnippet(targetCell, row.original)
		},
		{
			accessorKey: 'toolsCachedAt',
			header: 'Tools',
			meta: { sortKey: 'toolsCachedAt', hideBelow: 'md' },
			cell: ({ row }) => renderSnippet(toolsCell, row.original)
		},
		{
			accessorKey: 'enabled',
			header: 'Enabled',
			meta: { hideBelow: 'sm', headerClass: 'w-0', cellClass: 'w-0' },
			cell: ({ row }) => renderSnippet(enabledCell, row.original)
		},
		actionsColumn<McpServer>((server) => renderSnippet(actionsCell, server))
	];

	// Without any server the add button moves from the header into the empty panel, like on the jobs list
	async function fetchServers(query: TableQuery) {
		const result = await mcpService.list(query);
		noServers = result.total === 0 && !query.search && !query.transport;
		return result;
	}

	function openAdd() {
		editing = null;
		dialogOpen = true;
	}

	function openEdit(server: McpServer) {
		viewing = null;
		editing = server;
		dialogOpen = true;
	}

	function openTest(server: McpServer) {
		viewing = null;
		testing = server;
	}

	async function onSaved(server: McpServer, created: boolean) {
		toast.success(created ? `Added "${server.name}"` : `Saved "${server.name}"`);
		void dataTable?.refresh();
		if (!created) return;

		// A new server that advertises OAuth goes straight to its login, like codex mcp add does, and is tested once the browser comes back
		if (server.auth.status === 'not_logged_in') {
			toast.info(`Logging in to "${server.name}"`, {
				description: 'The server uses OAuth, so its login opens now.'
			});
			if (await startOAuthLogin(server)) return;
		}

		// Other new servers are tested right away, so their tools are known when attaching them to jobs
		testing = server;
	}

	async function logInTo(server: McpServer) {
		viewing = null;
		testing = null;
		await startOAuthLogin(server);
	}

	function confirmLogout(server: McpServer) {
		viewing = null;
		openConfirmDialog({
			title: `Log out of ${server.name}`,
			message: 'Jobs that use this server lose access to it until someone logs in again.',
			confirm: {
				label: 'Log out',
				destructive: true,
				action: async () => {
					const result = await tryCatch(mcpService.logout(server.id));
					if (result.error) {
						apiErrorToast(result.error, 'Failed to log out');
						return;
					}
					toast.success(`Logged out of "${server.name}"`);
					await dataTable?.refresh();
				}
			}
		});
	}

	// The OAuth callback sends the browser back here with the outcome, which is shown once and then dropped from the URL
	onMount(async () => {
		const params = page.url.searchParams;
		const serverId = params.get('server');
		const outcome = params.get('oauth');
		const error = params.get('oauthError');
		if (!serverId || (!outcome && error === null)) return;

		const url = new URL(page.url);
		for (const key of ['server', 'oauth', 'oauthError']) url.searchParams.delete(key);
		void goto(url, { replaceState: true, noScroll: true, keepFocus: true });

		const server = await tryCatch(mcpService.get(serverId));
		const name = server.data ? `"${server.data.name}"` : 'the server';
		if (error !== null) {
			toast.error(`Failed to log in to ${name}`, { description: error });
			return;
		}
		toast.success(`Logged in to ${name}`);

		// Testing right away lists the tools the login unlocked
		if (server.data) testing = server.data;
	});

	// The update endpoint replaces the whole server, so the switch sends every field back
	function bodyOf(server: McpServer): McpServerBody {
		return {
			name: server.name,
			description: server.description ?? undefined,
			transport: server.transport === 'http' ? 'http' : 'stdio',
			command: server.command ?? undefined,
			args: server.args ?? [],
			env: server.env,
			url: server.url ?? undefined,
			headers: server.headers,
			oauth: server.oauth,
			enabled: server.enabled
		};
	}

	async function setEnabled(server: McpServer, enabled: boolean) {
		toggling[server.id] = true;
		dataTable?.updateRow(server.id, { enabled });
		const result = await tryCatch(mcpService.update(server.id, { ...bodyOf(server), enabled }));
		toggling[server.id] = false;
		if (result.error) {
			dataTable?.updateRow(server.id, { enabled: !enabled });
			apiErrorToast(result.error, 'Failed to update the server');
			return;
		}
		dataTable?.updateRow(server.id, result.data);
	}

	function confirmDelete(server: McpServer) {
		openConfirmDialog({
			title: `Delete ${server.name}`,
			message: "Jobs that use this server lose access to its tools. This can't be undone.",
			confirm: {
				label: 'Delete',
				destructive: true,
				action: async () => {
					const result = await tryCatch(mcpService.delete(server.id));
					if (result.error) {
						apiErrorToast(result.error, 'Failed to delete the server');
						return;
					}
					toast.success(`Deleted "${server.name}"`);
					await dataTable?.refresh();
				}
			}
		});
	}

	// A server waiting for its login offers the login inline, every other server the test, and the menu holds the rest
	function serverActions(server: McpServer) {
		const needsLogin = server.auth.status === 'not_logged_in';
		const logIn: RowAction = {
			label: server.auth.status === 'oauth' ? 'Log in again' : 'Log in',
			icon: LogInIcon,
			onSelect: () => logInTo(server)
		};
		const test: RowAction = { label: 'Test', icon: PlugZapIcon, onSelect: () => openTest(server) };
		return {
			inline: needsLogin ? logIn : test,
			items: [
				{ label: 'Edit', icon: PencilIcon, onSelect: () => openEdit(server) },
				needsLogin ? test : canLogIn(server.auth) && logIn,
				!!server.auth.loggedInAt && {
					label: 'Log out',
					icon: LogOutIcon,
					onSelect: () => confirmLogout(server)
				},
				!wide.current && {
					label: server.enabled ? 'Disable' : 'Enable',
					icon: server.enabled ? ToggleLeftIcon : ToggleRightIcon,
					disabled: toggling[server.id],
					onSelect: () => setEnabled(server, !server.enabled)
				},
				{
					label: 'Delete',
					icon: Trash2Icon,
					variant: 'destructive',
					onSelect: () => confirmDelete(server)
				}
			] satisfies (RowAction | false)[]
		};
	}

	// A finished test updated the cached tools, which the row and an open detail sheet show
	async function onTested() {
		await dataTable?.refresh();
	}
</script>

{#snippet nameCell(server: McpServer)}
	{@const phoneBadges = server.auth.status !== 'unsupported' || !server.enabled}
	<div class="flex min-w-0 flex-col items-start">
		<!-- The whole row opens the sheet too, the button keeps it reachable by keyboard -->
		<button
			type="button"
			class="flex max-w-full text-left"
			title={server.name}
			onclick={() => (viewing = server)}
		>
			<span class="truncate font-medium link-underline">{server.name}</span>
		</button>
		{#if phoneBadges || server.description}
			<span class="flex max-w-full min-w-0 items-center gap-1.5">
				<!-- The states of the columns that phones leave out lead the second line there -->
				{#if phoneBadges}
					<span class="flex shrink-0 items-center gap-1.5 py-0.5 sm:hidden">
						{#if server.auth.status !== 'unsupported'}<AuthBadge auth={server.auth} />{/if}
						{#if !server.enabled}<Badge variant="secondary">Disabled</Badge>{/if}
					</span>
				{/if}
				{#if server.description}
					<span class="text-muted-foreground truncate text-sm" title={server.description}>
						{server.description}
					</span>
				{/if}
			</span>
		{/if}
	</div>
{/snippet}

{#snippet authCell(server: McpServer)}
	{#if server.auth.status === 'unsupported'}
		<span class="text-muted-foreground">None</span>
	{:else}
		<AuthBadge auth={server.auth} />
	{/if}
{/snippet}

{#snippet targetCell(server: McpServer)}
	{@const target =
		server.transport === 'http' ? server.url : [server.command, ...(server.args ?? [])].join(' ')}
	{@const Icon = server.transport === 'http' ? GlobeIcon : SquareTerminalIcon}
	<span class="text-muted-foreground flex min-w-0 items-center gap-2">
		<Icon class="size-4 shrink-0" aria-label={transportLabel(server.transport)} />
		<span class="truncate font-mono text-xs" title={target ?? undefined}>{target}</span>
	</span>
{/snippet}

{#snippet toolsCell(server: McpServer)}
	{#if server.toolsCachedAt}
		<span class="flex flex-col whitespace-nowrap">
			<span class="numeric">
				{server.tools?.length ?? 0}
				{server.tools?.length === 1 ? 'tool' : 'tools'}
			</span>
			<span class="text-muted-foreground text-sm">
				<RelativeTime value={server.toolsCachedAt} interactive={false} />
			</span>
		</span>
	{:else}
		<span class="text-muted-foreground" title="Not tested yet">—</span>
	{/if}
{/snippet}

{#snippet enabledCell(server: McpServer)}
	<Switch
		checked={server.enabled}
		disabled={toggling[server.id]}
		aria-label="{server.enabled ? 'Disable' : 'Enable'} {server.name}"
		onCheckedChange={(checked) => setEnabled(server, checked)}
	/>
{/snippet}

{#snippet addButton()}
	<Button onclick={openAdd}>
		<PlusIcon data-icon="inline-start" />
		Add MCP server
	</Button>
{/snippet}

{#snippet actionsCell(server: McpServer)}
	{@const actions = serverActions(server)}
	<RowActions name={server.name} inline={actions.inline} items={actions.items} />
{/snippet}

<svelte:head>
	<title>MCP servers · Umpteenth</title>
</svelte:head>

<PageHeader
	title="MCP servers"
	description="Tools your jobs can use through the Model Context Protocol."
	actions={noServers ? undefined : addButton}
/>

<DataTable
	bind:this={dataTable}
	label="MCP servers"
	{columns}
	fetchPage={fetchServers}
	getRowId={(server) => server.id}
	onRowClick={(server) => (viewing = server)}
	defaultSort="name"
	searchPlaceholder="Search servers"
	filters={[
		{
			key: 'transport',
			label: 'Transport',
			options: [
				{ value: 'stdio', label: 'stdio' },
				{ value: 'http', label: 'HTTP' }
			]
		}
	]}
>
	{#snippet empty()}
		<Empty.Root class="py-6">
			<Empty.Header>
				<Empty.Media variant="icon">
					<ServerIcon />
				</Empty.Media>
				<Empty.Title>No MCP servers yet</Empty.Title>
				<Empty.Description>
					Register stdio or HTTP MCP servers to give jobs access to more tools.
				</Empty.Description>
			</Empty.Header>
			<Empty.Content>
				{@render addButton()}
			</Empty.Content>
		</Empty.Root>
	{/snippet}
</DataTable>

<ServerDialog bind:open={dialogOpen} server={editing} {onSaved} />
<ServerTestDialog bind:server={testing} {onTested} onLogIn={logInTo} />
<ServerSheet
	bind:server={viewing}
	onTest={openTest}
	onEdit={openEdit}
	onLogIn={logInTo}
	onLogOut={confirmLogout}
/>
