<script lang="ts">
	import * as Tooltip from '#lib/components/ui/tooltip/index.js';
	import { relativeTimeClock } from '#lib/utils/clock.svelte.js';
	import { formatDateTime, formatRelative } from '#lib/utils/format-util.js';
	import { cn } from '#lib/utils/style.js';

	let {
		value,
		interactive = true,
		side = 'top',
		class: className
	}: {
		// A unix millisecond timestamp
		value: number;
		// False renders a plain <time> with the exact time as its title, for rows that are already a link or a button, so the time adds no tab stop of its own
		interactive?: boolean;
		// Where the exact-time tooltip opens, e.g. 'bottom' in a header so it doesn't cover the title above
		side?: 'top' | 'right' | 'bottom' | 'left';
		class?: string;
	} = $props();

	const relative = $derived(formatRelative(value, relativeTimeClock.now));
	const exact = $derived(formatDateTime(value));
	const datetime = $derived(new Date(value).toISOString());
</script>

{#if interactive}
	<Tooltip.Root>
		<Tooltip.Trigger>
			{#snippet child({ props })}
				<time {...props} {datetime} class={cn('cursor-default', className)}>
					{relative}
				</time>
			{/snippet}
		</Tooltip.Trigger>
		<Tooltip.Content {side}>{exact}</Tooltip.Content>
	</Tooltip.Root>
{:else}
	<time {datetime} title={exact} class={className}>{relative}</time>
{/if}
