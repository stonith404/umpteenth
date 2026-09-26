<script lang="ts">
	import type { AdminWorkspace } from '$lib/api/types';
	import { openConfirmDialog } from '$lib/components/confirm-dialog';
	import {
		DataTable,
		RowActions,
		actionsColumn,
		renderComponent,
		renderSnippet
	} from '$lib/components/data-table';
	import RelativeTime from '$lib/components/relative-time.svelte';
	import { Badge } from '$lib/components/ui/badge';
	import * as Empty from '$lib/components/ui/empty';
	import AdminService from '$lib/services/admin-service';
	import WorkspaceService from '$lib/services/workspace-service';
	import { apiErrorToast } from '$lib/utils/error-util';
	import { tryCatch } from '$lib/utils/try-catch-util';
	import { enterWorkspace, workspaceInitial } from '$lib/utils/workspace-util';
	import LayersIcon from '@lucide/svelte/icons/layers';
	import LogInIcon from '@lucide/svelte/icons/log-in';
	import Trash2Icon from '@lucide/svelte/icons/trash-2';
	import type { ColumnDef } from '@tanstack/table-core';
	import { toast } from 'svelte-sonner';

	let { data } = $props();

	const adminService = new AdminService();
	const workspaceService = new WorkspaceService();

	let dataTable: ReturnType<typeof DataTable<AdminWorkspace>> | undefined = $state();

	const columns: ColumnDef<AdminWorkspace>[] = [
		{
			accessorKey: 'name',
			header: 'Workspace',
			meta: { sortKey: 'name', cellClass: 'w-full max-w-0' },
			cell: ({ row }) => renderSnippet(nameCell, row.original)
		},
		{
			id: 'owner',
			header: 'Owner',
			meta: { hideBelow: 'md', cellClass: 'max-w-56' },
			cell: ({ row }) => renderSnippet(ownerCell, row.original)
		},
		{
			accessorKey: 'memberCount',
			header: 'Members',
			meta: { sortKey: 'members', hideBelow: 'lg', align: 'right', cellClass: 'numeric' }
		},
		{
			accessorKey: 'createdAt',
			header: 'Created',
			meta: { sortKey: 'createdAt', hideBelow: 'xl', cellClass: 'whitespace-nowrap' },
			cell: ({ row }) => renderComponent(RelativeTime, { value: row.original.createdAt })
		},
		actionsColumn<AdminWorkspace>((workspace) => renderSnippet(actionsCell, workspace))
	];

	// Instance admins can open any workspace, with the owner's rights, without joining it
	async function open(workspace: AdminWorkspace) {
		const result = await tryCatch(workspaceService.switchTo(workspace.id));
		if (result.error) {
			apiErrorToast(result.error, 'Failed to open the workspace');
			return;
		}
		await enterWorkspace();
	}

	function confirmDelete(workspace: AdminWorkspace) {
		openConfirmDialog({
			title: `Delete ${workspace.name}`,
			message:
				"Every job, run, secret, provider and MCP server in the workspace is deleted, and its members lose access. This can't be undone.",
			// Deleting a whole workspace asks for its name, so it can't be confirmed by reflex
			confirmText: workspace.name,
			confirm: {
				label: 'Delete workspace',
				destructive: true,
				action: async () => {
					const result = await tryCatch(adminService.deleteWorkspace(workspace.id));
					if (result.error) {
						apiErrorToast(result.error, 'Failed to delete the workspace');
						return;
					}
					toast.success(`Deleted ${workspace.name}`);

					// Deleting the workspace the session was in moved the session to another one
					if (workspace.id === data.user.workspace.id) await enterWorkspace();
					else await dataTable?.refresh();
				}
			}
		});
	}
</script>

{#snippet nameCell(workspace: AdminWorkspace)}
	<div class="flex min-w-0 items-center gap-3">
		<span
			class="bg-fill text-fill-foreground flex size-7 shrink-0 items-center justify-center rounded-md text-xs font-semibold"
			aria-hidden="true"
		>
			{workspaceInitial(workspace.name)}
		</span>
		<span class="truncate font-medium" title={workspace.name}>{workspace.name}</span>
		{#if workspace.id === data.user.workspace.id}
			<Badge variant="secondary">Current</Badge>
		{/if}
	</div>
{/snippet}

{#snippet ownerCell(workspace: AdminWorkspace)}
	{#if workspace.ownerId}
		<span class="flex min-w-0 flex-col">
			<span class="truncate">{workspace.ownerName || workspace.ownerEmail}</span>
			{#if workspace.ownerName && workspace.ownerEmail}
				<span class="text-muted-foreground truncate text-sm">{workspace.ownerEmail}</span>
			{/if}
		</span>
	{:else}
		<span class="text-muted-foreground">No owner yet</span>
	{/if}
{/snippet}

<!-- Opening is the way in, and the session's own workspace needs none -->
{#snippet actionsCell(workspace: AdminWorkspace)}
	<RowActions
		name={workspace.name}
		inline={workspace.id !== data.user.workspace.id && {
			label: 'Open',
			icon: LogInIcon,
			onSelect: () => open(workspace)
		}}
		items={[
			{
				label: 'Delete',
				icon: Trash2Icon,
				variant: 'destructive',
				onSelect: () => confirmDelete(workspace)
			}
		]}
	/>
{/snippet}

<div class="flex flex-col gap-4">
	<p class="text-muted-foreground text-sm leading-snug">
		Every workspace on this instance. Open one to work in it with the owner's rights, without
		joining it.
	</p>

	<DataTable
		bind:this={dataTable}
		label="Workspaces"
		{columns}
		fetchPage={(query) => adminService.listWorkspaces(query)}
		getRowId={(workspace) => workspace.id}
		defaultSort="name"
		searchPlaceholder="Search workspaces"
	>
		{#snippet empty()}
			<Empty.Root size="sm">
				<Empty.Header>
					<Empty.Media variant="icon">
						<LayersIcon />
					</Empty.Media>
					<Empty.Title>No workspaces found</Empty.Title>
				</Empty.Header>
			</Empty.Root>
		{/snippet}
	</DataTable>
</div>
