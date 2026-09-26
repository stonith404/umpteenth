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
	import { languageForScript, type CodeLanguage } from '$lib/components/code/script-language';
	import { openConfirmDialog } from '$lib/components/confirm-dialog';
	import {
		actionsColumn,
		DataTable,
		renderComponent,
		renderSnippet,
		RowActions
	} from '$lib/components/data-table';
	import AuthorLabel from '$lib/components/playbook/author-label.svelte';
	import KindIcon from '$lib/components/playbook/kind-icon.svelte';
	import VersionMeta from '$lib/components/playbook/version-meta.svelte';
	import VersionSummary from '$lib/components/playbook/version-summary.svelte';
	import RelativeTime from '$lib/components/relative-time.svelte';
	import { Badge } from '$lib/components/ui/badge';
	import { Button } from '$lib/components/ui/button';
	import * as Card from '$lib/components/ui/card';
	import * as Collapsible from '$lib/components/ui/collapsible';
	import PlaybookService from '$lib/services/playbook-service';
	import { apiErrorToast } from '$lib/utils/error-util';
	import {
		learningKindLabel,
		scriptLanguageLabel,
		type PlaybookChangeKind
	} from '$lib/utils/playbook-util';
	import { cn } from '$lib/utils/style';
	import { tryCatch } from '$lib/utils/try-catch-util';
	import { subscribeWorkspaceEvents } from '$lib/utils/workspace-events';
	import { onMount, type Snippet } from 'svelte';
	import ArchiveIcon from '@lucide/svelte/icons/archive';
	import ArchiveRestoreIcon from '@lucide/svelte/icons/archive-restore';
	import ChevronRightIcon from '@lucide/svelte/icons/chevron-right';
	import ArrowUpRightIcon from '@lucide/svelte/icons/arrow-up-right';
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
	const hasVerify = $derived(content.verify !== undefined && content.verify !== null);

	// A part of the playbook that runs around the agent, with a short state such as `3 lines` when it is set
	type RunPart = {
		kind: PlaybookChangeKind;
		title: string;
		description: string;
		code: string | null;
		language: CodeLanguage;
		state: string | null;
	};

	const lineCount = (code: string) => {
		const n = code.trimEnd().split('\n').length;
		return `${n} ${n === 1 ? 'line' : 'lines'}`;
	};

	// Verify holds a list of checks, and the count is what tells two verify setups apart at a glance
	const verifyState = $derived.by(() => {
		const checks = (content.verify as { checks?: unknown } | null | undefined)?.checks;
		if (!Array.isArray(checks)) return 'Set';
		return `${checks.length} ${checks.length === 1 ? 'check' : 'checks'}`;
	});

	const runParts = $derived<RunPart[]>([
		{
			kind: 'setup',
			title: 'Setup',
			description: 'Runs before every run, e.g. to install tools.',
			code: content.setup || null,
			language: 'shell',
			state: content.setup ? lineCount(content.setup) : null
		},
		{
			kind: 'main',
			title: 'Main',
			description: content.main
				? 'Does the whole job without the agent.'
				: 'Once runs are reliable, the job proposes a main script that does the work without the agent.',
			code: content.main || null,
			language: 'shell',
			state: content.main ? lineCount(content.main) : null
		},
		{
			kind: 'verify',
			title: 'Verify',
			description: 'How the runs of the main script are checked.',
			code: hasVerify ? JSON.stringify(content.verify, null, 2) : null,
			language: 'json',
			state: hasVerify ? verifyState : null
		},
		{
			kind: 'dockerfile',
			title: 'Dockerfile',
			description: content.dockerfile
				? 'Builds the image runs start from.'
				: 'Runs use the base image. Add a Dockerfile in the environment when the job needs more tools.',
			code: content.dockerfile || null,
			language: 'dockerfile',
			state: content.dockerfile ? 'Custom image' : null
		}
	]);

	let versionsTable: ReturnType<typeof DataTable<PlaybookVersionListItem>> | undefined = $state();
	let jsonOpen = $state(false);
	let editingLearning = $state<PlaybookLearning | null>(null);
	// The version the learning dialog opened with, see saveContent
	let learningBaseVersion = 0;
	let editingCode = $state<CodeTarget | null>(null);
	let diffVersion = $state<number | null>(null);
	let showRetired = $state(false);

	// The summary takes the free width and truncates, while the other columns keep to their content
	const columns: ColumnDef<PlaybookVersionListItem>[] = [
		{
			accessorKey: 'version',
			header: 'Version',
			meta: { sortKey: 'version', cellClass: 'numeric w-0 whitespace-nowrap' },
			cell: ({ row }) => renderSnippet(versionCell, row.original)
		},
		{
			accessorKey: 'author',
			header: 'Author',
			meta: { hideBelow: 'md', cellClass: 'w-0 whitespace-nowrap' },
			cell: ({ row }) => renderComponent(AuthorLabel, { author: row.original.author })
		},
		{
			accessorKey: 'summary',
			header: 'Summary',
			meta: { cellClass: 'w-full max-w-0 truncate' },
			cell: ({ row }) => renderSnippet(summaryCell, row.original)
		},
		{
			accessorKey: 'createdAt',
			header: 'Created',
			meta: { sortKey: 'createdAt', hideBelow: 'sm', cellClass: 'w-0 whitespace-nowrap' },
			cell: ({ row }) =>
				renderComponent(RelativeTime, { value: row.original.createdAt, interactive: false })
		},
		actionsColumn<PlaybookVersionListItem>((item) => renderSnippet(actionsCell, item))
	];

	// A new version changes the page, the job header's version and the history table
	async function reload() {
		await Promise.all([
			invalidate('app:playbook'),
			invalidate('app:job'),
			versionsTable?.refresh()
		]);
	}

	// Every edit is saved as a new version, then the page reloads to show it
	// The version the edit started from travels along, so an edit can't overwrite a version written meanwhile, e.g. by reflection
	// The page follows reflection while a dialog is open, so a dialog passes the version it opened with instead of the one loaded now
	async function saveContent(
		next: PlaybookContent,
		summary: string,
		baseVersion: number = data.playbook.version
	) {
		const result = await tryCatch(
			playbookService.update(job.id, {
				content: next,
				summary: summary || undefined,
				baseVersion
			})
		);
		if (result.error) {
			// After a conflict the page reloads, so the next edit starts from the latest version
			if (isApiError(result.error, 'conflict')) await invalidate('app:playbook');
			throw result.error;
		}
		await reload();
	}

	// Learning edits are saved one after another, and each builds its list from the playbook the previous save reloaded
	// Building from the content at click time instead would let a second quick click bring back what the first one changed
	let learningSaveQueue: Promise<unknown> = Promise.resolve();
	let pendingLearningSaves = $state(0);
	const savingLearnings = $derived(pendingLearningSaves > 0);

	function saveLearnings(
		update: (current: PlaybookLearning[]) => PlaybookLearning[],
		summary: string,
		baseVersion?: number
	): Promise<boolean> {
		pendingLearningSaves++;
		const save = learningSaveQueue.then(async () => {
			const result = await tryCatch(
				saveContent({ ...content, learnings: update(learnings) }, summary, baseVersion)
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

	// Edit buttons are disabled while learnings save, so the version the dialog opens with is the latest one
	function editLearning(learning: PlaybookLearning) {
		learningBaseVersion = playbook.version;
		editingLearning = learning;
	}

	// Only the fields the dialog edits are applied, so the rest of the learning comes from the latest version
	function saveLearning(updated: PlaybookLearning) {
		const { text, when, kind } = updated;
		return saveLearnings(
			(current) => current.map((l) => (l.id === updated.id ? { ...l, text, when, kind } : l)),
			`Edited learning ${updated.id}`,
			learningBaseVersion
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
		const result = await tryCatch(saveContent(next, `Edited ${label}`, target.baseVersion));
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
			content: script.content,
			baseVersion: playbook.version
		};
	}

	function editMain(main: string) {
		const lang = main.split('\n', 1)[0].includes('python') ? 'python' : 'bash';
		editingCode = {
			kind: 'main',
			name: 'main',
			title: 'Edit the main script',
			lang,
			content: main,
			baseVersion: playbook.version
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

	async function saveJson(next: PlaybookContent, summary: string, baseVersion: number) {
		await saveContent(next, summary, baseVersion);
		toast.success('Saved a new playbook version');
	}

	// Reflection writes versions in the background, so the page follows along while it is open
	onMount(() =>
		subscribeWorkspaceEvents({
			onReflection: (event) => {
				if (event.jobId !== job.id || event.status === 'pending') return;
				void reload();
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
					await reload();
				}
			}
		});
	}
</script>

{#snippet versionCell(item: PlaybookVersionListItem)}
	<span class="inline-flex items-center gap-2">
		v{item.version}
		{#if item.version === playbook.version}
			<Badge variant="secondary">Current</Badge>
		{/if}
	</span>
{/snippet}

{#snippet summaryCell(item: PlaybookVersionListItem)}
	{#if item.summary}
		<span title={item.summary}>{item.summary}</span>
	{:else}
		<span class="text-muted-foreground">No summary</span>
	{/if}
{/snippet}

<!-- A row opens its changes on click, and the menu holds the same action for keyboard users next to the rest -->
{#snippet actionsCell(item: PlaybookVersionListItem)}
	<RowActions
		name="version {item.version}"
		items={[
			{ label: 'Show changes', icon: GitCompareIcon, onSelect: () => (diffVersion = item.version) },
			!!item.sourceRunId && {
				label: 'Open source run',
				icon: ArrowUpRightIcon,
				href: `/runs/${item.sourceRunId}`
			},
			item.version !== playbook.version && {
				label: `Roll back to version ${item.version}`,
				icon: RotateCcwIcon,
				onSelect: () => confirmRollback(item.version)
			}
		]}
	/>
{/snippet}

{#snippet learningItem(learning: PlaybookLearning)}
	{@const sources = learning.sources ?? []}
	<!-- The kind sits in a column of its own, so a long list reads by kind before it reads as prose -->
	<li
		class="flex items-start gap-3 py-3 first:pt-0 last:pb-0 max-sm:flex-wrap"
		title="Learning {learning.id}"
	>
		<div class="w-28 shrink-0 max-sm:w-full">
			<Badge variant="secondary">{learningKindLabel(learning.kind)}</Badge>
		</div>
		<div class="flex min-w-0 flex-1 flex-col gap-1.5">
			<p
				class={cn('text-sm', learning.status === 'retired' && 'text-muted-foreground line-through')}
			>
				{learning.text}
			</p>
			<p class="text-muted-foreground flex flex-wrap gap-x-1 text-xs">
				{#if learning.when}<span>When {learning.when}</span><span aria-hidden="true">·</span>{/if}
				<span class="numeric" title="How many runs relied on this learning"
					>{learning.hits === 0
						? 'Not used yet'
						: learning.hits === 1
							? 'Used once'
							: `Used ${learning.hits} times`}</span
				>
				{#if sources.length > 0}
					<span aria-hidden="true">·</span>
					<a
						href="/runs/{sources[0]}"
						class="link-underline"
						title={sources.length > 1 ? `Learned in ${sources.length} runs` : undefined}
						>Source run{#if sources.length > 1}&nbsp;+{sources.length - 1}{/if}</a
					>
				{/if}
			</p>
		</div>
		<div class="flex shrink-0 gap-1">
			<Button
				variant="ghost"
				size="icon-sm"
				aria-label="Edit learning {learning.id}"
				disabled={savingLearnings}
				onclick={() => editLearning(learning)}
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

<!-- One row of the toolkit or of the run scripts: its icon, name and description, and its code once expanded -->
<!-- A part without code, like a missing main script, is a plain row that says so instead of an empty editor -->
<!-- On phones the aside moves under the description, so a narrow screen keeps its width for the text -->
{#snippet partRow(
	kind: PlaybookChangeKind,
	name: Snippet,
	description: string,
	aside: Snippet,
	body?: Snippet
)}
	{#if body}
		<Collapsible.Root>
			<Collapsible.Trigger>
				{#snippet child({ props })}
					<button {...props} class="group flex w-full items-start gap-3 text-left">
						<KindIcon {kind} />
						<span class="flex min-w-0 flex-1 flex-col gap-1">
							{@render name()}
							<span class="text-muted-foreground text-sm">{description}</span>
							<span class="sm:hidden">{@render aside()}</span>
						</span>
						<span class="shrink-0 max-sm:hidden">{@render aside()}</span>
						<ChevronRightIcon
							class="text-muted-foreground mt-1 size-4 shrink-0 transition-transform group-data-[state=open]:rotate-90 motion-reduce:transition-none"
						/>
					</button>
				{/snippet}
			</Collapsible.Trigger>
			<Collapsible.Content>
				<div class="flex flex-col gap-2 pt-3 sm:pl-9">
					{@render body()}
				</div>
			</Collapsible.Content>
		</Collapsible.Root>
	{:else}
		<div class="flex items-start gap-3">
			<KindIcon {kind} />
			<span class="flex min-w-0 flex-1 flex-col gap-1">
				{@render name()}
				<span class="text-muted-foreground text-sm">{description}</span>
				<span class="sm:hidden">{@render aside()}</span>
			</span>
			<span class="shrink-0 max-sm:hidden">{@render aside()}</span>
			<!-- Keeps the asides of plain rows in line with the expandable rows' -->
			<span class="size-4 shrink-0" aria-hidden="true"></span>
		</div>
	{/if}
{/snippet}

{#snippet scriptName(script: PlaybookScript)}
	<span class="flex flex-wrap items-center gap-x-2 gap-y-1">
		<span class="font-mono text-sm font-medium">{script.name}</span>
		<Badge variant="secondary">{scriptLanguageLabel(script.lang)}</Badge>
		{#if script.sideEffects}
			<Badge variant="warning">Side effects</Badge>
		{/if}
	</span>
{/snippet}

{#snippet scriptStats(script: PlaybookScript)}
	<span class="text-muted-foreground numeric mt-0.5 shrink-0 text-xs whitespace-nowrap">
		{script.stats.calls}
		{script.stats.calls === 1 ? 'call' : 'calls'} ·
		<span class={cn(script.stats.failures > 0 && 'text-destructive')}
			>{script.stats.failures} failed</span
		>
	</span>
{/snippet}

{#snippet partName(title: string)}
	<span class="text-sm font-medium">{title}</span>
{/snippet}

{#snippet partState(part: RunPart)}
	<span class="mt-0.5 shrink-0 text-xs whitespace-nowrap">
		{#if part.state}
			<span class="numeric">{part.state}</span>
		{:else}
			<Badge variant="secondary">{part.kind === 'main' ? 'Not yet' : 'Not set'}</Badge>
		{/if}
	</span>
{/snippet}

<svelte:head>
	<title>Playbook · {job.name} · Umpteenth</title>
</svelte:head>

<!-- The current version heads the page like the job overview's performance section: who made it and why -->
<section class="flex flex-col gap-4" aria-labelledby="playbook-version-heading">
	<div class="flex flex-wrap items-start justify-between gap-3">
		<div class="flex min-w-0 flex-col gap-1">
			<h2 id="playbook-version-heading" class="text-lg font-semibold">
				{playbook.version > 0 ? `Version ${playbook.version}` : 'Empty playbook'}
			</h2>
			<div class="text-muted-foreground text-sm">
				{#if playbook.version > 0}
					<VersionMeta
						author={playbook.author}
						createdAt={playbook.createdAt}
						sourceRunId={playbook.sourceRunId}
					/>
				{:else}
					What the job learns from its runs shows up here{job.selfImprove
						? '.'
						: ', once learning is turned on in the settings.'}
				{/if}
			</div>
		</div>
		<div class="flex flex-wrap gap-2">
			{#if playbook.version > 0}
				<Button variant="outline" onclick={() => (diffVersion = playbook.version)}>
					<GitCompareIcon data-icon="inline-start" />
					Show changes
				</Button>
			{/if}
			<Button variant="outline" onclick={() => (jsonOpen = true)}>
				<FileJsonIcon data-icon="inline-start" />
				Edit as JSON
			</Button>
		</div>
	</div>

	{#if playbook.summary}
		<VersionSummary author={playbook.author} summary={playbook.summary} />
	{/if}
</section>

<Card.Root>
	<Card.Header>
		<Card.Title level={3}>Learnings</Card.Title>
		<Card.Description>
			Know-how the agent reads before every run. Retired learnings are kept for the record.
		</Card.Description>
	</Card.Header>
	<Card.Content>
		<!-- Learnings are keyed by position, since an older version may repeat an id and a repeated key would break the page -->
		{#if activeLearnings.length > 0}
			<ul class="divide-y">
				{#each activeLearnings as learning, i (i)}
					{@render learningItem(learning)}
				{/each}
			</ul>
		{:else}
			<p class="text-muted-foreground text-sm">No active learnings yet</p>
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
						{#each retiredLearnings as learning, i (i)}
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
		<Card.Title level={3}>Toolkit</Card.Title>
		<Card.Description>
			Scripts in <code class="font-mono text-xs">/ump/toolkit</code>, also offered to the agent as
			tools.
		</Card.Description>
	</Card.Header>
	<Card.Content>
		{#if toolkit.length > 0}
			<ul class="divide-y">
				{#each toolkit as script (script.name)}
					<li class="py-3 first:pt-0 last:pb-0">
						{#snippet name()}
							{@render scriptName(script)}
						{/snippet}
						{#snippet aside()}
							{@render scriptStats(script)}
						{/snippet}
						{#snippet body()}
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
						{/snippet}
						{@render partRow('script', name, script.description, aside, body)}
					</li>
				{/each}
			</ul>
		{:else}
			<p class="text-muted-foreground text-sm">No toolkit scripts yet</p>
		{/if}
	</Card.Content>
</Card.Root>

<!-- Setup, main, verify and the image are one list of equal rows, so a missing one is as visible as one that is set -->
<Card.Root>
	<Card.Header>
		<Card.Title level={3}>Run scripts</Card.Title>
		<Card.Description>
			What runs around the agent, and the image it runs in. Once the job graduates, main does the
			whole job without the agent.
		</Card.Description>
	</Card.Header>
	<Card.Content>
		<ul class="divide-y">
			{#each runParts as part (part.kind)}
				<li class="py-3 first:pt-0 last:pb-0">
					{#snippet name()}
						{@render partName(part.title)}
					{/snippet}
					{#snippet aside()}
						{@render partState(part)}
					{/snippet}
					{#snippet body()}
						<CodeEditor
							value={part.code ?? ''}
							language={part.language}
							readonly
							label={part.title}
							class="h-auto max-h-72"
						/>
						{#if part.kind === 'main' || part.kind === 'dockerfile'}
							<div class="flex justify-end gap-1">
								{#if part.kind === 'main' && content.main}
									{@const main = content.main}
									<Button variant="ghost" size="sm" onclick={() => editMain(main)}>
										<PencilIcon data-icon="inline-start" />
										Edit
									</Button>
								{:else}
									<Button variant="ghost" size="sm" href="/jobs/{job.id}/environment">
										Open environment
									</Button>
								{/if}
							</div>
						{/if}
					{/snippet}
					{@render partRow(part.kind, name, part.description, aside, part.code ? body : undefined)}
				</li>
			{/each}
		</ul>
	</Card.Content>
</Card.Root>

<Card.Root>
	<Card.Header>
		<Card.Title level={3}>History</Card.Title>
		<Card.Description>
			Every change is a version, whether it came from reflection, a manual edit or a rollback.
		</Card.Description>
	</Card.Header>
	{#if playbook.version === 0}
		<!-- An empty playbook has no versions to load, and says so in the same muted line as the cards above -->
		<Card.Content>
			<p class="text-muted-foreground text-sm">No versions yet</p>
		</Card.Content>
	{:else}
		<Card.Content class="p-0">
			<DataTable
				bind:this={versionsTable}
				flush
				label="Playbook versions"
				{columns}
				fetchPage={(query) => playbookService.versions(job.id, query)}
				getRowId={(item) => String(item.version)}
				onRowClick={(item) => (diffVersion = item.version)}
				defaultSort="-version"
				urlPrefix="versions"
				searchable={playbook.version > 10}
				searchPlaceholder="Search versions"
			>
				{#snippet empty()}
					<p class="text-muted-foreground p-4 text-sm">No versions yet</p>
				{/snippet}
			</DataTable>
		</Card.Content>
	{/if}
</Card.Root>

<JsonEditDialog bind:open={jsonOpen} {content} version={playbook.version} onSave={saveJson} />
<LearningDialog bind:learning={editingLearning} onSave={saveLearning} />
<ScriptDialog bind:target={editingCode} onSave={saveCode} />
<VersionDiffSheet
	jobId={job.id}
	bind:version={diffVersion}
	currentVersion={playbook.version}
	onRollback={confirmRollback}
/>
