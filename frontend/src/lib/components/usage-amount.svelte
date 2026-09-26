<!--
	A usage figure in the workspace's unit, a price like `$0.14` or a token count like `37.7k tokens`, never both
	In tokens mode a breakdown adds a tooltip with the input, output and cache tokens, while a price has no tooltip, since tokens would be the other unit
-->
<script lang="ts" module>
	// The token counts behind a usage figure, which tokens mode lists in a tooltip
	export type TokenBreakdown = {
		input: number;
		output: number;
		cacheRead?: number;
		cacheWrite?: number;
	};
</script>

<script lang="ts">
	import * as Tooltip from '$lib/components/ui/tooltip';
	import { formatTokens } from '$lib/utils/format-util';
	import { cn } from '$lib/utils/style';
	import { usageFormat, type Usage } from '$lib/utils/usage-util';

	let {
		usage,
		bare = false,
		breakdown,
		tabindex,
		class: className
	}: {
		usage: Usage;
		// Leaves out the word tokens under a label that already names the unit, such as a 'Tokens' column
		bare?: boolean;
		breakdown?: TokenBreakdown;
		// -1 keeps the tooltip's trigger out of the tab order, e.g. in a table whose rows are links already
		tabindex?: number;
		class?: string;
	} = $props();

	const format = $derived(usageFormat());
	const value = $derived(format.pick(usage));
	const text = $derived(bare ? format.formatBare(value) : format.format(value));
</script>

{#if format.unit === 'tokens' && breakdown}
	<Tooltip.Root>
		<Tooltip.Trigger {tabindex}>
			{#snippet child({ props })}
				<span {...props} class={cn('numeric cursor-default', className)}>{text}</span>
			{/snippet}
		</Tooltip.Trigger>
		<Tooltip.Content>
			<div class="numeric grid grid-cols-[auto_auto] gap-x-3 gap-y-0.5">
				<span class="text-muted-foreground">In</span>
				<span class="text-right">{formatTokens(breakdown.input)}</span>
				<span class="text-muted-foreground">Out</span>
				<span class="text-right">{formatTokens(breakdown.output)}</span>
				{#if breakdown.cacheRead}
					<span class="text-muted-foreground">Cache read</span>
					<span class="text-right">{formatTokens(breakdown.cacheRead)}</span>
				{/if}
				{#if breakdown.cacheWrite}
					<span class="text-muted-foreground">Cache write</span>
					<span class="text-right">{formatTokens(breakdown.cacheWrite)}</span>
				{/if}
			</div>
		</Tooltip.Content>
	</Tooltip.Root>
{:else}
	<span class={cn('numeric', className)}>{text}</span>
{/if}
