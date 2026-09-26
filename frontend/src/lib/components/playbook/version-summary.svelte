<script lang="ts">
	import LightbulbIcon from '@lucide/svelte/icons/lightbulb';

	let { author, summary }: { author: string; summary: string } = $props();
</script>

<!-- Set apart from the changes below it, since it is the one sentence that says why the version exists -->
<div class="bg-muted flex gap-3 rounded-lg px-4 py-3">
	<LightbulbIcon class="text-muted-foreground mt-0.5 size-4 shrink-0" aria-hidden="true" />
	<div class="flex min-w-0 flex-col gap-0.5">
		<p class="text-muted-foreground text-xs font-medium">
			{author === 'reflection' ? 'What reflection learned from the run' : 'Summary'}
		</p>
		<p class="text-sm break-words">{summary}</p>
	</div>
</div>
