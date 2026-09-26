<script lang="ts">
	import type { Secret } from '$lib/api/types';
	import { openConfirmDialog } from '$lib/components/confirm-dialog';
	import { DataTable, renderComponent, renderSnippet } from '$lib/components/data-table';
	import RelativeTime from '$lib/components/relative-time.svelte';
	import { Button } from '$lib/components/ui/button';
	import * as Empty from '$lib/components/ui/empty';
	import SecretService from '$lib/services/secret-service';
	import { apiErrorToast } from '$lib/utils/error-util';
	import { tryCatch } from '$lib/utils/try-catch-util';
	import KeyRoundIcon from '@lucide/svelte/icons/key-round';
	import PencilIcon from '@lucide/svelte/icons/pencil';
	import PlusIcon from '@lucide/svelte/icons/plus';
	import Trash2Icon from '@lucide/svelte/icons/trash-2';
	import type { ColumnDef } from '@tanstack/table-core';
	import { toast } from 'svelte-sonner';
	import SecretDialog from './secret-dialog.svelte';

	const secretService = new SecretService();

	let dataTable: ReturnType<typeof DataTable<Secret>> | undefined = $state();
	let dialogOpen = $state(false);
	let editing = $state<Secret | null>(null);

	const columns: ColumnDef<Secret>[] = [
		{
			accessorKey: 'name',
			header: 'Name',
			meta: { sortKey: 'name', cellClass: 'font-mono font-medium' }
		},
		{
			accessorKey: 'createdAt',
			header: 'Created',
			meta: { sortKey: 'createdAt' },
			cell: ({ row }) => renderComponent(RelativeTime, { value: row.original.createdAt })
		},
		{
			accessorKey: 'updatedAt',
			header: 'Updated',
			meta: { sortKey: 'updatedAt' },
			cell: ({ row }) => renderComponent(RelativeTime, { value: row.original.updatedAt })
		},
		{
			id: 'actions',
			header: () => renderSnippet(srOnly, 'Actions'),
			meta: { headerClass: 'w-0', cellClass: 'w-0 text-right' },
			cell: ({ row }) => renderSnippet(actionsCell, row.original)
		}
	];

	function openCreate() {
		editing = null;
		dialogOpen = true;
	}

	function openUpdate(secret: Secret) {
		editing = secret;
		dialogOpen = true;
	}

	function onSaved(name: string, created: boolean) {
		toast.success(created ? `Created "${name}"` : `Updated "${name}"`);
		void dataTable?.refresh();
	}

	function confirmDelete(secret: Secret) {
		openConfirmDialog({
			title: `Delete ${secret.name}`,
			message:
				"Jobs and MCP servers that use this secret lose access to it right away. This can't be undone.",
			confirm: {
				label: 'Delete',
				destructive: true,
				action: async () => {
					const result = await tryCatch(secretService.delete(secret.id));
					if (result.error) {
						apiErrorToast(result.error, 'Failed to delete the secret');
						return;
					}
					toast.success(`Deleted "${secret.name}"`);
					await dataTable?.refresh();
				}
			}
		});
	}
</script>

{#snippet srOnly(text: string)}
	<span class="sr-only">{text}</span>
{/snippet}

{#snippet actionsCell(secret: Secret)}
	<div class="flex justify-end gap-1">
		<Button
			variant="ghost"
			size="sm"
			aria-label="Update {secret.name}"
			onclick={() => openUpdate(secret)}
		>
			<PencilIcon data-icon="inline-start" />
			Update value
		</Button>
		<Button
			variant="ghost"
			size="icon-sm"
			aria-label="Delete {secret.name}"
			onclick={() => confirmDelete(secret)}
		>
			<Trash2Icon />
		</Button>
	</div>
{/snippet}

<svelte:head>
	<title>Secrets · Umpteenth</title>
</svelte:head>

<div class="flex flex-col gap-4">
	<p class="text-muted-foreground text-sm">
		Secrets are passed to jobs as environment variables, and MCP servers reference them as
		<code class="font-mono text-xs">{'{{secret:NAME}}'}</code>. Values are write-only.
	</p>

	<DataTable
		bind:this={dataTable}
		label="Secrets"
		{columns}
		fetchPage={(query) => secretService.list(query)}
		getRowId={(secret) => secret.id}
		defaultSort="name"
		searchPlaceholder="Search secrets"
	>
		{#snippet actions()}
			<Button onclick={openCreate}>
				<PlusIcon data-icon="inline-start" />
				Create secret
			</Button>
		{/snippet}
		{#snippet empty()}
			<Empty.Root class="py-6">
				<Empty.Header>
					<Empty.Media variant="icon">
						<KeyRoundIcon />
					</Empty.Media>
					<Empty.Title>No secrets</Empty.Title>
					<Empty.Description>
						Store API keys and tokens once and use them in jobs and MCP servers.
					</Empty.Description>
				</Empty.Header>
			</Empty.Root>
		{/snippet}
	</DataTable>
</div>

<SecretDialog bind:open={dialogOpen} secret={editing} {onSaved} />
