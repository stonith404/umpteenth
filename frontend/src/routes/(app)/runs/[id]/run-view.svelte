<script lang="ts">
	import { goto, invalidateAll } from '$app/navigation';
	import { page } from '$app/state';
	import type { RunDetail } from '$lib/api/types';
	import { openConfirmDialog } from '$lib/components/confirm-dialog';
	import { isLiveStatus } from '$lib/components/runs/run-meta';
	import * as Alert from '$lib/components/ui/alert';
	import * as Tabs from '$lib/components/ui/tabs';
	import RunService from '$lib/services/run-service';
	import { apiErrorToast } from '$lib/utils/error-util';
	import { tryCatch } from '$lib/utils/try-catch-util';
	import { subscribeWorkspaceEvents } from '$lib/utils/workspace-events';
	import { hasRole } from '$lib/utils/workspace-util';
	import { onMount } from 'svelte';
	import WifiOffIcon from '@lucide/svelte/icons/wifi-off';
	import { toast } from 'svelte-sonner';
	import LearnedTab from './learned-tab.svelte';
	import LiveBar from './live-bar.svelte';
	import OutputsTab from './outputs-tab.svelte';
	import RawTab from './raw-tab.svelte';
	import RunHeader from './run-header.svelte';
	import { RunStream } from './run-stream.svelte';
	import {
		payloadOf,
		turnsTaken,
		type BrokerCallPayload,
		type LlmCallPayload
	} from './timeline-model';
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
	const RECONNECT_NOTICE_DELAY_MS = 2000;

	const runService = new RunService();

	// The run is only ever replaced as a whole, so it needs no deep reactivity
	let run = $state.raw(initialRun);
	const live = $derived(isLiveStatus(run.status));

	// Deleting erases the run's transcript and usage for everyone, so only admins may
	const canDelete = $derived(hasRole(page.data.user!, 'admin'));
	let deleting = false;

	const stream = new RunStream(initialRun.id, {
		// The stream only announces status changes from the moment it connects, so one that happened after the page loaded would leave the header stale
		onOpen: () => scheduleRefetch(),
		onStatus: (status) => {
			run = { ...run, status };
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
			// A run deleted in another tab or by someone else can't be shown anymore, so the page reloads into its not-found state
			onRun: (event) => {
				if (event.runId === run.id && event.deleted && !deleting) void invalidateAll();
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
		run = { ...run, reflection: 'pending' };
		toast.success('Learning from this run');
		if (activeTab !== 'learned') onTabChange('learned');
	}

	// The run row only gets its totals when the run ends, so live totals are summed from the model calls so far
	// A model call that is tried again records every attempt under the same turn, so turns are counted by number, as the backend does
	const eventTotals = $derived.by(() => {
		let cost = 0;
		let tokensIn = 0;
		let tokensOut = 0;
		for (const event of stream.events) {
			if (event.type !== 'llm.call' && event.type !== 'broker.call') continue;
			const payload = payloadOf<LlmCallPayload | BrokerCallPayload>(event);
			cost += payload.cost ?? 0;
			tokensIn += payload.usage?.input ?? 0;
			tokensOut += payload.usage?.output ?? 0;
		}
		return { cost, tokensIn, tokensOut, turns: turnsTaken(stream.events) };
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

	// A live run's timeline follows new output and scrolls the header away, so a compact bar keeps the status and Stop in reach
	let header: HTMLElement | null = $state(null);
	let liveBarAnchor: HTMLElement | undefined = $state();
	let actionsHidden = $state(false);
	$effect(() => {
		if (!header || !liveBarAnchor || !live) {
			actionsHidden = false;
			return;
		}
		// The bar takes over once the header's Stop is out of reach, which happens long before the stats and alerts below it scroll away
		const actions = header.querySelector('[data-slot="page-header-actions"]') ?? header;

		// The sticky app header covers the top of the page, so the actions count as gone once they have slid under it, which is where the bar sticks
		const offset = Number.parseFloat(getComputedStyle(liveBarAnchor).top) || 0;
		const observer = new IntersectionObserver(
			([entry]) => (actionsHidden = !entry.isIntersecting && entry.boundingClientRect.top < offset),
			{ rootMargin: `-${Math.round(offset)}px 0px 0px 0px` }
		);
		observer.observe(actions);
		return () => observer.disconnect();
	});

	// A dropped stream usually comes back within a moment, so the notice only shows once reconnecting takes a while, rather than flashing and moving the timeline
	let reconnecting = $state(false);
	$effect(() => {
		if (!live || stream.connection !== 'reconnecting') {
			reconnecting = false;
			return;
		}
		const timer = setTimeout(() => (reconnecting = true), RECONNECT_NOTICE_DELAY_MS);
		return () => clearTimeout(timer);
	});

	// Refetches overlap, e.g. a status burst and the stream's end, so only the latest one may replace the run
	let refetchSeq = 0;
	async function refetch() {
		const seq = ++refetchSeq;
		const result = await tryCatch(runService.get(run.id));
		if (result.data && seq === refetchSeq) run = result.data;
	}

	// The active tab lives in the URL, so a link can point straight at the waterfall or the outputs
	const activeTab = $derived.by(() => {
		const tab = page.url.searchParams.get('tab');
		return TABS.find((t) => t.value === tab)?.value ?? 'timeline';
	});

	function onTabChange(value: string) {
		const url = new URL(page.url);
		if (value === 'timeline') url.searchParams.delete('tab');
		else url.searchParams.set('tab', value);
		void goto(url, { replaceState: true, noScroll: true, keepFocus: true });
	}

	// The action is Stop throughout, so the dialog's own Cancel can only mean leaving the run alone
	function confirmCancel() {
		openConfirmDialog({
			title: 'Stop this run?',
			message: `${run.jobName} #${run.number} ends as cancelled and its sandbox is destroyed.`,
			confirm: {
				label: 'Stop run',
				destructive: true,
				action: async () => {
					const result = await tryCatch(runService.cancel(run.id));
					if (result.error) {
						apiErrorToast(result.error, 'Failed to stop the run');
						return;
					}
					toast.success('Stopping the run');
					await refetch();
				}
			}
		});
	}

	function confirmDelete() {
		openConfirmDialog({
			title: `Delete ${run.jobName} #${run.number}`,
			message:
				"The run's timeline and artifacts are deleted, and its usage no longer counts toward the workspace's totals or daily limit. This can't be undone.",
			confirm: {
				label: 'Delete',
				destructive: true,
				action: async () => {
					deleting = true;
					const result = await tryCatch(runService.delete(run.id));
					if (result.error) {
						deleting = false;
						apiErrorToast(result.error, 'Failed to delete the run');
						return;
					}
					toast.success(`Deleted "${run.jobName} #${run.number}"`);
					await goto(`/jobs/${run.jobId}/runs`);
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
						toast.info('Retry skipped', {
							description: 'Another run of this job is still active.'
						});
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

<!-- A zero-height sticky anchor at the top of the page, which sticks right under the app header as soon as the page scrolls -->
<!-- Its negative margin cancels the page's gap, so it takes no room -->
<div bind:this={liveBarAnchor} class="sticky top-14.5 z-20 -mb-6 h-0">
	{#if live && actionsHidden}
		<LiveBar {run} onCancel={confirmCancel} />
	{/if}
</div>

<RunHeader
	bind:ref={header}
	{run}
	{live}
	cost={totals.cost}
	tokensIn={totals.tokensIn}
	tokensOut={totals.tokensOut}
	turns={totals.turns}
	{canLearn}
	showLearn={activeTab !== 'learned'}
	hideCancel={actionsHidden}
	onCancel={confirmCancel}
	onRetry={confirmRetry}
	onLearn={learn}
	onDelete={canDelete ? confirmDelete : undefined}
/>

{#if reconnecting}
	<Alert.Root variant="warning">
		<WifiOffIcon />
		<Alert.Title>Live updates paused, reconnecting…</Alert.Title>
		<Alert.Description
			>The run keeps going on the server. This page catches up once the connection is back.</Alert.Description
		>
	</Alert.Root>
{/if}

<Tabs.Root value={activeTab} onValueChange={onTabChange} spacing="md">
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
