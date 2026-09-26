<script lang="ts">
	import { isApiError } from '$lib/api/api-error';
	import { invalidate } from '$app/navigation';
	import type { JobImage } from '$lib/api/types';
	import CodeEditor from '$lib/components/code/code-editor.svelte';
	import { openConfirmDialog } from '$lib/components/confirm-dialog';
	import { DataTable, renderComponent, renderSnippet } from '$lib/components/data-table';
	import ImageStatusBadge, {
		isImageBuilding
	} from '$lib/components/jobs/image-status-badge.svelte';
	import RelativeTime from '$lib/components/relative-time.svelte';
	import * as Alert from '$lib/components/ui/alert';
	import { Button } from '$lib/components/ui/button';
	import * as Card from '$lib/components/ui/card';
	import * as Empty from '$lib/components/ui/empty';
	import ImageService from '$lib/services/image-service';
	import PlaybookService from '$lib/services/playbook-service';
	import { apiErrorToast } from '$lib/utils/error-util';
	import { formatBytes, formatDuration } from '$lib/utils/format-util';
	import { DEFAULT_IMAGE_TOOLS } from '$lib/utils/job-util';
	import { tryCatch } from '$lib/utils/try-catch-util';
	import { trackUnsavedValue } from '$lib/utils/unsaved-changes-util.svelte';
	import ContainerIcon from '@lucide/svelte/icons/container';
	import HammerIcon from '@lucide/svelte/icons/hammer';
	import InfoIcon from '@lucide/svelte/icons/info';
	import ScrollTextIcon from '@lucide/svelte/icons/scroll-text';
	import type { ColumnDef } from '@tanstack/table-core';
	import { onDestroy, untrack } from 'svelte';
	import { toast } from 'svelte-sonner';
	import ImageSheet from './image-sheet.svelte';

	let { data } = $props();

	const POLL_MS = 2000;

	const playbookService = new PlaybookService();
	const imageService = new ImageService();

	const job = $derived(data.job);
	const savedDockerfile = $derived(data.playbook.content.dockerfile ?? '');

	let dockerfile = $state(untrack(() => data.playbook.content.dockerfile ?? ''));
	let saving = $state(false);
	let rebuilding = $state(false);
	let openImageId = $state<string | null>(null);
	let imagesTable: ReturnType<typeof DataTable<JobImage>> | undefined = $state();
	let pollTimer: ReturnType<typeof setTimeout> | undefined;

	const dirty = $derived(dockerfile.trim() !== savedDockerfile.trim());

	const template = $derived(
		`FROM ${data.defaultImage}\n\n# Install extra tools here, e.g.\nRUN apt-get update && apt-get install -y --no-install-recommends ffmpeg && rm -rf /var/lib/apt/lists/*\n`
	);

	// The unsaved-changes bar offers to save or discard an edited Dockerfile when leaving the page
	const { markSaved } = trackUnsavedValue(
		() => dockerfile.trim(),
		(value) => (dockerfile = value),
		(value) => saveDockerfile(value)
	);

	// A playbook changed elsewhere, e.g. by a rollback or reflection, replaces the editor unless it holds unsaved edits
	let seenVersion = untrack(() => data.playbook.version);
	$effect(() => {
		const version = data.playbook.version;
		untrack(() => {
			if (version === seenVersion) return;
			seenVersion = version;
			if (!dirty) {
				dockerfile = savedDockerfile;
				markSaved();
			}
		});
	});

	const columns: ColumnDef<JobImage>[] = [
		{
			accessorKey: 'status',
			header: 'Status',
			meta: { sortKey: 'status' },
			cell: ({ row }) => renderComponent(ImageStatusBadge, { status: row.original.status })
		},
		{
			accessorKey: 'createdAt',
			header: 'Created',
			meta: { sortKey: 'createdAt' },
			cell: ({ row }) => renderComponent(RelativeTime, { value: row.original.createdAt })
		},
		{
			id: 'duration',
			header: 'Build time',
			meta: { cellClass: 'numeric' },
			cell: ({ row }) => buildTime(row.original)
		},
		{
			accessorKey: 'sizeBytes',
			header: 'Size',
			meta: { sortKey: 'sizeBytes', cellClass: 'numeric' },
			cell: ({ row }) => (row.original.sizeBytes ? formatBytes(row.original.sizeBytes) : '—')
		},
		{
			accessorKey: 'digest',
			header: 'Digest',
			meta: { cellClass: 'font-mono text-xs max-w-48 truncate' },
			cell: ({ row }) => renderSnippet(digestCell, row.original)
		},
		{
			id: 'actions',
			header: () => renderSnippet(srOnly, 'Actions'),
			meta: { headerClass: 'w-0', cellClass: 'w-0 text-right' },
			cell: ({ row }) => renderSnippet(actionsCell, row.original)
		}
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
		toast.success(next ? 'Saved, the image is being built' : 'Removed the Dockerfile');
		await Promise.all([invalidate('app:playbook'), invalidate('app:job')]);
		await refreshImages();
	}

	async function onSave() {
		saving = true;
		const result = await tryCatch(saveDockerfile(dockerfile));
		saving = false;
		if (result.error) {
			apiErrorToast(result.error, 'Failed to save the Dockerfile');
			return;
		}
		markSaved();
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
					dockerfile = '';
					markSaved();
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
		await refreshImages();
		openImageId = result.data.id;
	}

	// The table polls while any visible build is still running, so statuses update without a reload
	async function refreshImages() {
		clearTimeout(pollTimer);
		await imagesTable?.refresh();
		schedulePoll();
	}

	function schedulePoll() {
		clearTimeout(pollTimer);
		const building = imagesTable?.getRows().some((image) => isImageBuilding(image.status));
		if (building) pollTimer = setTimeout(refreshImages, POLL_MS);
	}

	async function fetchImages(query: Parameters<typeof imageService.list>[1]) {
		const page = await imageService.list(job.id, query);
		setTimeout(schedulePoll);
		return page;
	}

	onDestroy(() => clearTimeout(pollTimer));
</script>

{#snippet srOnly(text: string)}
	<span class="sr-only">{text}</span>
{/snippet}

{#snippet digestCell(image: JobImage)}
	{#if image.digest}
		<span title={image.digest}>{image.digest.replace(/^sha256:/, '').slice(0, 12)}</span>
	{:else}
		<span class="text-muted-foreground">—</span>
	{/if}
{/snippet}

{#snippet actionsCell(image: JobImage)}
	<Button variant="ghost" size="sm" onclick={() => (openImageId = image.id)}>
		<ScrollTextIcon data-icon="inline-start" />
		Log
	</Button>
{/snippet}

<svelte:head>
	<title>Environment · {job.name} · Umpteenth</title>
</svelte:head>

<Alert.Root variant="info">
	<InfoIcon />
	<Alert.Title>Most jobs don't need a Dockerfile</Alert.Title>
	<Alert.Description>
		The default image <code class="font-mono text-xs">{data.defaultImage}</code> already has
		{DEFAULT_IMAGE_TOOLS.join(', ')}. Add a Dockerfile when a job needs more, so tools are installed
		once instead of on every run.
	</Alert.Description>
</Alert.Root>

<Card.Root>
	<Card.Header>
		<Card.Title>Dockerfile</Card.Title>
		<Card.Description>
			Saving creates a new playbook version and builds the image. Runs wait until it is ready.
		</Card.Description>
		{#if !dockerfile.trim()}
			<Card.Action>
				<Button variant="outline" size="sm" onclick={() => (dockerfile = template)}>
					Start from a template
				</Button>
			</Card.Action>
		{/if}
	</Card.Header>
	<Card.Content>
		<CodeEditor
			bind:value={dockerfile}
			language="dockerfile"
			label="Dockerfile"
			placeholder={`FROM ${data.defaultImage}`}
			class="h-72"
		/>
	</Card.Content>
	<Card.Footer class="flex flex-wrap justify-between gap-2">
		<div>
			{#if savedDockerfile}
				<Button variant="ghost" onclick={confirmRemove} disabled={saving}>Use the base image</Button
				>
			{/if}
		</div>
		<div class="flex gap-2">
			{#if dirty}
				<Button variant="outline" onclick={() => (dockerfile = savedDockerfile)} disabled={saving}>
					Discard
				</Button>
			{/if}
			<Button onclick={onSave} isLoading={saving} disabled={!dirty}>
				<HammerIcon data-icon="inline-start" />
				Save & build
			</Button>
		</div>
	</Card.Footer>
</Card.Root>

<div class="flex flex-col gap-3">
	<div class="flex flex-col gap-1">
		<h2 class="text-lg font-semibold">Builds</h2>
		<p class="text-muted-foreground text-sm">
			Every Dockerfile gets its own image. Rebuild to pick up updates of the base image.
		</p>
	</div>
	<DataTable
		bind:this={imagesTable}
		label="Image builds"
		{columns}
		fetchPage={fetchImages}
		getRowId={(image) => image.id}
		defaultSort="-createdAt"
		defaultPageSize={10}
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
		{#snippet actions()}
			<Button
				variant="outline"
				onclick={rebuild}
				isLoading={rebuilding}
				disabled={!savedDockerfile}
				title={savedDockerfile ? undefined : 'The job has no Dockerfile'}
			>
				<HammerIcon data-icon="inline-start" />
				Rebuild
			</Button>
		{/snippet}
		{#snippet empty()}
			<Empty.Root class="py-6">
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
</div>

<ImageSheet jobId={job.id} bind:imageId={openImageId} onSettled={refreshImages} />
