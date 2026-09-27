<script lang="ts">
	import { wordmark } from '$lib/brand';
	import { cn } from '$lib/utils/style';

	let { class: className }: { class?: string } = $props();
</script>

<!-- Umpteen with a raised th, and the th only turns lime on dark surfaces because lime fades on white -->
<svg
	viewBox={wordmark.viewBox}
	xmlns="http://www.w3.org/2000/svg"
	class={cn('h-4 w-auto', className)}
	role="img"
	aria-label="Umpteenth"
>
	<path d={wordmark.stem} fill="currentColor" />
	<path d={wordmark.ordinal} class="fill-current dark:fill-brand" />
</svg>
