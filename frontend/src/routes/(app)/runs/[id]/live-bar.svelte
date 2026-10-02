<script lang="ts">
	import type { RunDetail } from '#lib/api/types.js';
	import RunDuration from '#lib/components/runs/run-duration.svelte';
	import StatusBadge from '#lib/components/runs/status-badge.svelte';
	import { Button } from '#lib/components/ui/button/index.js';
	import CircleStopIcon from '@lucide/svelte/icons/circle-stop';

	// A compact stand-in for the run header while a live run's timeline has scrolled it away
	let { run, onCancel }: { run: RunDetail; onCancel: () => void } = $props();
</script>

<div
	data-slot="run-live-bar"
	class="bg-popover ring-border absolute inset-x-0 top-2 flex items-center gap-3 rounded-lg py-2 pr-2 pl-4 shadow-md ring-1 motion-safe:animate-in motion-safe:fade-in-0 motion-safe:slide-in-from-top-1"
>
	<!-- A long job name truncates on its own, so the run number after it stays visible -->
	<p class="flex min-w-0 flex-1 items-baseline gap-1.5 text-sm font-medium">
		<span class="truncate">{run.jobName}</span>
		<span class="text-muted-foreground numeric shrink-0 font-normal">#{run.number}</span>
	</p>
	<StatusBadge status={run.status} />
	<span class="text-muted-foreground text-sm max-sm:hidden">
		<RunDuration {run} plain />
	</span>
	<Button variant="outline" size="sm" onclick={onCancel} disabled={run.cancelRequested}>
		<CircleStopIcon data-icon="inline-start" />
		Stop
	</Button>
</div>
