<script lang="ts">
	import type { ApiToken, ApiTokenCreated } from '$lib/api/types';
	import { openConfirmDialog } from '$lib/components/confirm-dialog';
	import { DataTable, renderComponent, renderSnippet } from '$lib/components/data-table';
	import RelativeTime from '$lib/components/relative-time.svelte';
	import { Badge } from '$lib/components/ui/badge';
	import { Button } from '$lib/components/ui/button';
	import * as Empty from '$lib/components/ui/empty';
	import ApiTokenService from '$lib/services/api-token-service';
	import { relativeTimeClock } from '$lib/utils/clock.svelte';
	import { apiErrorToast } from '$lib/utils/error-util';
	import { formatDate, formatDateTime } from '$lib/utils/format-util';
	import { tryCatch } from '$lib/utils/try-catch-util';
	import type { ColumnDef } from '@tanstack/table-core';
	import KeyRoundIcon from '@lucide/svelte/icons/key-round';
	import PlusIcon from '@lucide/svelte/icons/plus';
	import Trash2Icon from '@lucide/svelte/icons/trash-2';
	import { toast } from 'svelte-sonner';
	import CreateTokenDialog from './create-token-dialog.svelte';
	import TokenCreatedDialog from './token-created-dialog.svelte';

	const apiTokenService = new ApiTokenService();

	let dataTable: ReturnType<typeof DataTable<ApiToken>> | undefined = $state();
	let createOpen = $state(false);
	let created = $state<ApiTokenCreated | null>(null);

	const columns: ColumnDef<ApiToken>[] = [
		{
			accessorKey: 'name',
			header: 'Name',
			meta: { sortKey: 'name', cellClass: 'font-medium' }
		},
		{
			accessorKey: 'createdAt',
			header: 'Created',
			meta: { sortKey: 'createdAt' },
			cell: ({ row }) => renderComponent(RelativeTime, { value: row.original.createdAt })
		},
		{
			accessorKey: 'lastUsedAt',
			header: 'Last used',
			meta: { sortKey: 'lastUsedAt' },
			cell: ({ row }) => renderSnippet(lastUsedCell, row.original)
		},
		{
			accessorKey: 'expiresAt',
			header: 'Expires',
			meta: { sortKey: 'expiresAt' },
			cell: ({ row }) => renderSnippet(expiresCell, row.original)
		},
		{
			id: 'actions',
			header: () => renderSnippet(srOnly, 'Actions'),
			meta: { headerClass: 'w-0', cellClass: 'w-0 text-right' },
			cell: ({ row }) => renderSnippet(actionsCell, row.original)
		}
	];

	function onCreated(result: ApiTokenCreated) {
		created = result;
		void dataTable?.refresh();
	}

	function confirmDelete(token: ApiToken) {
		openConfirmDialog({
			title: 'Delete API token',
			message: `Automation that uses "${token.name}" stops working immediately. This can't be undone.`,
			confirm: {
				label: 'Delete',
				destructive: true,
				action: async () => {
					const result = await tryCatch(apiTokenService.delete(token.id));
					if (result.error) {
						apiErrorToast(result.error, 'Failed to delete the API token');
						return;
					}
					toast.success(`Deleted "${token.name}"`);
					await dataTable?.refresh();
				}
			}
		});
	}
</script>

{#snippet srOnly(text: string)}
	<span class="sr-only">{text}</span>
{/snippet}

{#snippet lastUsedCell(token: ApiToken)}
	{#if token.lastUsedAt}
		<RelativeTime value={token.lastUsedAt} />
	{:else}
		<span class="text-muted-foreground">Never</span>
	{/if}
{/snippet}

{#snippet expiresCell(token: ApiToken)}
	{#if token.expiresAt === null}
		<span class="text-muted-foreground">Never</span>
	{:else if token.expiresAt <= relativeTimeClock.now}
		<Badge variant="destructive" title={formatDateTime(token.expiresAt)}>Expired</Badge>
	{:else}
		<span title={formatDateTime(token.expiresAt)}>{formatDate(token.expiresAt)}</span>
	{/if}
{/snippet}

{#snippet actionsCell(token: ApiToken)}
	<Button
		variant="ghost"
		size="icon-sm"
		aria-label="Delete {token.name}"
		onclick={() => confirmDelete(token)}
	>
		<Trash2Icon />
	</Button>
{/snippet}

<svelte:head>
	<title>API tokens · Umpteenth</title>
</svelte:head>

<DataTable
	bind:this={dataTable}
	label="API tokens"
	{columns}
	fetchPage={(query) => apiTokenService.list(query)}
	getRowId={(token) => token.id}
	defaultSort="-createdAt"
	searchPlaceholder="Search tokens"
>
	{#snippet actions()}
		<Button onclick={() => (createOpen = true)}>
			<PlusIcon data-icon="inline-start" />
			Create token
		</Button>
	{/snippet}
	{#snippet empty()}
		<Empty.Root class="py-6">
			<Empty.Header>
				<Empty.Media variant="icon">
					<KeyRoundIcon />
				</Empty.Media>
				<Empty.Title>No API tokens</Empty.Title>
				<Empty.Description>
					Create a token to call the Umpteenth API from scripts and automation.
				</Empty.Description>
			</Empty.Header>
		</Empty.Root>
	{/snippet}
</DataTable>

<CreateTokenDialog bind:open={createOpen} {onCreated} />
<TokenCreatedDialog bind:created />
