<script lang="ts">
	import type { JobStateEntry } from '$lib/api/types';
	import {
		actionsColumn,
		DataTable,
		renderComponent,
		renderSnippet,
		RowActions
	} from '$lib/components/data-table';
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
	import DeleteEntryDialog from './delete-entry-dialog.svelte';
	import StateEntryDialog from './state-entry-dialog.svelte';

	let { data } = $props();

	const jobService = new JobService();

	const job = $derived(data.job);

	let dataTable: ReturnType<typeof DataTable<JobStateEntry>> | undefined = $state();
	let dialogOpen = $state(false);
	let editing = $state<JobStateEntry | null>(null);
	let deleting = $state<JobStateEntry | null>(null);

	// The value is the data people come for, so it takes the width and the foreground colour, and the time recedes
	const columns: ColumnDef<JobStateEntry>[] = [
		{
			accessorKey: 'key',
			header: 'Key',
			meta: {
				sortKey: 'key',
				headerClass: 'sm:w-1/4',
				cellClass: 'max-w-32 sm:max-w-64 truncate font-mono text-xs'
			},
			cell: ({ row }) => renderSnippet(keyCell, row.original.key)
		},
		{
			accessorKey: 'value',
			header: 'Value',
			meta: { cellClass: 'min-w-32 max-w-0' },
			cell: ({ row }) => renderSnippet(valueCell, row.original.value)
		},
		{
			accessorKey: 'updatedAt',
			header: 'Updated',
			meta: {
				sortKey: 'updatedAt',
				hideBelow: 'sm',
				cellClass: 'text-muted-foreground w-0 whitespace-nowrap'
			},
			cell: ({ row }) =>
				renderComponent(RelativeTime, { value: row.original.updatedAt, interactive: false })
		},
		actionsColumn<JobStateEntry>((entry) => renderSnippet(actionsCell, entry))
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

	async function deleteEntry(entry: JobStateEntry) {
		const result = await tryCatch(jobService.deleteState(job.id, entry.key));
		if (result.error) {
			apiErrorToast(result.error, 'Failed to delete the state entry');
			return;
		}
		toast.success(`Deleted "${entry.key}"`);
		await dataTable?.refresh();
	}
</script>

{#snippet keyCell(key: string)}
	<span title={key}>{key}</span>
{/snippet}

{#snippet valueCell(value: string)}
	{#if value}
		<pre class="line-clamp-2 font-mono text-xs break-words whitespace-pre-wrap">{value}</pre>
	{:else}
		<span class="text-muted-foreground">Empty</span>
	{/if}
{/snippet}

<!-- A row opens its entry for editing, and the menu offers the same for keyboard users next to deleting it -->
{#snippet actionsCell(entry: JobStateEntry)}
	<RowActions
		name={entry.key}
		items={[
			{ label: 'Edit', icon: PencilIcon, onSelect: () => openEdit(entry) },
			{
				label: 'Delete',
				icon: Trash2Icon,
				variant: 'destructive',
				onSelect: () => (deleting = entry)
			}
		]}
	/>
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
	onRowClick={openEdit}
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
		<Empty.Root size="sm">
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

<StateEntryDialog
	bind:open={dialogOpen}
	jobId={job.id}
	entry={editing}
	{onSaved}
	onStale={() => dataTable?.refresh()}
/>
<DeleteEntryDialog bind:entry={deleting} onDelete={deleteEntry} />
