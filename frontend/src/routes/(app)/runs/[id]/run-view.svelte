<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import type { RunDetail } from '$lib/api/types';
	import { openConfirmDialog } from '$lib/components/confirm-dialog';
	import { isLiveStatus } from '$lib/components/runs/run-meta';
	import * as Tabs from '$lib/components/ui/tabs';
	import RunService from '$lib/services/run-service';
	import { apiErrorToast } from '$lib/utils/error-util';
	import { tryCatch } from '$lib/utils/try-catch-util';
	import { subscribeWorkspaceEvents } from '$lib/utils/workspace-events';
	import { onMount } from 'svelte';
	import { toast } from 'svelte-sonner';
	import LearnedTab from './learned-tab.svelte';
	import OutputsTab from './outputs-tab.svelte';
	import RawTab from './raw-tab.svelte';
	import RunHeader from './run-header.svelte';
	import { RunStream } from './run-stream.svelte';
	import type { LlmCallPayload, Usage } from './timeline-model';
	import Timeline from './timeline.svelte';
	import Waterfall from './waterfall.svelte';

	let { initialRun }: { initialRun: RunDetail } = $props();

	const TABS = [
		{ value: 'timeline', label: 'Timeline' },
		{ value: 'waterfall', label: 'Waterfall' },
		{ value: 'outputs', label: 'Outputs' },
		{ value: 'learned', label: 'Learned' },
		{ value: 'raw', label: 'Raw' }
	] as const;
	const REFETCH_DELAY_MS = 300;

	const runService = new RunService();

	let run = $state(initialRun);
	const live = $derived(isLiveStatus(run.status));

	const stream = new RunStream(initialRun.id, {
		// The stream only announces status changes from the moment it connects, so one that happened after the page loaded would leave the header stale
		onOpen: () => scheduleRefetch(),
		onStatus: (status) => {
			run.status = status;
			scheduleRefetch();
		},
		onEnd: () => void refetch()
	});

	onMount(() => {
		void stream.start(isLiveStatus(initialRun.status));

		// Reflection runs after the run ended, so its outcome arrives as a workspace event rather than on the run's stream
		const unsubscribe = subscribeWorkspaceEvents({
			onReflection: (event) => {
				if (event.runId === run.id) void refetch();
			},
			onReconnect: () => void refetch()
		});
		return () => {
			stream.stop();
			unsubscribe();
			clearTimeout(refetchTimer);
		};
	});

	// Only runs that ended with a result can be learned from, and not while reflection on them is in progress
	const canLearn = $derived(
		['succeeded', 'failed', 'timed_out'].includes(run.status) && run.reflection !== 'pending'
	);

	async function learn() {
		const result = await tryCatch(runService.learn(run.id));
		if (result.error) {
			apiErrorToast(result.error, 'Failed to start learning from the run');
			return;
		}
		run.reflection = 'pending';
		toast.success('Learning from this run');
		if (activeTab !== 'learned') onTabChange('learned');
	}

	// The run row only gets its totals when the run ends, so live totals are summed from the model calls so far
	const eventTotals = $derived.by(() => {
		let cost = 0;
		let tokensIn = 0;
		let tokensOut = 0;
		let turns = 0;
		for (const event of stream.events) {
			if (event.type !== 'llm.call' && event.type !== 'broker.call') continue;
			const payload = (event.payload ?? {}) as LlmCallPayload & { usage?: Usage };
			cost += payload.cost ?? 0;
			tokensIn += payload.usage?.input ?? 0;
			tokensOut += payload.usage?.output ?? 0;
			if (event.type === 'llm.call') turns++;
		}
		return { cost, tokensIn, tokensOut, turns };
	});
	const totals = $derived(
		live
			? eventTotals
			: { cost: run.cost, tokensIn: run.tokIn, tokensOut: run.tokOut, turns: run.turns }
	);

	// Status notifications come in bursts, so the run is refetched once per burst
	let refetchTimer: ReturnType<typeof setTimeout> | undefined;
	function scheduleRefetch() {
		clearTimeout(refetchTimer);
		refetchTimer = setTimeout(() => void refetch(), REFETCH_DELAY_MS);
	}

	async function refetch() {
		const result = await tryCatch(runService.get(run.id));
		if (result.data) run = result.data;
	}

	// The active tab lives in the URL, so a link can point straight at the waterfall or the outputs
	const activeTab = $derived(page.url.searchParams.get('tab') ?? 'timeline');

	function onTabChange(value: string) {
		const url = new URL(page.url);
		if (value === 'timeline') url.searchParams.delete('tab');
		else url.searchParams.set('tab', value);
		void goto(url, { replaceState: true, noScroll: true, keepFocus: true });
	}

	function confirmCancel() {
		openConfirmDialog({
			title: 'Cancel run',
			message: `Stop ${run.jobName} #${run.number}? The sandbox is destroyed and the run ends as cancelled.`,
			confirm: {
				label: 'Stop run',
				destructive: true,
				action: async () => {
					const result = await tryCatch(runService.cancel(run.id));
					if (result.error) {
						apiErrorToast(result.error, 'Failed to cancel the run');
						return;
					}
					toast.success('Cancelling the run');
					await refetch();
				}
			}
		});
	}

	function confirmRetry() {
		openConfirmDialog({
			title: 'Retry run',
			message: `Start a new run of ${run.jobName} with the same input and instructions?`,
			confirm: {
				label: 'Retry',
				action: async () => {
					const result = await tryCatch(runService.retry(run.id));
					if (result.error) {
						apiErrorToast(result.error, 'Failed to retry the run');
						return;
					}
					if (result.data.status === 'skipped') {
						toast.warning('The retry was skipped because the job is already running');
					}
					await goto(`/runs/${result.data.runId}`);
				}
			}
		});
	}
</script>

<svelte:head>
	<title>{run.jobName} #{run.number} · Runs · Umpteenth</title>
</svelte:head>

<RunHeader
	{run}
	{live}
	cost={totals.cost}
	tokensIn={totals.tokensIn}
	tokensOut={totals.tokensOut}
	turns={totals.turns}
	{canLearn}
	onCancel={confirmCancel}
	onRetry={confirmRetry}
	onLearn={learn}
/>

<Tabs.Root value={activeTab} onValueChange={onTabChange} class="gap-4">
	<Tabs.List variant="line">
		{#each TABS as tab (tab.value)}
			<Tabs.Trigger value={tab.value}>{tab.label}</Tabs.Trigger>
		{/each}
	</Tabs.List>
	<!-- Only the active tab renders, so a streaming run doesn't keep re-rendering the raw JSON and the waterfall in the background -->
	<Tabs.Content value="timeline">
		{#if activeTab === 'timeline'}
			<Timeline {run} {stream} {live} />
		{/if}
	</Tabs.Content>
	<Tabs.Content value="waterfall">
		{#if activeTab === 'waterfall'}
			<Waterfall {run} events={stream.events} {live} />
		{/if}
	</Tabs.Content>
	<Tabs.Content value="outputs">
		{#if activeTab === 'outputs'}
			<OutputsTab {run} {live} />
		{/if}
	</Tabs.Content>
	<Tabs.Content value="learned">
		{#if activeTab === 'learned'}
			<LearnedTab {run} {canLearn} onLearn={learn} />
		{/if}
	</Tabs.Content>
	<Tabs.Content value="raw">
		{#if activeTab === 'raw'}
			<RawTab {run} events={stream.events} />
		{/if}
	</Tabs.Content>
</Tabs.Root>
