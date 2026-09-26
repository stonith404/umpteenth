<script lang="ts">
	import { isApiError } from '$lib/api/api-error';
	import { invalidate } from '$app/navigation';
	import type {
		PlaybookContent,
		PlaybookLearning,
		PlaybookScript,
		PlaybookVersionListItem
	} from '$lib/api/types';
	import CodeEditor from '$lib/components/code/code-editor.svelte';
	import { languageForScript } from '$lib/components/code/code-languages';
	import { openConfirmDialog } from '$lib/components/confirm-dialog';
	import { DataTable, renderComponent, renderSnippet } from '$lib/components/data-table';
	import RelativeTime from '$lib/components/relative-time.svelte';
	import { Badge } from '$lib/components/ui/badge';
	import { Button } from '$lib/components/ui/button';
	import * as Card from '$lib/components/ui/card';
	import * as Collapsible from '$lib/components/ui/collapsible';
	import * as Empty from '$lib/components/ui/empty';
	import PlaybookService from '$lib/services/playbook-service';
	import { apiErrorToast } from '$lib/utils/error-util';
	import { authorLabel } from '$lib/utils/playbook-util';
	import { cn } from '$lib/utils/style';
	import { tryCatch } from '$lib/utils/try-catch-util';
	import { subscribeWorkspaceEvents } from '$lib/utils/workspace-events';
	import { onMount } from 'svelte';
	import ArchiveIcon from '@lucide/svelte/icons/archive';
	import ArchiveRestoreIcon from '@lucide/svelte/icons/archive-restore';
	import BookOpenIcon from '@lucide/svelte/icons/book-open';
	import ChevronRightIcon from '@lucide/svelte/icons/chevron-right';
	import ContainerIcon from '@lucide/svelte/icons/container';
	import FileJsonIcon from '@lucide/svelte/icons/file-json';
	import GitCompareIcon from '@lucide/svelte/icons/git-compare';
	import PencilIcon from '@lucide/svelte/icons/pencil';
	import RotateCcwIcon from '@lucide/svelte/icons/rotate-ccw';
	import Trash2Icon from '@lucide/svelte/icons/trash-2';
	import type { ColumnDef } from '@tanstack/table-core';
	import { toast } from 'svelte-sonner';
	import JsonEditDialog from './json-edit-dialog.svelte';
	import LearningDialog from './learning-dialog.svelte';
	import ScriptDialog, { type CodeTarget } from './script-dialog.svelte';
	import VersionDiffSheet from './version-diff-sheet.svelte';

	let { data } = $props();

	const playbookService = new PlaybookService();

	const job = $derived(data.job);
	const playbook = $derived(data.playbook);
	const content = $derived(playbook.content);
	const learnings = $derived(content.learnings ?? []);
	const activeLearnings = $derived(learnings.filter((l) => l.status !== 'retired'));
	const retiredLearnings = $derived(learnings.filter((l) => l.status === 'retired'));
	const toolkit = $derived(content.toolkit ?? []);

	let versionsTable: ReturnType<typeof DataTable<PlaybookVersionListItem>> | undefined = $state();
	let jsonOpen = $state(false);
	let editingLearning = $state<PlaybookLearning | null>(null);
	let editingCode = $state<CodeTarget | null>(null);
	let diffVersion = $state<number | null>(null);
	let showRetired = $state(false);

	const columns: ColumnDef<PlaybookVersionListItem>[] = [
		{
			accessorKey: 'version',
			header: 'Version',
			meta: { sortKey: 'version', cellClass: 'numeric w-0' },
			cell: ({ row }) => renderSnippet(versionCell, row.original)
		},
		{
			accessorKey: 'author',
			header: 'Author',
			cell: ({ row }) => renderSnippet(authorCell, row.original)
		},
		{
			accessorKey: 'summary',
			header: 'Summary',
			meta: { cellClass: 'max-w-96 truncate' },
			cell: ({ row }) => row.original.summary ?? '—'
		},
		{
			accessorKey: 'createdAt',
			header: 'Created',
			meta: { sortKey: 'createdAt' },
			cell: ({ row }) => renderComponent(RelativeTime, { value: row.original.createdAt })
		},
		{
			id: 'actions',
			header: () => renderSnippet(srOnly, 'Actions'),
			meta: { headerClass: 'w-0', cellClass: 'w-0 text-right' },
			cell: ({ row }) => renderSnippet(actionsCell, row.original)
		}
	];

	// Every edit is saved as a new version, then the page and the job header reload to show it
	// The loaded version travels along, so an edit can't overwrite a version written meanwhile, e.g. by reflection
	async function saveContent(next: PlaybookContent, summary: string) {
		const result = await tryCatch(
			playbookService.update(job.id, {
				content: next,
				summary: summary || undefined,
				baseVersion: data.playbook.version
			})
		);
		if (result.error) {
			// After a conflict the page reloads, so the next attempt starts from the latest version
			if (isApiError(result.error, 'conflict')) await invalidate('app:playbook');
			throw result.error;
		}
		await Promise.all([invalidate('app:playbook'), invalidate('app:job')]);
		void versionsTable?.refresh();
	}

	// Learning edits are saved one after another, and each builds its list from the playbook the previous save reloaded
	// Building from the content at click time instead would let a second quick click bring back what the first one changed
	let learningSaveQueue: Promise<unknown> = Promise.resolve();
	let pendingLearningSaves = $state(0);
	const savingLearnings = $derived(pendingLearningSaves > 0);

	function saveLearnings(
		update: (current: PlaybookLearning[]) => PlaybookLearning[],
		summary: string
	): Promise<boolean> {
		pendingLearningSaves++;
		const save = learningSaveQueue.then(async () => {
			const result = await tryCatch(
				saveContent({ ...content, learnings: update(learnings) }, summary)
			);
			if (result.error) {
				apiErrorToast(result.error, 'Failed to save the playbook');
				return false;
			}
			toast.success(summary);
			return true;
		});
		learningSaveQueue = save;
		return save.finally(() => pendingLearningSaves--);
	}

	function setLearningStatus(learning: PlaybookLearning, status: 'active' | 'retired') {
		void saveLearnings(
			(current) => current.map((l) => (l.id === learning.id ? { ...l, status } : l)),
			status === 'retired' ? `Retired learning ${learning.id}` : `Restored learning ${learning.id}`
		);
	}

	// Only the fields the dialog edits are applied, so the rest of the learning comes from the latest version
	function saveLearning(updated: PlaybookLearning) {
		const { text, when, kind } = updated;
		return saveLearnings(
			(current) => current.map((l) => (l.id === updated.id ? { ...l, text, when, kind } : l)),
			`Edited learning ${updated.id}`
		);
	}

	// A script's header is read again on save, so its description and arguments follow the edited code
	async function saveCode(target: CodeTarget, code: string) {
		const next =
			target.kind === 'main'
				? { ...content, main: code }
				: {
						...content,
						toolkit: toolkit.map((s) => (s.name === target.name ? { ...s, content: code } : s))
					};
		const label = target.kind === 'main' ? 'the main script' : `script ${target.name}`;
		const result = await tryCatch(saveContent(next, `Edited ${label}`));
		if (result.error) {
			apiErrorToast(result.error, `Failed to save ${label}`);
			return false;
		}
		toast.success(`Saved ${label}`);
		return true;
	}

	function editScript(script: PlaybookScript) {
		editingCode = {
			kind: 'script',
			name: script.name,
			title: `Edit script ${script.name}`,
			lang: script.lang,
			content: script.content
		};
	}

	function editMain(main: string) {
		const lang = main.split('\n', 1)[0].includes('python') ? 'python' : 'bash';
		editingCode = {
			kind: 'main',
			name: 'main',
			title: 'Edit the main script',
			lang,
			content: main
		};
	}

	function confirmDeleteScript(script: PlaybookScript) {
		openConfirmDialog({
			title: `Delete script ${script.name}`,
			message:
				'Future runs no longer get it as a tool. The script stays in the history, so rolling back brings it back.',
			confirm: {
				label: 'Delete',
				destructive: true,
				action: async () => {
					const next = toolkit.filter((s) => s.name !== script.name);
					const result = await tryCatch(
						saveContent({ ...content, toolkit: next }, `Deleted script ${script.name}`)
					);
					if (result.error) {
						apiErrorToast(result.error, 'Failed to delete the script');
						return;
					}
					toast.success(`Deleted script ${script.name}`);
				}
			}
		});
	}

	async function saveJson(next: PlaybookContent, summary: string) {
		await saveContent(next, summary);
		toast.success('Saved a new playbook version');
	}

	// Reflection writes versions in the background, so the page follows along while it is open
	onMount(() =>
		subscribeWorkspaceEvents({
			onReflection: (event) => {
				if (event.jobId !== job.id || event.status === 'pending') return;
				void Promise.all([invalidate('app:playbook'), invalidate('app:job')]);
				void versionsTable?.refresh();
			}
		})
	);

	function confirmRollback(version: number) {
		openConfirmDialog({
			title: `Roll back to version ${version}`,
			message: `This creates a new version with the content of version ${version}, so the history stays intact. If its Dockerfile differs, the matching image is built again.`,
			confirm: {
				label: 'Roll back',
				action: async () => {
					const result = await tryCatch(playbookService.rollback(job.id, version));
					if (result.error) {
						apiErrorToast(result.error, 'Failed to roll back');
						return;
					}
					diffVersion = null;
					toast.success(`Rolled back to version ${version}`);
					await Promise.all([invalidate('app:playbook'), invalidate('app:job')]);
					await versionsTable?.refresh();
				}
			}
		});
	}
</script>

{#snippet srOnly(text: string)}
	<span class="sr-only">{text}</span>
{/snippet}

{#snippet versionCell(item: PlaybookVersionListItem)}
	<span class="inline-flex items-center gap-2">
		v{item.version}
		{#if item.version === playbook.version}
			<Badge variant="secondary">Current</Badge>
		{/if}
	</span>
{/snippet}

{#snippet authorCell(item: PlaybookVersionListItem)}
	<span class="inline-flex items-center gap-2">
		{authorLabel(item.author)}
		{#if item.sourceRunId}
			<a href="/runs/{item.sourceRunId}" class="text-muted-foreground text-xs hover:underline">
				from run
			</a>
		{/if}
	</span>
{/snippet}

{#snippet actionsCell(item: PlaybookVersionListItem)}
	<div class="flex justify-end gap-1">
		<Button
			variant="ghost"
			size="sm"
			aria-label="Show changes of version {item.version}"
			onclick={() => (diffVersion = item.version)}
		>
			<GitCompareIcon data-icon="inline-start" />
			Changes
		</Button>
		{#if item.version !== playbook.version}
			<Button
				variant="ghost"
				size="sm"
				aria-label="Roll back to version {item.version}"
				onclick={() => confirmRollback(item.version)}
			>
				<RotateCcwIcon data-icon="inline-start" />
				Roll back
			</Button>
		{/if}
	</div>
{/snippet}

{#snippet learningItem(learning: PlaybookLearning)}
	<li class="flex items-start gap-3 py-3 first:pt-0 last:pb-0">
		<div class="flex min-w-0 flex-1 flex-col gap-1.5">
			<p
				class={cn('text-sm', learning.status === 'retired' && 'text-muted-foreground line-through')}
			>
				{learning.text}
			</p>
			<div class="text-muted-foreground flex flex-wrap items-center gap-x-3 gap-y-1 text-xs">
				<span class="font-mono">{learning.id}</span>
				<Badge variant="outline" class="font-normal">{learning.kind}</Badge>
				{#if learning.when}<span>when {learning.when}</span>{/if}
				<span class="numeric" title="How often runs relied on this learning">
					{learning.hits}
					{learning.hits === 1 ? 'hit' : 'hits'}
				</span>
				{#each learning.sources ?? [] as source, i (source)}
					<a href="/runs/{source}" class="hover:underline">source run {i + 1}</a>
				{/each}
			</div>
		</div>
		<div class="flex shrink-0 gap-1">
			<Button
				variant="ghost"
				size="icon-sm"
				aria-label="Edit learning {learning.id}"
				disabled={savingLearnings}
				onclick={() => (editingLearning = learning)}
			>
				<PencilIcon />
			</Button>
			{#if learning.status === 'retired'}
				<Button
					variant="ghost"
					size="icon-sm"
					aria-label="Restore learning {learning.id}"
					title="Restore"
					disabled={savingLearnings}
					onclick={() => setLearningStatus(learning, 'active')}
				>
					<ArchiveRestoreIcon />
				</Button>
			{:else}
				<Button
					variant="ghost"
					size="icon-sm"
					aria-label="Retire learning {learning.id}"
					title="Retire"
					disabled={savingLearnings}
					onclick={() => setLearningStatus(learning, 'retired')}
				>
					<ArchiveIcon />
				</Button>
			{/if}
		</div>
	</li>
{/snippet}

{#snippet scriptPlaceholder(
	title: string,
	text: string,
	value: string | null | undefined,
	language: 'shell' | 'json'
)}
	<section class="flex flex-col gap-2">
		<h3 class="text-sm font-medium">{title}</h3>
		{#if value}
			<CodeEditor {value} {language} readonly label={title} class="h-auto max-h-72" />
		{:else}
			<p class="text-muted-foreground text-sm">{text}</p>
		{/if}
	</section>
{/snippet}

<svelte:head>
	<title>Playbook · {job.name} · Umpteenth</title>
</svelte:head>

<div class="flex flex-wrap items-center justify-between gap-3">
	<div class="flex flex-col gap-1">
		<h2 class="text-lg font-semibold">
			{playbook.version > 0 ? `Version ${playbook.version}` : 'Empty playbook'}
		</h2>
		<p class="text-muted-foreground text-sm">
			{#if playbook.version > 0}
				{authorLabel(playbook.author)}
				{#if playbook.createdAt}· <RelativeTime value={playbook.createdAt} />{/if}
				{#if playbook.summary}· {playbook.summary}{/if}
			{:else}
				What the job learns from its runs shows up here{job.selfImprove
					? '.'
					: ', once learning is turned on in the settings.'}
			{/if}
		</p>
	</div>
	<Button variant="outline" onclick={() => (jsonOpen = true)}>
		<FileJsonIcon data-icon="inline-start" />
		Edit as JSON
	</Button>
</div>

<Card.Root>
	<Card.Header>
		<Card.Title>Learnings</Card.Title>
		<Card.Description>
			Know-how the agent reads before every run. Retired learnings are kept for the record.
		</Card.Description>
	</Card.Header>
	<Card.Content>
		{#if activeLearnings.length > 0}
			<ul class="divide-y">
				{#each activeLearnings as learning (learning.id)}
					{@render learningItem(learning)}
				{/each}
			</ul>
		{:else}
			<p class="text-muted-foreground text-sm">No active learnings yet.</p>
		{/if}
		{#if retiredLearnings.length > 0}
			<Collapsible.Root bind:open={showRetired} class="mt-4">
				<Collapsible.Trigger>
					{#snippet child({ props })}
						<Button {...props} variant="ghost" size="sm" class="-ml-2">
							<ChevronRightIcon
								data-icon="inline-start"
								class={cn('transition-transform', showRetired && 'rotate-90')}
							/>
							{retiredLearnings.length} retired
						</Button>
					{/snippet}
				</Collapsible.Trigger>
				<Collapsible.Content>
					<ul class="mt-2 divide-y">
						{#each retiredLearnings as learning (learning.id)}
							{@render learningItem(learning)}
						{/each}
					</ul>
				</Collapsible.Content>
			</Collapsible.Root>
		{/if}
	</Card.Content>
</Card.Root>

<Card.Root>
	<Card.Header>
		<Card.Title>Toolkit</Card.Title>
		<Card.Description>
			Scripts in <code class="font-mono text-xs">/ump/toolkit</code>, also offered to the agent as
			tools.
		</Card.Description>
	</Card.Header>
	<Card.Content>
		{#if toolkit.length > 0}
			<ul class="flex flex-col gap-3">
				{#each toolkit as script (script.name)}
					<li>
						<Collapsible.Root class="bg-muted/30 rounded-2xl border">
							<Collapsible.Trigger class="group flex w-full items-start gap-3 px-4 py-3 text-left">
								<ChevronRightIcon
									class="text-muted-foreground mt-0.5 size-4 shrink-0 transition-transform group-data-[state=open]:rotate-90"
								/>
								<div class="flex min-w-0 flex-1 flex-col gap-1">
									<span class="flex flex-wrap items-center gap-2">
										<span class="font-mono text-sm font-medium">{script.name}</span>
										<Badge variant="outline" class="font-normal">{script.lang}</Badge>
										{#if script.sideEffects}
											<Badge variant="destructive">Side effects</Badge>
										{/if}
									</span>
									<span class="text-muted-foreground text-sm">{script.description}</span>
									<span class="text-muted-foreground numeric text-xs">
										{script.stats.calls} calls · {script.stats.failures} failed
									</span>
								</div>
							</Collapsible.Trigger>
							<Collapsible.Content class="flex flex-col gap-2 px-4 pb-4">
								{#if script.args}
									<p class="text-muted-foreground font-mono text-xs">
										args {JSON.stringify(script.args)}
									</p>
								{/if}
								<CodeEditor
									value={script.content}
									language={languageForScript(script.lang)}
									readonly
									label="Script {script.name}"
									class="h-auto max-h-96"
								/>
								<div class="flex justify-end gap-1">
									<Button
										variant="ghost"
										size="sm"
										aria-label="Edit script {script.name}"
										onclick={() => editScript(script)}
									>
										<PencilIcon data-icon="inline-start" />
										Edit
									</Button>
									<Button
										variant="ghost"
										size="sm"
										aria-label="Delete script {script.name}"
										onclick={() => confirmDeleteScript(script)}
									>
										<Trash2Icon data-icon="inline-start" />
										Delete
									</Button>
								</div>
							</Collapsible.Content>
						</Collapsible.Root>
					</li>
				{/each}
			</ul>
		{:else}
			<p class="text-muted-foreground text-sm">No scripts yet.</p>
		{/if}
	</Card.Content>
</Card.Root>

<Card.Root>
	<Card.Header>
		<Card.Title>Scripts</Card.Title>
		<Card.Description>
			The setup runs before every run. Once the job graduates, main does the whole job without the
			agent, and verify says how its runs are checked.
		</Card.Description>
	</Card.Header>
	<Card.Content class="flex flex-col gap-6">
		{@render scriptPlaceholder('Setup', 'No setup script.', content.setup, 'shell')}
		{@render scriptPlaceholder(
			'Main',
			'Not graduated yet. Once runs are reliable, the job proposes a script that does the work without the agent.',
			content.main,
			'shell'
		)}
		{#if content.main}
			{@const main = content.main}
			<div class="-mt-4 flex justify-end">
				<Button variant="ghost" size="sm" onclick={() => editMain(main)}>
					<PencilIcon data-icon="inline-start" />
					Edit main
				</Button>
			</div>
		{/if}
		{@render scriptPlaceholder(
			'Verify',
			'No verification yet.',
			content.verify !== undefined && content.verify !== null
				? JSON.stringify(content.verify, null, 2)
				: null,
			'json'
		)}
		<section class="flex flex-wrap items-center justify-between gap-2">
			<span class="text-muted-foreground inline-flex items-center gap-2 text-sm">
				<ContainerIcon class="size-4" />
				{content.dockerfile ? 'Uses a custom Dockerfile' : 'Uses the base image'}
			</span>
			<Button variant="outline" size="sm" href="/jobs/{job.id}/environment">Environment</Button>
		</section>
	</Card.Content>
</Card.Root>

<Card.Root>
	<Card.Header>
		<Card.Title>History</Card.Title>
		<Card.Description>
			Every change is a version, whether it came from reflection, a manual edit or a rollback.
		</Card.Description>
	</Card.Header>
	<Card.Content>
		<DataTable
			bind:this={versionsTable}
			label="Playbook versions"
			{columns}
			fetchPage={(query) => playbookService.versions(job.id, query)}
			getRowId={(item) => String(item.version)}
			defaultSort="-version"
			defaultPageSize={10}
			urlPrefix="versions"
			searchPlaceholder="Search versions"
		>
			{#snippet empty()}
				<Empty.Root class="py-6">
					<Empty.Header>
						<Empty.Media variant="icon">
							<BookOpenIcon />
						</Empty.Media>
						<Empty.Title>No versions yet</Empty.Title>
						<Empty.Description>
							Reflection saves a version after runs teach the job something new.
						</Empty.Description>
					</Empty.Header>
				</Empty.Root>
			{/snippet}
		</DataTable>
	</Card.Content>
</Card.Root>

<JsonEditDialog bind:open={jsonOpen} {content} onSave={saveJson} />
<LearningDialog bind:learning={editingLearning} onSave={saveLearning} />
<ScriptDialog bind:target={editingCode} onSave={saveCode} />
<VersionDiffSheet
	jobId={job.id}
	bind:version={diffVersion}
	currentVersion={playbook.version}
	onRollback={confirmRollback}
/>
