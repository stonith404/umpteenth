<script lang="ts">
	import { collapseUnchanged, diffLines, diffStats } from '$lib/utils/diff-util';
	import { cn } from '$lib/utils/style';

	let {
		before,
		after,
		context = 3,
		showStats = true,
		class: className
	}: {
		before: string;
		after: string;
		// Unchanged lines shown around each change, the rest collapse into a gap marker
		context?: number;
		// Hidden when the surrounding UI already shows the added and removed counts
		showStats?: boolean;
		class?: string;
	} = $props();

	const lines = $derived(diffLines(before, after));
	const hunks = $derived(collapseUnchanged(lines, context));
	const stats = $derived(diffStats(lines));
</script>

<div
	data-slot="diff-view"
	class={cn('bg-muted/30 dark:bg-input/20 overflow-hidden rounded-lg border', className)}
>
	{#if showStats}
		<div class="text-muted-foreground flex items-center gap-3 border-b px-3 py-1.5 text-xs">
			<span class="numeric text-success-foreground">+{stats.added}</span>
			<span class="numeric text-destructive">−{stats.removed}</span>
		</div>
	{/if}
	<div class="max-h-128 overflow-auto">
		<table class="w-full border-collapse font-mono text-xs leading-5">
			<tbody>
				{#each hunks as line, i (i)}
					{#if line.type === 'gap'}
						<tr class="bg-muted/60 text-muted-foreground">
							<td colspan="3" class="px-3 py-0.5 text-center select-none">
								⋯ {line.count} unchanged {line.count === 1 ? 'line' : 'lines'}
							</td>
						</tr>
					{:else}
						<tr
							class={cn(
								line.type === 'added' && 'bg-success-tint',
								line.type === 'removed' && 'bg-destructive-tint'
							)}
						>
							<td class="text-muted-foreground w-10 px-2 text-right align-top select-none">
								{line.oldNumber ?? ''}
							</td>
							<td class="text-muted-foreground w-10 px-2 text-right align-top select-none">
								{line.newNumber ?? ''}
							</td>
							<!-- Long lines wrap at spaces where they can and only break inside a word that is wider than the column -->
							<!-- The marker hangs in the cell's left padding, so wrapped lines continue under the text rather than under the marker -->
							<td class="pr-2 pl-5 align-top break-words whitespace-pre-wrap wrap-anywhere">
								<span
									aria-hidden="true"
									class={cn(
										'-ml-3 inline-block w-3 select-none',
										line.type === 'added' && 'text-success-foreground',
										line.type === 'removed' && 'text-destructive'
									)}>{line.type === 'added' ? '+' : line.type === 'removed' ? '−' : ' '}</span
								><span class="sr-only"
									>{line.type === 'added'
										? 'Added: '
										: line.type === 'removed'
											? 'Removed: '
											: ''}</span
								>{line.text}
							</td>
						</tr>
					{/if}
				{/each}
			</tbody>
		</table>
		{#if lines.length === 0}
			<p class="text-muted-foreground px-3 py-4 text-center text-xs">Empty</p>
		{/if}
	</div>
</div>
