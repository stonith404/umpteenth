<script lang="ts">
	import { collapseUnchanged, diffLines, diffStats } from '$lib/utils/diff-util';
	import { cn } from '$lib/utils/style';

	let {
		before,
		after,
		context = 3,
		class: className
	}: {
		before: string;
		after: string;
		// Unchanged lines shown around each change, the rest collapse into a gap marker
		context?: number;
		class?: string;
	} = $props();

	const lines = $derived(diffLines(before, after));
	const hunks = $derived(collapseUnchanged(lines, context));
	const stats = $derived(diffStats(lines));
</script>

<div
	data-slot="diff-view"
	class={cn('bg-muted/30 dark:bg-input/20 overflow-hidden rounded-xl border', className)}
>
	<div class="text-muted-foreground flex items-center gap-3 border-b px-3 py-1.5 text-xs">
		<span class="numeric text-green-700 dark:text-green-400">+{stats.added}</span>
		<span class="numeric text-red-700 dark:text-red-400">−{stats.removed}</span>
	</div>
	<div class="max-h-[32rem] overflow-auto">
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
								line.type === 'added' && 'bg-green-500/10 dark:bg-green-500/15',
								line.type === 'removed' && 'bg-red-500/10 dark:bg-red-500/15'
							)}
						>
							<td class="text-muted-foreground w-10 px-2 text-right align-top select-none">
								{line.oldNumber ?? ''}
							</td>
							<td class="text-muted-foreground w-10 px-2 text-right align-top select-none">
								{line.newNumber ?? ''}
							</td>
							<td class="px-2 align-top whitespace-pre-wrap break-all">
								<span
									aria-hidden="true"
									class={cn(
										'inline-block w-3 select-none',
										line.type === 'added' && 'text-green-700 dark:text-green-400',
										line.type === 'removed' && 'text-red-700 dark:text-red-400'
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
