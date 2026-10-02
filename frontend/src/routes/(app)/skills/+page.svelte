<script lang="ts">
	import type { Skill } from '$lib/api/types';
	import { openConfirmDialog } from '$lib/components/confirm-dialog';
	import {
		DataTable,
		RowActions,
		actionsColumn,
		renderSnippet,
		type TableQuery
	} from '$lib/components/data-table';
	import PageHeader from '$lib/components/page-header.svelte';
	import RelativeTime from '$lib/components/relative-time.svelte';
	import { Button } from '$lib/components/ui/button';
	import * as Empty from '$lib/components/ui/empty';
	import SkillService from '$lib/services/skill-service';
	import { apiErrorToast } from '$lib/utils/error-util';
	import { formatBytes } from '$lib/utils/format-util';
	import { tryCatch } from '$lib/utils/try-catch-util';
	import DownloadIcon from '@lucide/svelte/icons/download';
	import EyeIcon from '@lucide/svelte/icons/eye';
	import PlusIcon from '@lucide/svelte/icons/plus';
	import PuzzleIcon from '@lucide/svelte/icons/puzzle';
	import RefreshCwIcon from '@lucide/svelte/icons/refresh-cw';
	import Trash2Icon from '@lucide/svelte/icons/trash-2';
	import UploadIcon from '@lucide/svelte/icons/upload';
	import type { ColumnDef } from '@tanstack/table-core';
	import { toast } from 'svelte-sonner';
	import SkillDialog from './skill-dialog.svelte';
	import SkillSheet from './skill-sheet.svelte';

	const skillService = new SkillService();

	let dataTable: ReturnType<typeof DataTable<Skill>> | undefined = $state();
	let dialogOpen = $state(false);
	let replacing = $state<Skill | null>(null);
	let viewing = $state<Skill | null>(null);
	// Without any skill the add button moves from the header into the empty panel, like on the jobs list
	let noSkills = $state<boolean>();
	let updating = $state<Record<string, boolean>>({});

	const columns: ColumnDef<Skill>[] = [
		{
			accessorKey: 'name',
			header: 'Name',
			meta: { sortKey: 'name', cellClass: 'w-full max-w-0' },
			cell: ({ row }) => renderSnippet(nameCell, row.original)
		},
		{
			accessorKey: 'size',
			header: 'Size',
			meta: { sortKey: 'size', hideBelow: 'md' },
			cell: ({ row }) => renderSnippet(sizeCell, row.original)
		},
		{
			accessorKey: 'jobCount',
			header: 'Used by',
			meta: { hideBelow: 'sm' },
			cell: ({ row }) => renderSnippet(jobsCell, row.original)
		},
		{
			accessorKey: 'updatedAt',
			header: 'Updated',
			meta: { sortKey: 'updatedAt', hideBelow: 'lg' },
			cell: ({ row }) => renderSnippet(updatedCell, row.original)
		},
		actionsColumn<Skill>((skill) => renderSnippet(actionsCell, skill))
	];

	function openAdd() {
		replacing = null;
		dialogOpen = true;
	}

	function openReplace(skill: Skill) {
		viewing = null;
		replacing = skill;
		dialogOpen = true;
	}

	function onSaved(skills: Skill[], created: boolean) {
		if (skills.length > 1) toast.success(`Added ${skills.length} skills`);
		else if (skills[0])
			toast.success(created ? `Added "${skills[0].name}"` : `Replaced "${skills[0].name}"`);
		void dataTable?.refresh();
	}

	// Takes the current version from the link the skill was imported from
	async function updateFromLink(skill: Skill) {
		updating[skill.id] = true;
		const result = await tryCatch(skillService.reimport(skill.id));
		updating[skill.id] = false;
		if (result.error) {
			apiErrorToast(result.error, `Failed to update "${skill.name}"`);
			return;
		}
		if (result.data.contentHash === skill.contentHash) {
			toast.success(`"${skill.name}" is up to date`);
		} else {
			toast.success(`Updated "${skill.name}"`);
		}
		if (viewing?.id === skill.id) viewing = result.data;
		dataTable?.updateRow(skill.id, result.data);
	}

	// The rows on the current page, which the bulk delete reads the names and job counts of the selection from
	let shown: Skill[] = [];
	async function fetchSkills(query: TableQuery) {
		const result = await skillService.list(query);
		shown = result.items ?? [];
		return result;
	}

	function confirmDeleteMany(ids: string[]) {
		const count = ids.length === 1 ? '1 skill' : `${ids.length} skills`;
		const inUse = shown.filter((s) => ids.includes(s.id) && s.jobCount > 0).length;
		let used = '';
		if (inUse > 0 && inUse === ids.length) {
			used =
				ids.length === 1
					? 'Jobs use it and will run without it. '
					: 'Jobs use them and will run without them. ';
		} else if (inUse > 0) {
			used = `${inUse} of them ${inUse === 1 ? 'is' : 'are'} used by jobs, which will run without ${inUse === 1 ? 'it' : 'them'}. `;
		}
		openConfirmDialog({
			title: `Delete ${count}`,
			message: `${used}This can't be undone.`,
			confirm: {
				label: 'Delete',
				destructive: true,
				action: async () => {
					const result = await tryCatch(skillService.deleteMany(ids));
					if (result.error) {
						apiErrorToast(result.error, 'Failed to delete the skills');
						return;
					}

					// Skills someone else deleted meanwhile are gone anyway, so the toast only counts the ones this delete removed
					const deleted = result.data.deleted?.length ?? 0;
					toast.success(`Deleted ${deleted === 1 ? '1 skill' : `${deleted} skills`}`);
					if (viewing && ids.includes(viewing.id)) viewing = null;
					dataTable?.clearSelection();
					await dataTable?.refresh();
				}
			}
		});
	}

	function confirmDelete(skill: Skill) {
		const used =
			skill.jobCount > 0
				? `${skill.jobCount} ${skill.jobCount === 1 ? 'job uses' : 'jobs use'} this skill and will run without it. `
				: '';
		openConfirmDialog({
			title: `Delete ${skill.name}`,
			message: `${used}This can't be undone.`,
			confirm: {
				label: 'Delete',
				destructive: true,
				action: async () => {
					const result = await tryCatch(skillService.delete(skill.id));
					if (result.error) {
						apiErrorToast(result.error, 'Failed to delete the skill');
						return;
					}
					toast.success(`Deleted "${skill.name}"`);
					await dataTable?.refresh();
				}
			}
		});
	}
</script>

{#snippet nameCell(skill: Skill)}
	<div class="flex min-w-0 flex-col items-start">
		<!-- The whole row opens the sheet too, the button keeps it reachable by keyboard -->
		<button
			type="button"
			class="flex max-w-full text-left"
			title={skill.name}
			onclick={() => (viewing = skill)}
		>
			<span class="truncate font-medium link-underline">{skill.name}</span>
		</button>
		<span class="text-muted-foreground max-w-full truncate text-sm" title={skill.description}>
			{skill.description}
		</span>
	</div>
{/snippet}

{#snippet sizeCell(skill: Skill)}
	<span class="flex flex-col whitespace-nowrap">
		<span class="numeric">{formatBytes(skill.size)}</span>
		<span class="text-muted-foreground numeric text-sm">
			{skill.fileCount}
			{skill.fileCount === 1 ? 'file' : 'files'}
		</span>
	</span>
{/snippet}

{#snippet jobsCell(skill: Skill)}
	{#if skill.jobCount > 0}
		<span class="numeric whitespace-nowrap">
			{skill.jobCount}
			{skill.jobCount === 1 ? 'job' : 'jobs'}
		</span>
	{:else}
		<span class="text-muted-foreground">—</span>
	{/if}
{/snippet}

{#snippet updatedCell(skill: Skill)}
	<span class="text-muted-foreground whitespace-nowrap">
		<RelativeTime value={skill.updatedAt} interactive={false} />
	</span>
{/snippet}

{#snippet selectionActions(ids: string[])}
	<Button variant="destructive-outline" onclick={() => confirmDeleteMany(ids)}>
		<Trash2Icon data-icon="inline-start" />
		Delete
	</Button>
{/snippet}

{#snippet addButton()}
	<Button onclick={openAdd}>
		<PlusIcon data-icon="inline-start" />
		Add skill
	</Button>
{/snippet}

{#snippet actionsCell(skill: Skill)}
	<RowActions
		name={skill.name}
		inline={{ label: 'View', icon: EyeIcon, onSelect: () => (viewing = skill) }}
		items={[
			!!skill.sourceUrl && {
				label: 'Update from link',
				icon: RefreshCwIcon,
				disabled: updating[skill.id],
				onSelect: () => updateFromLink(skill)
			},
			{ label: 'Replace', icon: UploadIcon, onSelect: () => openReplace(skill) },
			{
				label: 'Download',
				icon: DownloadIcon,
				onSelect: () => window.location.assign(skillService.downloadUrl(skill.id))
			},
			{
				label: 'Delete',
				icon: Trash2Icon,
				variant: 'destructive',
				onSelect: () => confirmDelete(skill)
			}
		]}
	/>
{/snippet}

<svelte:head>
	<title>Skills · Umpteenth</title>
</svelte:head>

<PageHeader
	title="Skills"
	description="Instructions, scripts and resources that jobs' agents load when a task calls for them."
	actions={noSkills === false ? addButton : undefined}
/>

<DataTable
	bind:this={dataTable}
	label="Skills"
	{columns}
	fetchPage={fetchSkills}
	bind:isEmpty={noSkills}
	getRowId={(skill) => skill.id}
	selectable
	rowLabel={(skill) => skill.name}
	{selectionActions}
	onRowClick={(skill) => (viewing = skill)}
	defaultSort="name"
	searchPlaceholder="Search skills"
>
	{#snippet empty()}
		<Empty.Root size="sm">
			<Empty.Header>
				<Empty.Media variant="icon">
					<PuzzleIcon />
				</Empty.Media>
				<Empty.Title>No skills yet</Empty.Title>
				<Empty.Description>
					Upload a skill folder as a zip or add one from GitHub, then attach it to the jobs that
					need it.
				</Empty.Description>
			</Empty.Header>
			<Empty.Content>
				{@render addButton()}
			</Empty.Content>
		</Empty.Root>
	{/snippet}
</DataTable>

<SkillDialog bind:open={dialogOpen} skill={replacing} {onSaved} />
<SkillSheet
	bind:skill={viewing}
	onReplace={openReplace}
	onUpdate={updateFromLink}
	updating={viewing ? !!updating[viewing.id] : false}
/>
