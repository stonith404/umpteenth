<script lang="ts">
	import { isApiError } from '$lib/api/api-error';
	import { invalidate } from '$app/navigation';
	import type { JobImage } from '$lib/api/types';
	import CodeEditor from '$lib/components/code/code-editor.svelte';
	import { openConfirmDialog } from '$lib/components/confirm-dialog';
	import {
		actionsColumn,
		DataTable,
		renderComponent,
		renderSnippet,
		RowActions
	} from '$lib/components/data-table';
	import ImageStatusBadge, {
		isImageBuilding
	} from '$lib/components/jobs/image-status-badge.svelte';
	import RelativeTime from '$lib/components/relative-time.svelte';
	import { Button } from '$lib/components/ui/button';
	import * as Card from '$lib/components/ui/card';
	import * as Empty from '$lib/components/ui/empty';
	import ImageService from '$lib/services/image-service';
	import PlaybookService from '$lib/services/playbook-service';
	import { apiErrorToast } from '$lib/utils/error-util';
	import { formatBytes, formatDateTime, formatDuration } from '$lib/utils/format-util';
	import { DEFAULT_IMAGE_TOOLS } from '$lib/utils/job-util';
	import { tryCatch } from '$lib/utils/try-catch-util';
	import { invalidateAfterNavigation, subscribeWorkspaceEvents } from '$lib/utils/workspace-events';
	import ContainerIcon from '@lucide/svelte/icons/container';
	import HammerIcon from '@lucide/svelte/icons/hammer';
	import PlusIcon from '@lucide/svelte/icons/plus';
	import RefreshCwIcon from '@lucide/svelte/icons/refresh-cw';
	import ScrollTextIcon from '@lucide/svelte/icons/scroll-text';
	import type { ColumnDef } from '@tanstack/table-core';
	import { onMount, tick, untrack } from 'svelte';
	import { toast } from 'svelte-sonner';
	import { dockerfileTemplate } from '../../dockerfile-template';
	import ImageSheet from './image-sheet.svelte';

	let { data } = $props();

	const POLL_MS = 2000;
	const EDITOR_ID = 'dockerfile-editor';

	const playbookService = new PlaybookService();
	const imageService = new ImageService();

	const job = $derived(data.job);

	// A Dockerfile replaces the job's base image rather than extending it, so the template starts from that image to keep what the job runs on
	const baseImage = $derived(job.image || data.defaultImage);
	const savedDockerfile = $derived(data.playbook.content.dockerfile ?? '');

	// The editor starts from the saved Dockerfile and keeps the user's text until they save it or leave the tab
	let dockerfile = $state(untrack(() => savedDockerfile));
	// The saved Dockerfile the user's text was written against, so a save can tell that a newer version changed it meanwhile, e.g. reflection
	let editedFrom = untrack(() => savedDockerfile);
	let saving = $state(false);
	let rebuilding = $state(false);
	let openImageId = $state<string | null>(null);

	// A job without a Dockerfile shows a calm page with one button, and the editor only once the user asks for it
	let adding = $state(false);
	const showEditor = $derived(!!savedDockerfile || adding || !!dockerfile.trim());
	const showBuilds = $derived(data.hasBuilds || !!savedDockerfile);

	// The default image's tools as a sentence, e.g. `bash, curl and git`
	const toolList = `${DEFAULT_IMAGE_TOOLS.slice(0, -1).join(', ')} and ${DEFAULT_IMAGE_TOOLS.at(-1)}`;
	let imagesTable: ReturnType<typeof DataTable<JobImage>> | undefined = $state();

	// Differs from the playbook, so 'Save & build' has something to save, the untouched template included
	const dirty = $derived(dockerfile.trim() !== savedDockerfile.trim());

	// Cmd or Ctrl+S saves an edited Dockerfile, like in a code editor
	// While the editor shows, the browser's own save dialog stays closed even when there is nothing to save
	function handleSaveShortcut(event: KeyboardEvent) {
		if (event.key.toLowerCase() !== 's' || !(event.metaKey || event.ctrlKey)) return;
		if (!showEditor) return;
		event.preventDefault();
		if (dirty && !saving) void onSave();
	}

	const columns: ColumnDef<JobImage>[] = [
		{
			accessorKey: 'status',
			header: 'Status',
			meta: { sortKey: 'status', cellClass: 'w-0 whitespace-nowrap' },
			cell: ({ row }) =>
				renderComponent(ImageStatusBadge, { status: row.original.status, appearance: 'plain' })
		},
		{
			accessorKey: 'createdAt',
			header: 'Created',
			meta: { sortKey: 'createdAt', cellClass: 'whitespace-nowrap' },
			cell: ({ row }) =>
				renderComponent(RelativeTime, { value: row.original.createdAt, interactive: false })
		},
		{
			id: 'duration',
			header: 'Build time',
			meta: { hideBelow: 'sm', align: 'right', cellClass: 'numeric w-0 whitespace-nowrap' },
			cell: ({ row }) => buildTime(row.original)
		},
		{
			accessorKey: 'sizeBytes',
			header: 'Size',
			meta: {
				sortKey: 'sizeBytes',
				hideBelow: 'sm',
				align: 'right',
				cellClass: 'numeric w-0 whitespace-nowrap'
			},
			cell: ({ row }) => (row.original.sizeBytes ? formatBytes(row.original.sizeBytes) : '—')
		},
		{
			accessorKey: 'digest',
			header: 'Digest',
			meta: { hideBelow: 'md', cellClass: 'w-0 whitespace-nowrap' },
			cell: ({ row }) => renderSnippet(digestCell, row.original)
		},
		actionsColumn<JobImage>((image) => renderSnippet(actionsCell, image))
	];

	function buildTime(image: JobImage) {
		if (!image.startedAt) return '—';
		return formatDuration((image.finishedAt ?? Date.now()) - image.startedAt);
	}

	// Saving creates a playbook version, whose new Dockerfile starts a build right away
	async function saveDockerfile(value: string) {
		const next = value.trim() ? value : null;
		const result = await tryCatch(
			playbookService.update(job.id, {
				content: { ...data.playbook.content, dockerfile: next },
				summary: next ? 'Updated the Dockerfile' : 'Removed the Dockerfile',
				baseVersion: data.playbook.version
			})
		);
		if (result.error) {
			// After a conflict the page reloads, so the next save starts from the latest version
			if (isApiError(result.error, 'conflict')) await invalidate('app:playbook');
			throw result.error;
		}

		toast.success(
			next ? 'Saved the Dockerfile' : 'Removed the Dockerfile',
			next ? { description: 'The image is being built. Runs wait until it is ready.' } : undefined
		);
		await Promise.all([invalidate('app:playbook'), invalidate('app:job'), imagesTable?.refresh()]);
	}

	async function onSave() {
		// A conflict reloads the saved Dockerfile but keeps the user's text, which would otherwise overwrite the newer Dockerfile it never showed
		if (savedDockerfile !== editedFrom) {
			toast.error('Failed to save the Dockerfile', {
				description:
					'It changed since you started editing. Copy your changes, then reload the page to start from the latest version.'
			});
			return;
		}

		saving = true;
		const result = await tryCatch(saveDockerfile(dockerfile));
		saving = false;
		if (result.error) {
			apiErrorToast(result.error, 'Failed to save the Dockerfile');
			return;
		}
		// The page reloaded with the Dockerfile just saved, which the text is now written against
		editedFrom = savedDockerfile;
		// The saved Dockerfile keeps the editor open from now on, so a later version without one closes it again
		adding = false;
	}

	// The editor opens with the template filled in and takes the focus, since writing the Dockerfile is the next step
	async function addDockerfile() {
		dockerfile = dockerfileTemplate(baseImage);
		adding = true;
		await tick();
		document.getElementById(EDITOR_ID)?.focus();
	}

	function confirmRemove() {
		openConfirmDialog({
			title: 'Remove the Dockerfile',
			message:
				'Runs go back to the base image. The playbook keeps the Dockerfile in its history, so you can roll back later.',
			confirm: {
				label: 'Remove',
				destructive: true,
				action: async () => {
					const result = await tryCatch(saveDockerfile(''));
					if (result.error) {
						apiErrorToast(result.error, 'Failed to remove the Dockerfile');
						return;
					}
					// Without a Dockerfile the page goes back to its calm panel, even when the editor was opened through it
					dockerfile = '';
					editedFrom = '';
					adding = false;
				}
			}
		});
	}

	async function rebuild() {
		rebuilding = true;
		const result = await tryCatch(imageService.rebuild(job.id));
		rebuilding = false;
		if (result.error) {
			apiErrorToast(result.error, 'Failed to start a rebuild');
			return;
		}
		toast.success('Rebuilding the image');
		await imagesTable?.refresh();
		openImageId = result.data.id;
	}

	// Reflection can change the Dockerfile in the background, so the page follows along while it is open
	onMount(() =>
		subscribeWorkspaceEvents({
			onReflection: (event) => {
				if (event.jobId === job.id && event.status !== 'pending') void followNewVersion();
			},
			onReconnect: () => void followNewVersion()
		})
	);

	// Text the user hasn't touched takes the new Dockerfile, while edits stay and a save then reports that the Dockerfile changed
	async function followNewVersion() {
		const untouched = dockerfile === editedFrom;
		await Promise.all([invalidateAfterNavigation('app:playbook'), imagesTable?.refresh()]);
		if (untouched && dockerfile === editedFrom) {
			dockerfile = savedDockerfile;
			editedFrom = savedDockerfile;
		}
	}

	// The table polls while any visible build is still running, so statuses update without a reload
	// Every load replaces the rows, which runs this again for the next poll, while a failed load stops polling instead of repeating its error
	$effect(() => {
		const building = imagesTable?.getRows().some((image) => isImageBuilding(image.status));
		if (!building) return;
		const timer = setTimeout(() => imagesTable?.refresh(), POLL_MS);
		return () => clearTimeout(timer);
	});
</script>

{#snippet digestCell(image: JobImage)}
	{#if image.digest}
		<span class="font-mono text-xs" title={image.digest}>
			{image.digest.replace(/^sha256:/, '').slice(0, 12)}
		</span>
	{:else}
		<span class="text-muted-foreground">—</span>
	{/if}
{/snippet}

{#snippet actionsCell(image: JobImage)}
	<RowActions
		name="the build from {formatDateTime(image.createdAt)}"
		inline={{ label: 'Log', icon: ScrollTextIcon, onSelect: () => (openImageId = image.id) }}
	/>
{/snippet}

<svelte:head>
	<title>Environment · {job.name} · Umpteenth</title>
</svelte:head>

<svelte:window onkeydown={handleSaveShortcut} />

{#if !showEditor}
	<!-- Most jobs never need a Dockerfile, so a job without one gets a single calm panel instead of an empty editor and an empty table -->
	<Empty.Root variant="panel">
		<Empty.Header>
			<Empty.Media variant="icon">
				<ContainerIcon />
			</Empty.Media>
			<!-- 'Base image' is the job settings' name for it, and a Dockerfile builds on top of it -->
			<Empty.Title>Runs use the base image</Empty.Title>
			<Empty.Description>
				{#if job.image}
					<code class="font-mono text-xs">{job.image}</code>, set in the job's settings. Add a
					Dockerfile when the job needs more tools, so they are installed once instead of on every
					run.
				{:else}
					<code class="font-mono text-xs">{data.defaultImage}</code>, the workspace default, already
					has {toolList}. Add a Dockerfile when the job needs more, so tools are installed once
					instead of on every run.
				{/if}
			</Empty.Description>
		</Empty.Header>
		<Empty.Content>
			<Button onclick={addDockerfile}>
				<PlusIcon data-icon="inline-start" />
				Add a Dockerfile
			</Button>
		</Empty.Content>
	</Empty.Root>
{:else}
	<Card.Root>
		<Card.Header>
			<Card.Title>Dockerfile</Card.Title>
			<Card.Description>
				Saving creates a new playbook version and builds the image. Runs wait until it is ready.
			</Card.Description>
			{#if !dockerfile.trim()}
				<Card.Action>
					<Button
						variant="outline"
						size="sm"
						onclick={() => (dockerfile = dockerfileTemplate(baseImage))}
					>
						Start from a template
					</Button>
				</Card.Action>
			{/if}
		</Card.Header>
		<Card.Content>
			<CodeEditor
				bind:value={dockerfile}
				id={EDITOR_ID}
				language="dockerfile"
				label="Dockerfile"
				placeholder={`FROM ${baseImage}`}
				class="h-72"
			/>
		</Card.Content>
		<Card.Footer>
			<div class="flex w-full flex-wrap items-center justify-between gap-2">
				<div>
					{#if savedDockerfile}
						<!-- Pulled into the gutter so its label lines up with the card title -->
						<Button variant="ghost" class="-ml-3" onclick={confirmRemove} disabled={saving}>
							Use the base image
						</Button>
					{/if}
				</div>
				<div class="ml-auto flex flex-wrap justify-end gap-2">
					<!-- A rebuild uses the saved Dockerfile, so it only shows while the editor holds that one -->
					{#if savedDockerfile && !dirty}
						<Button variant="outline" onclick={rebuild} isLoading={rebuilding}>
							<RefreshCwIcon data-icon="inline-start" />
							Rebuild
						</Button>
					{/if}
					<Button onclick={onSave} isLoading={saving} disabled={!dirty}>
						<HammerIcon data-icon="inline-start" />
						Save & build
					</Button>
				</div>
			</div>
		</Card.Footer>
	</Card.Root>
{/if}

{#if showBuilds}
	<!-- Like the playbook history, the builds table sits edge to edge in a card -->
	<Card.Root>
		<Card.Header>
			<Card.Title>Builds</Card.Title>
			<Card.Description>
				Every Dockerfile gets its own image. Rebuild to pick up updates of the base image.
			</Card.Description>
		</Card.Header>
		<Card.Content padding="none">
			<DataTable
				bind:this={imagesTable}
				flush
				label="Image builds"
				{columns}
				fetchPage={(query) => imageService.list(job.id, query)}
				getRowId={(image) => image.id}
				onRowClick={(image) => (openImageId = image.id)}
				defaultSort="-createdAt"
				urlPrefix="images"
				searchable={false}
				filters={[
					{
						key: 'status',
						label: 'Status',
						options: [
							{ value: 'queued', label: 'Queued' },
							{ value: 'building', label: 'Building' },
							{ value: 'ready', label: 'Ready' },
							{ value: 'failed', label: 'Failed' }
						]
					}
				]}
			>
				{#snippet empty()}
					<Empty.Root size="sm">
						<Empty.Header>
							<Empty.Media variant="icon">
								<ContainerIcon />
							</Empty.Media>
							<Empty.Title>No builds yet</Empty.Title>
							<Empty.Description>
								The job runs on the base image until it has a Dockerfile.
							</Empty.Description>
						</Empty.Header>
					</Empty.Root>
				{/snippet}
			</DataTable>
		</Card.Content>
	</Card.Root>
{/if}

<ImageSheet jobId={job.id} bind:imageId={openImageId} onSettled={() => imagesTable?.refresh()} />
