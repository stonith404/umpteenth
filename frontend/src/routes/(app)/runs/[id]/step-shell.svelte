<script lang="ts" module>
	export type StepTone =
		'default' | 'llm' | 'tool' | 'sandbox' | 'success' | 'danger' | 'live' | 'muted';

	const toneClasses: Record<StepTone, string> = {
		default: 'text-foreground',
		llm: 'text-fuchsia-600 dark:text-fuchsia-400 border-fuchsia-500/30',
		tool: 'text-sky-600 dark:text-sky-400 border-sky-500/30',
		sandbox: 'text-amber-600 dark:text-amber-400 border-amber-500/30',
		success: 'text-green-600 dark:text-green-400 border-green-500/30',
		danger: 'text-red-600 dark:text-red-400 border-red-500/40 bg-red-500/5',
		live: 'text-blue-600 dark:text-blue-400 border-blue-500/40',
		muted: 'text-muted-foreground'
	};
</script>

<script lang="ts">
	import { formatDateTime, formatDuration } from '$lib/utils/format-util';
	import { cn } from '$lib/utils/style';
	import type { Component, Snippet } from 'svelte';

	let {
		icon: Icon,
		title,
		tone = 'default',
		ts,
		startTs,
		compact = false,
		pulse = false,
		meta,
		children
	}: {
		icon: Component;
		title: string | Snippet;
		tone?: StepTone;
		// When the step happened, shown as an offset from the start of the run
		ts?: number;
		startTs: number;
		// Single-line steps such as status changes take less vertical room
		compact?: boolean;
		pulse?: boolean;
		meta?: Snippet;
		children?: Snippet;
	} = $props();
</script>

<li class={cn('relative flex gap-3', compact ? 'pb-3' : 'pb-5')} data-slot="timeline-step">
	<!-- The connector line runs from this step's icon to the next one -->
	<span
		class="bg-border absolute top-8 bottom-0 left-[15px] w-px [li:last-child>&]:hidden"
		aria-hidden="true"
	></span>
	<span
		class={cn(
			'bg-card relative z-[1] flex size-8 shrink-0 items-center justify-center rounded-full border',
			toneClasses[tone]
		)}
		aria-hidden="true"
	>
		<Icon class={cn('size-4', pulse && 'animate-pulse')} />
	</span>
	<!-- Paths, URLs and tokens in agent output often have no break opportunity, so they may break anywhere rather than widen the timeline -->
	<div class="flex min-w-0 flex-1 flex-col gap-2 pt-1.5 wrap-anywhere">
		<div class="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-sm">
			<span class="font-medium">
				{#if typeof title === 'string'}{title}{:else}{@render title()}{/if}
			</span>
			{#if meta}
				<span
					class="text-muted-foreground flex flex-wrap items-center gap-x-2 gap-y-1 text-xs [&>*+*]:before:mr-2 [&>*+*]:before:content-['·']"
				>
					{@render meta()}
				</span>
			{/if}
			{#if ts !== undefined}
				<span
					class="text-muted-foreground numeric ml-auto shrink-0 text-xs"
					title={formatDateTime(ts)}
				>
					+{formatDuration(Math.max(0, ts - startTs))}
				</span>
			{/if}
		</div>
		{#if children}
			{@render children()}
		{/if}
	</div>
</li>
