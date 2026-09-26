<script lang="ts">
	import * as Tooltip from '$lib/components/ui/tooltip';
	import { relativeTimeClock } from '$lib/utils/clock.svelte';
	import { formatDateTime, formatRelative } from '$lib/utils/format-util';

	// A unix millisecond timestamp
	let { value }: { value: number } = $props();

	const relative = $derived(formatRelative(value, relativeTimeClock.now));
	const exact = $derived(formatDateTime(value));
</script>

<Tooltip.Root>
	<Tooltip.Trigger>
		{#snippet child({ props })}
			<time {...props} datetime={new Date(value).toISOString()} class="cursor-default">
				{relative}
			</time>
		{/snippet}
	</Tooltip.Trigger>
	<Tooltip.Content>{exact}</Tooltip.Content>
</Tooltip.Root>
