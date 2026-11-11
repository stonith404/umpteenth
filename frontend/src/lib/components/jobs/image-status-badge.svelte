<script lang="ts" module>
	import type { BadgeVariant } from '#lib/components/ui/badge/index.js';

	// Build states of a job image, toned like run statuses: live blue, ready green, failed red
	const tones: Record<string, { badge: BadgeVariant; icon: string; label: string }> = {
		queued: { badge: 'secondary', icon: 'text-muted-foreground', label: 'text-foreground' },
		building: { badge: 'info', icon: 'text-info', label: 'text-foreground' },
		ready: { badge: 'success', icon: 'text-success', label: 'text-foreground' },
		failed: { badge: 'destructive', icon: 'text-destructive', label: 'text-destructive' }
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
	import { badgeVariants } from '#lib/components/ui/badge/index.js';
	import { cn } from '#lib/utils/style.js';
	import CheckCircleIcon from 'phosphor-svelte/lib/CheckCircle';
	import CircleDashedIcon from 'phosphor-svelte/lib/CircleDashed';
	import XCircleIcon from 'phosphor-svelte/lib/XCircle';
	import CircleNotchIcon from 'phosphor-svelte/lib/CircleNotch';

	let {
		status,
		appearance = 'pill'
	}: {
		status: string;
		// 'pill' is a tinted badge for headers, 'plain' is the tinted icon and a label for tables, like StatusBadge
		appearance?: 'pill' | 'plain';
	} = $props();

	const tone = $derived(tones[status] ?? tones.queued);
	const pill = $derived(appearance === 'pill');
	const iconClass = $derived(cn('shrink-0', !pill && ['size-4', tone.icon]));
</script>

<span
	data-slot="image-status-badge"
	data-status={status}
	class={pill
		? badgeVariants({ variant: tone.badge })
		: 'inline-flex w-fit shrink-0 items-center gap-1.5 whitespace-nowrap'}
>
	{#if status === 'building'}
		<CircleNotchIcon
			class={cn(iconClass, 'animate-spin motion-reduce:animate-none')}
			aria-hidden="true"
		/>
	{:else if status === 'ready'}
		<CheckCircleIcon class={iconClass} aria-hidden="true" />
	{:else if status === 'failed'}
		<XCircleIcon class={iconClass} aria-hidden="true" />
	{:else}
		<CircleDashedIcon class={iconClass} aria-hidden="true" />
	{/if}
	{#if pill}
		{labels[status] ?? status}
	{:else}
		<span class={tone.label}>{labels[status] ?? status}</span>
	{/if}
</span>
