<script lang="ts" module>
	import { mark } from '$lib/brand';

	// Splits a path of the mark into its eight rows of cells, so each row can slip sideways on its own
	function byRow(d: string) {
		const rows: string[] = Array.from({ length: 8 }, () => '');
		for (const cell of d.match(/M[^M]+/g) ?? []) {
			const y = Number(cell.slice(1).split(/[ h]/)[1]);
			rows[(y - 16) / 4] += cell;
		}
		return rows;
	}

	const bars = byRow(mark.bar);
	const rows = byRow(mark.dither).map((dither, i) => ({ dither, bar: bars[i] }));
</script>

<script lang="ts">
	import { cn } from '$lib/utils/style';

	let { class: className }: { class?: string } = $props();
</script>

<!--
@component
The Signal mark tearing like a corrupted frame, shown on the error pages in place of an icon.
It holds still, then its rows slip sideways in two short bursts while stray lime slices flash, and it stays still under reduced motion.
Like the logo it is pixel art on a 16 cell grid, so size it in multiples of 16 px.
The rows move on their own layers, so crisp edges keep hairline seams from showing between them.
-->
<svg
	viewBox={mark.viewBox}
	xmlns="http://www.w3.org/2000/svg"
	class={cn('size-16', className)}
	shape-rendering="crispEdges"
	aria-hidden="true"
>
	{#each rows as row, i (i)}
		<g class="row row-{i}">
			<path d={row.dither} fill="currentColor" />
			<path d={row.bar} class="fill-brand" />
		</g>
	{/each}
	<path class="slice slice-0 fill-brand" d="M16 20h12v4h-12z" />
	<path class="slice slice-1 fill-brand" d="M16 24h20v4h-20z" />
	<path class="slice slice-2 fill-brand" d="M24 32h20v4h-20z" />
	<path class="slice slice-3 fill-brand" d="M8 40h28v4h-28z" />
	<path class="slice slice-4 fill-brand" d="M24 44h28v4h-28z" />
</svg>

<style>
	/* Every change lands on a 70 ms frame and holds until the next one, so the tear reads as dropped frames rather than motion */
	.row,
	.slice {
		animation-duration: 3.6s;
		animation-iteration-count: infinite;
		animation-timing-function: steps(1, end);
	}

	.slice {
		opacity: 0;
	}

	.row-1 {
		animation-name: tear-row-1;
	}
	.row-2 {
		animation-name: tear-row-2;
	}
	.row-3 {
		animation-name: tear-row-3;
	}
	.row-4 {
		animation-name: tear-row-4;
	}
	.row-5 {
		animation-name: tear-row-5;
	}
	.row-7 {
		animation-name: tear-row-7;
	}
	.slice-0 {
		animation-name: tear-slice-0;
	}
	.slice-1 {
		animation-name: tear-slice-1;
	}
	.slice-2 {
		animation-name: tear-slice-2;
	}
	.slice-3 {
		animation-name: tear-slice-3;
	}
	.slice-4 {
		animation-name: tear-slice-4;
	}

	/* The hard burst runs from 31.94% to 41.67% and the aftershock from 65.28% to 69.17%, and rows 0 and 6 never move so the mark stays recognisable */
	@keyframes tear-row-1 {
		33.89% {
			transform: translateX(-4px);
			opacity: 0;
		}
		35.83% {
			opacity: 1;
		}
		41.67% {
			transform: none;
		}
		65.28% {
			transform: translateX(-8px);
		}
		69.17% {
			transform: none;
		}
	}
	@keyframes tear-row-2 {
		31.94% {
			transform: translateX(-4px);
			opacity: 0;
		}
		33.89% {
			transform: translateX(12px);
			opacity: 1;
		}
		37.78% {
			transform: translateX(-4px);
		}
		39.72% {
			transform: translateX(12px);
		}
		41.67% {
			transform: none;
		}
	}
	@keyframes tear-row-3 {
		33.89% {
			transform: translateX(8px);
		}
		35.83% {
			transform: translateX(-4px);
		}
		41.67% {
			transform: none;
		}
		65.28% {
			transform: translateX(-8px);
		}
		67.22% {
			transform: translateX(-4px);
		}
		69.17% {
			transform: none;
		}
	}
	@keyframes tear-row-4 {
		31.94% {
			transform: translateX(-8px);
		}
		41.67% {
			transform: none;
		}
		67.22% {
			transform: translateX(-4px);
			opacity: 0;
		}
		69.17% {
			transform: none;
			opacity: 1;
		}
	}
	@keyframes tear-row-5 {
		31.94% {
			transform: translateX(-4px);
		}
		37.78% {
			transform: translateX(8px);
		}
		39.72% {
			transform: translateX(12px);
		}
		41.67% {
			transform: none;
		}
		65.28% {
			transform: translateX(8px);
		}
		67.22% {
			transform: translateX(-8px);
		}
		69.17% {
			transform: none;
		}
	}
	@keyframes tear-row-7 {
		31.94% {
			transform: translateX(8px);
		}
		33.89% {
			transform: translateX(-8px);
		}
		41.67% {
			transform: none;
		}
	}

	/* Each slice shows for a single frame, like data landing in the wrong row */
	@keyframes tear-slice-0 {
		31.94% {
			opacity: 0.9;
		}
		33.89% {
			opacity: 0;
		}
	}
	@keyframes tear-slice-1 {
		37.78% {
			opacity: 0.9;
		}
		39.72% {
			opacity: 0;
		}
	}
	@keyframes tear-slice-2 {
		39.72% {
			opacity: 0.9;
		}
		41.67% {
			opacity: 0;
		}
	}
	@keyframes tear-slice-3 {
		65.28% {
			opacity: 0.9;
		}
		67.22% {
			opacity: 0;
		}
	}
	@keyframes tear-slice-4 {
		67.22% {
			opacity: 0.9;
		}
		69.17% {
			opacity: 0;
		}
	}

	@media (prefers-reduced-motion: reduce) {
		.row,
		.slice {
			animation: none;
		}
	}
</style>
