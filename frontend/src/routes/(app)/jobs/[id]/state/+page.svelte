<script lang="ts">
	import type { JobStateEntry } from '$lib/api/types';
	import { openConfirmDialog } from '$lib/components/confirm-dialog';
	import { DataTable, renderComponent, renderSnippet } from '$lib/components/data-table';
	import RelativeTime from '$lib/components/relative-time.svelte';
	import { Button } from '$lib/components/ui/button';
	import * as Empty from '$lib/components/ui/empty';
	import JobService from '$lib/services/job-service';
	import { apiErrorToast } from '$lib/utils/error-util';
	import { tryCatch } from '$lib/utils/try-catch-util';
	import DatabaseIcon from '@lucide/svelte/icons/database';
	import PencilIcon from '@lucide/svelte/icons/pencil';
	import PlusIcon from '@lucide/svelte/icons/plus';
	import Trash2Icon from '@lucide/svelte/icons/trash-2';
	import type { ColumnDef } from '@tanstack/table-core';
	import { toast } from 'svelte-sonner';
	import StateEntryDialog from './state-entry-dialog.svelte';

	let { data } = $props();

	const jobService = new JobService();

	const job = $derived(data.job);

	let dataTable: ReturnType<typeof DataTable<JobStateEntry>> | undefined = $state();
	let dialogOpen = $state(false);
	let editing = $state<JobStateEntry | null>(null);

	const columns: ColumnDef<JobStateEntry>[] = [
		{
			accessorKey: 'key',
			header: 'Key',
			meta: {
				sortKey: 'key',
				headerClass: 'w-1/4',
				cellClass: 'max-w-64 truncate font-mono text-xs'
			}
		},
		{
			accessorKey: 'value',
			header: 'Value',
			meta: { cellClass: 'max-w-0' },
			cell: ({ row }) => renderSnippet(valueCell, row.original.value)
		},
		{
			accessorKey: 'updatedAt',
			header: 'Updated',
			meta: { sortKey: 'updatedAt', headerClass: 'w-0', cellClass: 'w-0 whitespace-nowrap' },
			cell: ({ row }) => renderComponent(RelativeTime, { value: row.original.updatedAt })
		},
		{
			id: 'actions',
			header: () => renderSnippet(srOnly, 'Actions'),
			meta: { headerClass: 'w-0', cellClass: 'w-0 text-right' },
			cell: ({ row }) => renderSnippet(actionsCell, row.original)
		}
	];

	function openAdd() {
		editing = null;
		dialogOpen = true;
	}

	function openEdit(entry: JobStateEntry) {
		editing = entry;
		dialogOpen = true;
	}

	async function onSaved(key: string) {
		toast.success(`Saved "${key}"`);
		await dataTable?.refresh();
	}

	function confirmDelete(entry: JobStateEntry) {
		openConfirmDialog({
			title: 'Delete state entry',
			message: `Runs that rely on "${entry.key}" start without it. This can't be undone.`,
			confirm: {
				label: 'Delete',
				destructive: true,
				action: async () => {
					const result = await tryCatch(jobService.deleteState(job.id, entry.key));
					if (result.error) {
						apiErrorToast(result.error, 'Failed to delete the state entry');
						return;
					}
					toast.success(`Deleted "${entry.key}"`);
					await dataTable?.refresh();
				}
			}
		});
	}
</script>

{#snippet srOnly(text: string)}
	<span class="sr-only">{text}</span>
{/snippet}

{#snippet valueCell(value: string)}
	<pre
		class="text-muted-foreground line-clamp-3 font-mono text-xs whitespace-pre-wrap break-all">{value}</pre>
{/snippet}

{#snippet actionsCell(entry: JobStateEntry)}
	<div class="flex justify-end gap-1">
		<Button
			variant="ghost"
			size="icon-sm"
			aria-label="Edit {entry.key}"
			onclick={() => openEdit(entry)}
		>
			<PencilIcon />
		</Button>
		<Button
			variant="ghost"
			size="icon-sm"
			aria-label="Delete {entry.key}"
			onclick={() => confirmDelete(entry)}
		>
			<Trash2Icon />
		</Button>
	</div>
{/snippet}

<svelte:head>
	<title>State · {job.name} · Umpteenth</title>
</svelte:head>

<DataTable
	bind:this={dataTable}
	label="State"
	{columns}
	fetchPage={(query) => jobService.listState(job.id, query)}
	getRowId={(entry) => entry.key}
	defaultSort="key"
	searchPlaceholder="Search state"
>
	{#snippet actions()}
		<Button onclick={openAdd}>
			<PlusIcon data-icon="inline-start" />
			Add entry
		</Button>
	{/snippet}
	{#snippet empty()}
		<Empty.Root class="py-6">
			<Empty.Header>
				<Empty.Media variant="icon">
					<DatabaseIcon />
				</Empty.Media>
				<Empty.Title>No state yet</Empty.Title>
				<Empty.Description>
					Runs keep values here between runs, e.g. the last item they processed.
				</Empty.Description>
			</Empty.Header>
		</Empty.Root>
	{/snippet}
</DataTable>

<StateEntryDialog bind:open={dialogOpen} jobId={job.id} entry={editing} {onSaved} />
