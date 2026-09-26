<script lang="ts" module>
	// Other parts of the shell (e.g. the header's search button) open the palette through this state
	export const commandPalette = $state({ open: false });
</script>

<script lang="ts">
	import { goto } from '$app/navigation';
	import type { JobListItem, Run } from '$lib/api/types';
	import StatusBadge from '$lib/components/runs/status-badge.svelte';
	import * as Command from '$lib/components/ui/command';
	import { mainNav, secondaryNav } from '$lib/navigation';
	import JobService from '$lib/services/job-service';
	import RunService from '$lib/services/run-service';
	import { debounced } from '$lib/utils/debounce-util';
	import { formatRelative } from '$lib/utils/format-util';
	import { tryCatch } from '$lib/utils/try-catch-util';
	import BriefcaseIcon from '@lucide/svelte/icons/briefcase';
	import PlusIcon from '@lucide/svelte/icons/plus';
	import type { Component } from 'svelte';

	const SEARCH_DEBOUNCE_MS = 200;
	const RESULT_LIMIT = 5;

	const runService = new RunService();
	const jobService = new JobService();

	type Action = { id: string; label: string; href: string; icon: Component; keywords?: string };

	// Static destinations, filtered on the client since they never change
	const actions: Action[] = [
		{ id: 'new-job', label: 'New job', href: '/jobs/new', icon: PlusIcon, keywords: 'create add' },
		...[...mainNav, ...secondaryNav].map((item) => ({
			id: `nav-${item.href}`,
			label: item.label,
			href: item.href,
			icon: item.icon,
			keywords: 'go to open'
		}))
	];

	let query = $state('');
	let runs = $state.raw<Run[]>([]);
	let jobs = $state.raw<JobListItem[]>([]);
	let loading = $state(false);
	let searchSeq = 0;

	const visibleActions = $derived.by(() => {
		const q = query.trim().toLowerCase();
		if (!q) return actions;
		return actions.filter((a) => `${a.label} ${a.keywords ?? ''}`.toLowerCase().includes(q));
	});

	// Search results lead once something is typed, before that the shortcuts do
	const groups = $derived.by(() => {
		const order = query.trim()
			? (['jobs', 'runs', 'actions'] as const)
			: (['actions', 'runs', 'jobs'] as const);
		return order.filter((group) => {
			if (group === 'actions') return visibleActions.length > 0;
			if (group === 'jobs') return jobs.length > 0;
			return runs.length > 0;
		});
	});

	// Jobs and runs are searched on the server, with the most recent ones shown before anything is typed
	async function search(q: string) {
		const seq = ++searchSeq;
		loading = true;
		const params = { page: 1, pageSize: RESULT_LIMIT, ...(q ? { search: q } : {}) };
		const [runResult, jobResult] = await Promise.all([
			tryCatch(runService.list(params)),
			tryCatch(jobService.list(params))
		]);
		if (seq !== searchSeq) return;
		loading = false;
		runs = runResult.data?.items ?? [];
		jobs = jobResult.data?.items ?? [];
	}
	const searchDebounced = debounced(search, SEARCH_DEBOUNCE_MS);

	// Opening starts from a clean slate with fresh recent items
	$effect(() => {
		if (!commandPalette.open) return;
		query = '';
		void search('');
	});

	function onQueryInput(value: string) {
		query = value;
		searchDebounced(value.trim());
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

<Command.Dialog
	bind:open={commandPalette.open}
	shouldFilter={false}
	title="Command palette"
	description="Jump to a job or run, or start something new"
>
	<Command.Input
		placeholder="Search jobs and runs…"
		value={query}
		oninput={(e) => onQueryInput(e.currentTarget.value)}
	/>
	<Command.List>
		{#if !loading && groups.length === 0}
			<Command.Empty>No results</Command.Empty>
		{/if}
		{#each groups as group, i (group)}
			{#if i > 0}
				<Command.Separator />
			{/if}
			{#if group === 'actions'}
				<Command.Group heading="Go to">
					{#each visibleActions as action (action.id)}
						<Command.Item value={action.id} onSelect={() => select(action.href)}>
							<action.icon />
							{action.label}
						</Command.Item>
					{/each}
				</Command.Group>
			{:else if group === 'jobs'}
				<Command.Group heading="Jobs">
					{#each jobs as job (job.id)}
						<Command.Item value="job-{job.id}" onSelect={() => select(`/jobs/${job.id}`)}>
							<BriefcaseIcon />
							<span class="truncate">{job.name}</span>
							{#if job.scheduleHuman}
								<span class="text-muted-foreground ml-auto truncate text-xs"
									>{job.scheduleHuman}</span
								>
							{/if}
						</Command.Item>
					{/each}
				</Command.Group>
			{:else}
				<Command.Group heading={query.trim() ? 'Runs' : 'Recent runs'}>
					{#each runs as run (run.id)}
						<Command.Item value="run-{run.id}" onSelect={() => select(`/runs/${run.id}`)}>
							<StatusBadge status={run.status} iconOnly />
							<span class="truncate">
								{run.jobName}
								<span class="text-muted-foreground numeric">#{run.number}</span>
							</span>
							<span class="text-muted-foreground ml-auto shrink-0 text-xs">
								{formatRelative(run.startedAt ?? run.queuedAt)}
							</span>
						</Command.Item>
					{/each}
				</Command.Group>
			{/if}
		{/each}
	</Command.List>
</Command.Dialog>
