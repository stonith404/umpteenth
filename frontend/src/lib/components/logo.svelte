<script lang="ts">
	import { mark } from '#lib/brand.js';
	import { cn } from '#lib/utils/style.js';

	let { class: className }: { class?: string } = $props();
</script>

<!-- The mark is pixel art on a 16 cell grid, so size it in multiples of 16 px to keep every cell on whole pixels -->
<svg
	viewBox={mark.viewBox}
	xmlns="http://www.w3.org/2000/svg"
	class={cn('size-8', className)}
	aria-hidden="true"
>
	<path d={mark.dither} fill="currentColor" />
	<path d={mark.bar} class="fill-brand" />
</svg>
