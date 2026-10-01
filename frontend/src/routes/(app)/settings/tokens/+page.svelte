<script lang="ts">
	import { page } from '$app/state';
	import type { ApiToken, ApiTokenCreated } from '$lib/api/types';
	import { openConfirmDialog } from '$lib/components/confirm-dialog';
	import {
		DataTable,
		RowActions,
		actionsColumn,
		renderComponent,
		renderSnippet
	} from '$lib/components/data-table';
	import RelativeTime from '$lib/components/relative-time.svelte';
	import { Button } from '$lib/components/ui/button';
	import * as Empty from '$lib/components/ui/empty';
	import ApiTokenService from '$lib/services/api-token-service';
	import { apiErrorToast } from '$lib/utils/error-util';
	import { tryCatch } from '$lib/utils/try-catch-util';
	import { hasRole } from '$lib/utils/workspace-util';
	import type { ColumnDef } from '@tanstack/table-core';
	import KeyRoundIcon from '@lucide/svelte/icons/key-round';
	import PlusIcon from '@lucide/svelte/icons/plus';
	import Trash2Icon from '@lucide/svelte/icons/trash-2';
	import { toast } from 'svelte-sonner';
	import Expiry from '../expiry.svelte';
	import CreateTokenDialog from './create-token-dialog.svelte';
	import TokenCreatedDialog from './token-created-dialog.svelte';

	const apiTokenService = new ApiTokenService();

	let dataTable: ReturnType<typeof DataTable<ApiToken>> | undefined = $state();
	let createOpen = $state(false);
	let created = $state<ApiTokenCreated | null>(null);
	// Without any token the create button moves from the toolbar into the empty panel, like on the jobs list
	let noTokens = $state<boolean>();

	// Members only see their own tokens, so only admins, who see everyone's, need to know whose a token is
	const seesEveryToken = hasRole(page.data.user!, 'admin');

	const columns: ColumnDef<ApiToken>[] = [
		{
			accessorKey: 'name',
			header: 'Name',
			meta: { sortKey: 'name', cellClass: 'w-full max-w-0' },
			cell: ({ row }) => renderSnippet(nameCell, row.original)
		},
		...(seesEveryToken
			? [
					{
						accessorKey: 'createdBy',
						header: 'Created by',
						meta: { hideBelow: 'lg', cellClass: 'whitespace-nowrap' },
						cell: ({ row }) => row.original.createdBy ?? '—'
					} satisfies ColumnDef<ApiToken>
				]
			: []),
		{
			accessorKey: 'createdAt',
			header: 'Created',
			meta: { sortKey: 'createdAt', hideBelow: 'md', cellClass: 'whitespace-nowrap' },
			cell: ({ row }) => renderComponent(RelativeTime, { value: row.original.createdAt })
		},
		{
			accessorKey: 'lastUsedAt',
			header: 'Last used',
			meta: { sortKey: 'lastUsedAt', hideBelow: 'sm', cellClass: 'whitespace-nowrap' },
			cell: ({ row }) => renderSnippet(lastUsedCell, row.original)
		},
		{
			accessorKey: 'expiresAt',
			header: 'Expires',
			meta: { sortKey: 'expiresAt', cellClass: 'whitespace-nowrap' },
			cell: ({ row }) => renderComponent(Expiry, { value: row.original.expiresAt })
		},
		actionsColumn<ApiToken>((token) => renderSnippet(actionsCell, token))
	];

	function onCreated(result: ApiTokenCreated) {
		created = result;
		void dataTable?.refresh();
	}

	function confirmDelete(token: ApiToken) {
		openConfirmDialog({
			title: `Delete ${token.name}`,
			message:
				"Scripts and automation that use this token stop working immediately. This can't be undone.",
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

{#snippet nameCell(token: ApiToken)}
	<span class="block truncate font-medium" title={token.name}>{token.name}</span>
{/snippet}

{#snippet lastUsedCell(token: ApiToken)}
	{#if token.lastUsedAt}
		<RelativeTime value={token.lastUsedAt} />
	{:else}
		<span class="text-muted-foreground">Never</span>
	{/if}
{/snippet}

{#snippet createButton()}
	<Button onclick={() => (createOpen = true)}>
		<PlusIcon data-icon="inline-start" />
		Create API token
	</Button>
{/snippet}

{#snippet actionsCell(token: ApiToken)}
	<RowActions
		name={token.name}
		items={[
			{
				label: 'Delete',
				icon: Trash2Icon,
				variant: 'destructive',
				onSelect: () => confirmDelete(token)
			}
		]}
	/>
{/snippet}

<div class="flex flex-col gap-4">
	<p class="text-muted-foreground text-sm leading-snug">
		API tokens let scripts call the Umpteenth API with your role in this workspace.
		{seesEveryToken
			? 'Admins see every token of the workspace, members only their own.'
			: 'You see the tokens you created.'}
	</p>

	<DataTable
		bind:this={dataTable}
		label="API tokens"
		{columns}
		fetchPage={(query) => apiTokenService.list(query)}
		bind:isEmpty={noTokens}
		getRowId={(token) => token.id}
		defaultSort="-createdAt"
		searchPlaceholder="Search tokens"
		actions={noTokens === false ? createButton : undefined}
	>
		{#snippet empty()}
			<Empty.Root size="sm">
				<Empty.Header>
					<Empty.Media variant="icon">
						<KeyRoundIcon />
					</Empty.Media>
					<Empty.Title>No API tokens</Empty.Title>
					<Empty.Description>
						Create a token to call the Umpteenth API from scripts and automation.
					</Empty.Description>
				</Empty.Header>
				<Empty.Content>
					{@render createButton()}
				</Empty.Content>
			</Empty.Root>
		{/snippet}
	</DataTable>
</div>

<CreateTokenDialog bind:open={createOpen} {onCreated} />
<TokenCreatedDialog bind:created />
