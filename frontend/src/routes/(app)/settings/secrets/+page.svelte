<script lang="ts">
	import type { Secret } from '$lib/api/types';
	import { openConfirmDialog } from '$lib/components/confirm-dialog';
	import { DataTable, RowActions, actionsColumn, renderSnippet } from '$lib/components/data-table';
	import { Button } from '$lib/components/ui/button';
	import * as Empty from '$lib/components/ui/empty';
	import * as Tooltip from '$lib/components/ui/tooltip';
	import SecretService from '$lib/services/secret-service';
	import { relativeTimeClock } from '$lib/utils/clock.svelte';
	import { apiErrorToast } from '$lib/utils/error-util';
	import { formatDateTime, formatRelative } from '$lib/utils/format-util';
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
	// Without any secret the create button moves from the toolbar into the empty panel, like on the jobs list
	let noSecrets = $state<boolean>();

	// Values only change by replacing them, so the last update is the date that matters and the creation date sits in its tooltip
	const columns: ColumnDef<Secret>[] = [
		{
			accessorKey: 'name',
			header: 'Name',
			meta: { sortKey: 'name', cellClass: 'w-full max-w-0' },
			cell: ({ row }) => renderSnippet(nameCell, row.original)
		},
		{
			accessorKey: 'updatedAt',
			header: 'Last updated',
			meta: { sortKey: 'updatedAt', cellClass: 'whitespace-nowrap' },
			cell: ({ row }) => renderSnippet(updatedCell, row.original)
		},
		actionsColumn<Secret>((secret) => renderSnippet(actionsCell, secret))
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

{#snippet nameCell(secret: Secret)}
	<span class="block truncate font-mono font-medium" title={secret.name}>{secret.name}</span>
{/snippet}

{#snippet updatedCell(secret: Secret)}
	<Tooltip.Root>
		<Tooltip.Trigger>
			{#snippet child({ props })}
				<time {...props} datetime={new Date(secret.updatedAt).toISOString()} class="cursor-default">
					{formatRelative(secret.updatedAt, relativeTimeClock.now)}
				</time>
			{/snippet}
		</Tooltip.Trigger>
		<Tooltip.Content>
			<span class="flex flex-col">
				<span>Updated {formatDateTime(secret.updatedAt)}</span>
				<span class="text-muted-foreground">Created {formatDateTime(secret.createdAt)}</span>
			</span>
		</Tooltip.Content>
	</Tooltip.Root>
{/snippet}

{#snippet createButton()}
	<Button onclick={openCreate}>
		<PlusIcon data-icon="inline-start" />
		Create secret
	</Button>
{/snippet}

{#snippet actionsCell(secret: Secret)}
	<RowActions
		name={secret.name}
		items={[
			{ label: 'Update value', icon: PencilIcon, onSelect: () => openUpdate(secret) },
			{
				label: 'Delete',
				icon: Trash2Icon,
				variant: 'destructive',
				onSelect: () => confirmDelete(secret)
			}
		]}
	/>
{/snippet}

<div class="flex flex-col gap-4">
	<p class="text-muted-foreground text-sm leading-snug">
		Secrets are passed to jobs as environment variables, and MCP servers reference them as
		<code class="font-mono text-xs">{'{{secret:NAME}}'}</code>. Values are write-only.
	</p>

	<DataTable
		bind:this={dataTable}
		label="Secrets"
		{columns}
		fetchPage={(query) => secretService.list(query)}
		bind:isEmpty={noSecrets}
		getRowId={(secret) => secret.id}
		defaultSort="name"
		searchPlaceholder="Search secrets"
		actions={noSecrets === false ? createButton : undefined}
	>
		{#snippet empty()}
			<Empty.Root size="sm">
				<Empty.Header>
					<Empty.Media variant="icon">
						<KeyRoundIcon />
					</Empty.Media>
					<Empty.Title>No secrets</Empty.Title>
					<Empty.Description>
						Store API keys and tokens once and use them in jobs and MCP servers.
					</Empty.Description>
				</Empty.Header>
				<Empty.Content>
					{@render createButton()}
				</Empty.Content>
			</Empty.Root>
		{/snippet}
	</DataTable>
</div>

<SecretDialog bind:open={dialogOpen} secret={editing} {onSaved} />
