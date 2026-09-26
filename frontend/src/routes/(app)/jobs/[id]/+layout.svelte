<script lang="ts">
	import { goto, invalidate } from '$app/navigation';
	import { page } from '$app/state';
	import RunNowDialog from '$lib/components/jobs/run-now-dialog.svelte';
	import RelativeTime from '$lib/components/relative-time.svelte';
	import ModeBadge from '$lib/components/runs/mode-badge.svelte';
	import StatusBadge from '$lib/components/runs/status-badge.svelte';
	import { Badge } from '$lib/components/ui/badge';
	import { Button } from '$lib/components/ui/button';
	import { Label } from '$lib/components/ui/label';
	import { Switch } from '$lib/components/ui/switch';
	import * as Tabs from '$lib/components/ui/tabs';
	import JobService from '$lib/services/job-service';
	import { apiErrorToast } from '$lib/utils/error-util';
	import { jobScheduleLabel } from '$lib/utils/job-util';
	import { tryCatch } from '$lib/utils/try-catch-util';
	import BookOpenIcon from '@lucide/svelte/icons/book-open';
	import CalendarClockIcon from '@lucide/svelte/icons/calendar-clock';
	import ClockIcon from '@lucide/svelte/icons/clock';
	import PlayIcon from '@lucide/svelte/icons/play';
	import type { Snippet } from 'svelte';
	import { toast } from 'svelte-sonner';
	import { activeJobTab, jobTabs } from './job-tabs';

	let { data, children }: { data: import('./$types').LayoutData; children: Snippet } = $props();

	const jobService = new JobService();

	const job = $derived(data.job);
	const schedule = $derived(jobScheduleLabel(job));
	const activeTab = $derived(activeJobTab(page.url.pathname, job.id));

	let runNowOpen = $state(false);
	let togglingEnabled = $state(false);

	function onTabChange(value: string) {
		const tab = jobTabs.find((t) => t.value === value);
		if (tab) void goto(`/jobs/${job.id}${tab.path}`);
	}

	// The update endpoint merges, so only the enabled state is sent and edits made elsewhere in the meantime are kept
	async function setEnabled(enabled: boolean) {
		togglingEnabled = true;
		const result = await tryCatch(jobService.update(job.id, { enabled }));
		togglingEnabled = false;
		if (result.error) {
			apiErrorToast(result.error, 'Failed to update the job');
			return;
		}
		toast.success(enabled ? 'Job enabled' : 'Job paused');
		await invalidate('app:job');
	}
</script>

<div class="flex flex-col gap-6">
	<div class="flex flex-wrap items-start justify-between gap-4">
		<div class="flex min-w-0 flex-col gap-2">
			<h1 class="truncate text-2xl font-semibold">{job.name}</h1>
			<div class="text-muted-foreground flex flex-wrap items-center gap-x-4 gap-y-1.5 text-sm">
				<span class="inline-flex items-center gap-1.5">
					<CalendarClockIcon class="size-4" />
					{schedule ?? 'On demand'}
				</span>
				{#if job.cron}
					<span class="inline-flex items-center gap-1.5">
						<ClockIcon class="size-4" />
						{#if !job.enabled}
							Paused
						{:else if job.nextRunAt}
							Next run <RelativeTime value={job.nextRunAt} />
						{:else}
							No upcoming run
						{/if}
					</span>
				{/if}
				{#if job.lastRun}
					<a href="/runs/{job.lastRun.id}" class="inline-flex items-center gap-1.5 hover:underline">
						Last run
						<StatusBadge status={job.lastRun.status} />
					</a>
				{/if}
				<a
					href="/jobs/{job.id}/playbook"
					class="inline-flex items-center gap-1.5 hover:underline"
					title="Current playbook version"
				>
					<BookOpenIcon class="size-4" />
					{job.playbookVersion > 0 ? `Playbook v${job.playbookVersion}` : 'No playbook yet'}
				</a>
				{#if job.modePin}
					<span class="inline-flex items-center gap-1.5">
						Pinned to <ModeBadge mode={job.modePin} />
					</span>
				{:else if job.nextMode}
					<span class="inline-flex items-center gap-1.5" title="The mode the next run starts in">
						Runs as <ModeBadge mode={job.nextMode} />
					</span>
				{/if}
				{#if job.demoted}
					<Badge
						variant="outline"
						class="font-normal"
						title="The main script fell back to the agent twice in a row, so runs are Assisted until the job graduates again"
					>
						Demoted
					</Badge>
				{/if}
				{#if !job.selfImprove}
					<Badge variant="outline" class="font-normal">Learning off</Badge>
				{/if}
			</div>
		</div>
		<div class="flex items-center gap-4">
			<div class="flex items-center gap-2">
				<Switch
					id="job-enabled"
					checked={job.enabled}
					disabled={togglingEnabled}
					onCheckedChange={setEnabled}
				/>
				<Label for="job-enabled" class="font-normal">{job.enabled ? 'Enabled' : 'Paused'}</Label>
			</div>
			<Button onclick={() => (runNowOpen = true)}>
				<PlayIcon data-icon="inline-start" />
				Run now
			</Button>
		</div>
	</div>

	<Tabs.Root value={activeTab} onValueChange={onTabChange} class="gap-6">
		<Tabs.List variant="line">
			{#each jobTabs as tab (tab.value)}
				<Tabs.Trigger value={tab.value}>{tab.label}</Tabs.Trigger>
			{/each}
		</Tabs.List>
		<Tabs.Content value={activeTab} class="flex flex-col gap-6">
			<!-- Moving to another job on the same tab remounts the page, so it never shows the previous job's tables or forms -->
			{#key job.id}
				{@render children()}
			{/key}
		</Tabs.Content>
	</Tabs.Root>
</div>

<RunNowDialog bind:open={runNowOpen} {job} />
