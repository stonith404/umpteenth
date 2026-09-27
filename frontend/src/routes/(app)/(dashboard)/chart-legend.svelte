<script lang="ts">
	let {
		series
	}: {
		series: { key: string; label: string; color: string }[];
	} = $props();
</script>

<!--
	Kumo's small legend items: a round dot and the series name, in centred rows under the plot
	Names wrap onto another row rather than truncate, because a legend of cut-off job names can't be told apart
	Only a name longer than an item's cap truncates, with the full name in its title
	It always takes the height of two rows, so a one-row legend leaves its plot as tall as a two-row one beside it
-->
<ul
	class="text-muted-foreground mt-3 flex min-h-9 shrink-0 flex-wrap content-start justify-center gap-x-4 gap-y-1 text-xs"
	aria-label="Legend"
>
	{#each series as s (s.key)}
		<li class="flex max-w-50 min-w-0 items-center gap-2" title={s.label}>
			<span class="size-2 shrink-0 rounded-full bg-(--series-color)" style:--series-color={s.color}
			></span>
			<span class="truncate">{s.label}</span>
		</li>
	{/each}
</ul>
