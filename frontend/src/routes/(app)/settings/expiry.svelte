<!--
@component
When an API token or an invite stops working, shown alike on both settings tabs.
Relative while it is near ('in 3 days'), a short date beyond a week ('Oct 11'), and a neutral 'Expired' badge once it passed, each with the exact time in its tooltip.
-->
<script lang="ts">
	import RelativeTime from '#lib/components/relative-time.svelte';
	import { Badge } from '#lib/components/ui/badge/index.js';
	import * as Tooltip from '#lib/components/ui/tooltip/index.js';
	import { relativeTimeClock } from '#lib/utils/clock.svelte.js';
	import { formatDateTime } from '#lib/utils/format-util.js';

	let {
		value
	}: {
		// A unix millisecond timestamp, or null for something that never expires
		value: number | null;
	} = $props();
</script>

{#if value === null}
	<span class="text-muted-foreground">Never</span>
{:else if value <= relativeTimeClock.now}
	<!-- An expired token or invite is a state and not a failure, so the badge stays neutral -->
	<Tooltip.Root>
		<Tooltip.Trigger>
			{#snippet child({ props })}
				<Badge {...props} variant="secondary" class="cursor-default">Expired</Badge>
			{/snippet}
		</Tooltip.Trigger>
		<Tooltip.Content>{formatDateTime(value)}</Tooltip.Content>
	</Tooltip.Root>
{:else}
	<RelativeTime {value} />
{/if}
