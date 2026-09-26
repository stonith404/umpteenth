<script lang="ts" module>
	// Other parts of the shell (e.g. the header's search button) open the palette through this state
	export const commandPalette = $state({ open: false });
</script>

<script lang="ts">
	import { goto } from '$app/navigation';
	import type { JobListItem, Run, User } from '$lib/api/types';
	import StatusBadge from '$lib/components/runs/status-badge.svelte';
	import * as Command from '$lib/components/ui/command';
	import * as Dialog from '$lib/components/ui/dialog';
	import { Spinner } from '$lib/components/ui/spinner';
	import { searchablePages, type SearchablePage } from '$lib/navigation';
	import JobService from '$lib/services/job-service';
	import RunService from '$lib/services/run-service';
	import { debounced } from '$lib/utils/debounce-util';
	import { formatRelative } from '$lib/utils/format-util';
	import { scheduleLabel } from '$lib/utils/job-util';
	import { tryCatch } from '$lib/utils/try-catch-util';
	import BriefcaseIcon from '@lucide/svelte/icons/briefcase';
	import PlusIcon from '@lucide/svelte/icons/plus';
	import SearchIcon from '@lucide/svelte/icons/search';
	import { Command as CommandPrimitive } from 'bits-ui';
	import { untrack } from 'svelte';

	const SEARCH_DEBOUNCE_MS = 200;
	const RESULT_LIMIT = 5;
	// The server doesn't search run numbers, so a number is looked up among this many of the newest runs, the most one request returns
	const RUN_NUMBER_SCAN = 100;

	type Group = 'actions' | 'pages' | 'jobs' | 'runs';
	// Server results together with the query they answer, so results of an older query can be told apart while a newer one loads
	type Results = { query: string; jobs: JobListItem[]; runs: Run[] };

	let { user }: { user: User } = $props();

	const runService = new RunService();
	const jobService = new JobService();

	// Things to start rather than places to go, kept apart so they lead the palette before anything is typed
	const actions: SearchablePage[] = [
		{
			id: 'create-job',
			label: 'Create job',
			href: '/jobs/new',
			icon: PlusIcon,
			keywords: 'new add'
		}
	];

	// Destinations, filtered on the client since they only change with who is signed in
	const pages = $derived(searchablePages(user));

	let query = $state('');
	const noResults: Results = { query: '', jobs: [], runs: [] };
	let results = $state.raw<Results>(noResults);
	// The recent jobs and runs from the last time the palette opened, shown again right away when it opens or the query is cleared
	let recent: Results | null = null;
	let loading = $state(false);
	let searchSeq = 0;
	// The row Enter opens, which is kept on the top row whenever the rows change
	let selected = $state('');
	let listRef = $state<HTMLElement | null>(null);

	// A run is found by its number too, on its own ('#14' or '14') or after words of its job's name ('triage #14')
	function parseRunNumber(text: string): { name: string; number: number } | null {
		const match = /^(?:(.*?)\s*#|)(\d+)$/.exec(text);
		if (!match) return null;
		return { name: (match[1] ?? '').trim(), number: Number(match[2]) };
	}

	function wordsOf(text: string) {
		return text.toLowerCase().split(/[^\p{L}\p{N}]+/u);
	}

	// How closely a name matches the query, which decides what leads the palette and so what Enter opens
	// 3 when the name starts with the query, 2 when every term starts one of its words, and 1 for a match elsewhere, e.g. in a job's instruction
	function nameScore(name: string, q: string, terms: string[]) {
		if (name.toLowerCase().startsWith(q)) return 3;
		const words = wordsOf(name);
		return terms.every((term) => words.some((word) => word.startsWith(term))) ? 2 : 1;
	}

	// Every term has to occur in a name, the rule the client applies to results that belong to an older query
	function containsTerms(name: string, terms: string[]) {
		const text = name.toLowerCase();
		return terms.every((term) => text.includes(term));
	}

	// Sorts by score, where equal scores keep their order, e.g. the newest run first
	function ranked<T>(items: T[], score: (item: T) => number) {
		return items
			.map((item) => ({ item, score: score(item) }))
			.sort((a, b) => b.score - a.score)
			.map(({ item }) => item);
	}

	const q = $derived(query.trim().toLowerCase());
	const terms = $derived(q.split(/\s+/).filter(Boolean));
	const runNumber = $derived(parseRunNumber(q));

	// Pages and actions match when every term occurs in their label or keywords, the closest ones first
	function matching(items: SearchablePage[]) {
		if (!q) return items;
		const matches = items.filter((item) =>
			containsTerms(`${item.label} ${item.keywords ?? ''}`, terms)
		);
		return ranked(matches, (item) => nameScore(item.label, q, terms));
	}
	const visibleActions = $derived(matching(actions));
	const visiblePages = $derived(matching(pages));

	// Results of an older query stay while the newer one loads, but only those that match what is typed now, so the top row never opens something unrelated
	const fresh = $derived(results.query === q);
	const jobs = $derived.by(() => {
		if (!q) return results.jobs;
		const matches = fresh
			? results.jobs
			: results.jobs.filter((job) => containsTerms(job.name, terms));
		return ranked(matches, (job) => nameScore(job.name, q, terms));
	});
	const runs = $derived.by(() => {
		if (!q) return results.runs;
		if (runNumber) {
			const nameTerms = runNumber.name.split(/\s+/).filter(Boolean);
			return results.runs.filter(
				(run) => run.number === runNumber.number && containsTerms(run.jobName, nameTerms)
			);
		}
		const matches = fresh
			? results.runs
			: results.runs.filter((run) => containsTerms(run.jobName, terms));
		return ranked(matches, (run) => nameScore(run.jobName, q, terms));
	});
	const searching = $derived(!!q && (!fresh || loading));

	// Before anything is typed the palette starts with what to do and what happened recently, and the pages the sidebar also lists come last
	// Once something is typed the closest match leads, and on a tie pages and actions go first since they don't move once the server answers
	const groups = $derived.by(() => {
		const counts: Record<Group, number> = {
			actions: visibleActions.length,
			pages: visiblePages.length,
			jobs: jobs.length,
			runs: runs.length
		};
		if (!q) {
			return (['actions', 'runs', 'jobs', 'pages'] as const).filter((group) => counts[group] > 0);
		}
		const best = (names: string[]) =>
			Math.max(0, ...names.map((name) => nameScore(name, q, terms)));
		const scores: Record<Group, number> = {
			pages: best(visiblePages.map((page) => page.label)),
			actions: best(visibleActions.map((action) => action.label)),
			jobs: best(jobs.map((job) => job.name)),
			// A run number names one run, which beats any name match
			runs: runNumber ? 4 : best(runs.map((run) => run.jobName))
		};
		return (['pages', 'actions', 'jobs', 'runs'] as const)
			.filter((group) => counts[group] > 0)
			.sort((a, b) => scores[b] - scores[a]);
	});

	// The rows' values in the order they render
	const rowValues = $derived(
		groups.flatMap((group) => {
			if (group === 'actions') return visibleActions.map((action) => action.id);
			if (group === 'pages') return visiblePages.map((page) => page.id);
			if (group === 'jobs') return jobs.map((job) => `job-${job.id}`);
			return runs.map((run) => `run-${run.id}`);
		})
	);
	const rowKey = $derived(rowValues.join('\n'));

	// Whenever the rows change the top one becomes the row Enter opens, while arrowing through unchanged rows keeps the choice
	// The command list only moves its selection when the selected row itself goes away, and not always then, so the palette decides
	$effect(() => {
		void rowKey;
		untrack(() => {
			selected = rowValues[0] ?? '';
			if (listRef) listRef.scrollTop = 0;
		});
	});

	// Jobs and runs are searched on the server, with the most recent ones shown before anything is typed
	async function search(text: string) {
		const seq = ++searchSeq;
		loading = true;
		const params = { page: 1, pageSize: RESULT_LIMIT, ...(text ? { search: text } : {}) };
		// A run number is picked out on the client, from the newest runs of the jobs the words before it name
		const number = parseRunNumber(text.toLowerCase());
		const runParams = number
			? { page: 1, pageSize: RUN_NUMBER_SCAN, ...(number.name ? { search: number.name } : {}) }
			: params;
		const [runResult, jobResult] = await Promise.all([
			tryCatch(runService.list(runParams)),
			tryCatch(jobService.list(params))
		]);
		if (seq !== searchSeq) return;
		loading = false;
		let foundRuns = runResult.data?.items ?? [];
		if (number) foundRuns = foundRuns.filter((run) => run.number === number.number);
		results = {
			query: text.toLowerCase(),
			jobs: jobResult.data?.items ?? [],
			runs: foundRuns.slice(0, RESULT_LIMIT)
		};
		if (!text) recent = results;
	}
	const searchDebounced = debounced(search, SEARCH_DEBOUNCE_MS);

	// Opening starts from a clean slate with fresh recent items, so a search still pending from the last time must not land afterwards
	$effect(() => {
		if (!commandPalette.open) return;
		untrack(() => {
			searchDebounced.cancel();
			query = '';
			results = recent ?? noResults;
			// The rows may be the ones it closed with, so the selection starts over explicitly instead of where the arrows left it
			selected = rowValues[0] ?? '';
			void search('');
		});
	});

	function onQueryInput(value: string) {
		query = value;
		const text = value.trim();
		// Clearing the query brings back the recent items at once, and drops a search still on its way
		if (!text && recent) {
			searchDebounced.cancel();
			searchSeq++;
			loading = false;
			results = recent;
			return;
		}
		searchDebounced(text);
	}

	function select(href: string) {
		commandPalette.open = false;
		void goto(href);
	}

	// ⌘K on macOS, Ctrl+K elsewhere
	function onKeydown(event: KeyboardEvent) {
		if (event.key.toLowerCase() === 'k' && (event.metaKey || event.ctrlKey)) {
			event.preventDefault();
			commandPalette.open = !commandPalette.open;
		}
	}
</script>

<svelte:window onkeydown={onKeydown} />

{#snippet pageItems(items: SearchablePage[])}
	{#each items as item (item.id)}
		<Command.Item value={item.id} onSelect={() => select(item.href)}>
			<item.icon class="text-muted-foreground" />
			{item.label}
		</Command.Item>
	{/each}
{/snippet}

<!-- Composed from the dialog and command parts rather than Command.Dialog, since Kumo's palette is wider, sits higher and has a flush search row and a footer -->
<Dialog.Root bind:open={commandPalette.open}>
	<Dialog.Content
		showCloseButton={false}
		class="top-[10vh] gap-0 overflow-hidden p-0 sm:top-[10vh] sm:max-w-2xl"
	>
		<Dialog.Title class="sr-only">Command palette</Dialog.Title>
		<Dialog.Description class="sr-only">
			Jump to a job, run or page, or start something new
		</Dialog.Description>
		<Command.Root
			bind:value={selected}
			shouldFilter={false}
			class="min-h-0 rounded-none bg-transparent p-0"
		>
			<div class="flex shrink-0 items-center gap-3 border-b px-4">
				<SearchIcon class="text-muted-foreground size-4 shrink-0" />
				<CommandPrimitive.Input
					value={query}
					oninput={(e) => onQueryInput(e.currentTarget.value)}
					placeholder="Search jobs, runs and pages…"
					class="placeholder:text-muted-foreground h-12 min-w-0 flex-1 bg-transparent text-base outline-none"
				/>
				{#if searching}
					<Spinner class="text-muted-foreground shrink-0" />
				{/if}
			</div>
			<Command.List bind:ref={listRef} class="max-h-[min(28rem,60dvh)] scroll-py-2 p-2">
				{#if !searching && groups.length === 0}
					<Command.Empty class="text-muted-foreground py-8">
						No jobs, runs or pages match "{query.trim()}"
					</Command.Empty>
				{/if}
				{#each groups as group (group)}
					{#if group === 'actions'}
						<Command.Group heading="Actions" class="px-0 py-1">
							{@render pageItems(visibleActions)}
						</Command.Group>
					{:else if group === 'pages'}
						<Command.Group heading="Go to" class="px-0 py-1">
							{@render pageItems(visiblePages)}
						</Command.Group>
					{:else if group === 'jobs'}
						<Command.Group heading="Jobs" class="px-0 py-1">
							{#each jobs as job (job.id)}
								<Command.Item value="job-{job.id}" onSelect={() => select(`/jobs/${job.id}`)}>
									<BriefcaseIcon class="text-muted-foreground" />
									<span class="min-w-0 truncate">{job.name}</span>
									<!-- Phones need the width for the name, and the job page shows the schedule anyway -->
									<span
										class="text-muted-foreground ml-auto hidden shrink-0 pl-4 text-sm sm:inline"
									>
										{scheduleLabel(job) ?? 'On demand'}
									</span>
								</Command.Item>
							{/each}
						</Command.Group>
					{:else}
						<Command.Group heading={q ? 'Runs' : 'Recent runs'} class="px-0 py-1">
							{#each runs as run (run.id)}
								<Command.Item value="run-{run.id}" onSelect={() => select(`/runs/${run.id}`)}>
									<!-- The status takes the 16px of the other rows' icons, so every name starts on one line -->
									<StatusBadge status={run.status} iconOnly class="size-4" />
									<!-- The name truncates before the run number does, since the number tells runs of one job apart -->
									<span class="min-w-0 truncate">{run.jobName}</span>
									<span class="text-muted-foreground numeric -ml-1 shrink-0">#{run.number}</span>
									<span class="text-muted-foreground ml-auto shrink-0 pl-4 text-sm">
										{formatRelative(run.startedAt ?? run.queuedAt)}
									</span>
								</Command.Item>
							{/each}
						</Command.Group>
					{/if}
				{/each}
			</Command.List>
			<!-- Key hints only help with a keyboard, which phones don't have -->
			<div
				class="bg-layer text-muted-foreground hidden shrink-0 items-center gap-4 border-t px-4 py-2.5 text-xs sm:flex"
				aria-hidden="true"
			>
				<span><kbd class="font-sans">↑↓</kbd> to navigate</span>
				<span><kbd class="font-sans">↵</kbd> to open</span>
				<span><kbd class="font-sans">esc</kbd> to close</span>
			</div>
		</Command.Root>
	</Dialog.Content>
</Dialog.Root>
