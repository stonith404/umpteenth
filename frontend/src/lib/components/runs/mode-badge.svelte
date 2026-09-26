<script lang="ts" module>
	import type { RunMode } from './run-meta';

	// Modes are categories, not outcomes, so only the icon carries the mode's hue from app.css and the badge itself stays neutral
	export const modeIconClasses: Record<RunMode, string> = {
		explore: 'text-mode-explore',
		assisted: 'text-mode-assisted',
		scripted: 'text-mode-scripted'
	};

	// Fills of the same hues, for chart marks and legend swatches that should match the badges
	export const modeFillClasses: Record<RunMode, string> = {
		explore: 'bg-mode-explore-fill',
		assisted: 'bg-mode-assisted-fill',
		scripted: 'bg-mode-scripted-fill'
	};
</script>

<script lang="ts">
	import { cn } from '$lib/utils/style';
	import { modeIcons, modeLabel } from './run-meta';

	type Props = {
		// One of explore|assisted|scripted, unknown values render without an icon
		mode: RunMode | (string & {});
		// Marks a scripted run that fell back to the agent, shown as a trailing hint
		fellBack?: boolean;
		class?: string;
	};

	let { mode, fellBack = false, class: className }: Props = $props();

	const Icon = $derived(modeIcons[mode as RunMode]);
	const label = $derived(modeLabel(mode));
</script>

<span
	data-slot="mode-badge"
	data-mode={mode}
	title={fellBack ? 'The script failed and the agent took over' : undefined}
	class={cn(
		'text-muted-foreground inline-flex w-fit shrink-0 items-center gap-1.5 whitespace-nowrap',
		className
	)}
>
	{#if Icon}
		<Icon class={cn('size-4 shrink-0', modeIconClasses[mode as RunMode])} aria-hidden="true" />
	{/if}
	{label}
	{#if fellBack}
		<span class="opacity-70">· fell back</span>
	{/if}
</span>
