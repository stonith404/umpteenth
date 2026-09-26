<script lang="ts" module>
	import type { RunMode } from './run-meta';

	// Mode colors (PLAN §13): Explore violet, Assisted blue, Scripted emerald
	const modeClasses: Record<RunMode, string> = {
		explore: 'bg-violet-100/80 text-violet-800 dark:bg-violet-500/15 dark:text-violet-300',
		assisted: 'bg-blue-100/80 text-blue-800 dark:bg-blue-500/20 dark:text-blue-300',
		scripted: 'bg-emerald-100/80 text-emerald-800 dark:bg-emerald-500/15 dark:text-emerald-300'
	};
</script>

<script lang="ts">
	import { cn } from '$lib/utils/style';
	import { modeIcons, modeLabel } from './run-meta';

	type Props = {
		// One of explore|assisted|scripted, unknown values render neutral
		mode: RunMode | (string & {});
		// Marks a scripted run that fell back to the agent, shown as a trailing hint
		fellBack?: boolean;
		iconOnly?: boolean;
		class?: string;
	};

	let { mode, fellBack = false, iconOnly = false, class: className }: Props = $props();

	const Icon = $derived(modeIcons[mode as RunMode]);
	const label = $derived(modeLabel(mode));
</script>

<span
	data-slot="mode-badge"
	data-mode={mode}
	title={iconOnly ? label : fellBack ? 'The script failed and the agent took over' : undefined}
	class={cn(
		'inline-flex h-5 w-fit shrink-0 items-center gap-1 rounded-full text-xs font-medium whitespace-nowrap',
		iconOnly ? 'size-5 justify-center' : 'px-2',
		modeClasses[mode as RunMode] ?? 'bg-neutral-200/70 text-neutral-700',
		className
	)}
>
	{#if Icon}
		<Icon class="size-3 shrink-0" aria-hidden="true" />
	{/if}
	{#if iconOnly}
		<span class="sr-only">{label}</span>
	{:else}
		{label}
		{#if fellBack}
			<span class="opacity-70">· fell back</span>
		{/if}
	{/if}
</span>
