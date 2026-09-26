<script lang="ts" module>
	// Build states of a job image (PLAN §4.11), toned like run statuses: live blue, ready green, failed red
	const tones: Record<string, string> = {
		queued: 'bg-muted text-muted-foreground',
		building: 'bg-blue-500/10 text-blue-700 dark:bg-blue-500/15 dark:text-blue-400',
		ready: 'bg-green-500/10 text-green-700 dark:bg-green-500/15 dark:text-green-400',
		failed: 'bg-red-500/10 text-red-700 dark:bg-red-500/15 dark:text-red-400'
	};

	const labels: Record<string, string> = {
		queued: 'Queued',
		building: 'Building',
		ready: 'Ready',
		failed: 'Failed'
	};

	// Statuses that still change, so views poll until the build settles
	export function isImageBuilding(status: string) {
		return status === 'queued' || status === 'building';
	}
</script>

<script lang="ts">
	import { cn } from '$lib/utils/style';
	import CircleCheckIcon from '@lucide/svelte/icons/circle-check';
	import CircleDashedIcon from '@lucide/svelte/icons/circle-dashed';
	import CircleXIcon from '@lucide/svelte/icons/circle-x';
	import LoaderCircleIcon from '@lucide/svelte/icons/loader-circle';

	let { status, class: className }: { status: string; class?: string } = $props();
</script>

<span
	data-slot="image-status-badge"
	data-status={status}
	class={cn(
		'inline-flex h-5 w-fit shrink-0 items-center gap-1 rounded-3xl px-2 text-xs font-medium whitespace-nowrap',
		tones[status] ?? tones.queued,
		className
	)}
>
	{#if status === 'building'}
		<LoaderCircleIcon class="size-3 animate-spin" aria-hidden="true" />
	{:else if status === 'ready'}
		<CircleCheckIcon class="size-3" aria-hidden="true" />
	{:else if status === 'failed'}
		<CircleXIcon class="size-3" aria-hidden="true" />
	{:else}
		<CircleDashedIcon class="size-3" aria-hidden="true" />
	{/if}
	{labels[status] ?? status}
</span>
