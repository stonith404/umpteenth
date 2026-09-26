<script lang="ts">
	import { navigating } from '$app/state';
	import { untrack } from 'svelte';

	// Fast navigations finish before this, so they never flash a bar
	const SHOW_DELAY_MS = 150;
	const TRICKLE_INTERVAL_MS = 250;
	// How long the completed bar stays before it is removed, which covers its fill and fade
	const FINISH_MS = 350;

	// The bar creeps towards 90% while the next page loads and fills up once it is there, since the real progress of a load is unknown
	let progress = $state(0);
	let visible = $state(false);

	// Only the navigation state drives this, the bar's own state is read untracked so updating it doesn't restart the timers
	$effect(() => {
		const pending = navigating.to !== null;
		return untrack(() => (pending ? start() : finish()));
	});

	function start() {
		// A navigation that starts while the previous bar fades out begins a fresh bar
		visible = false;
		progress = 0;

		let trickle: ReturnType<typeof setInterval> | undefined;
		const delay = setTimeout(() => {
			progress = 0.15;
			visible = true;
			trickle = setInterval(() => (progress += (0.9 - progress) * 0.12), TRICKLE_INTERVAL_MS);
		}, SHOW_DELAY_MS);
		return () => {
			clearTimeout(delay);
			clearInterval(trickle);
		};
	}

	function finish() {
		if (!visible) return;
		progress = 1;
		const timer = setTimeout(() => {
			visible = false;
			progress = 0;
		}, FINISH_MS);
		return () => clearTimeout(timer);
	}
</script>

<!-- Sits on the bottom edge of the positioned element it is placed in, e.g. the app header's border -->
{#if visible}
	<div
		role="progressbar"
		aria-label="Loading page"
		class="pointer-events-none absolute inset-x-0 -bottom-px h-0.5 overflow-hidden"
	>
		<!-- Once complete, the bar fills up first and fades after -->
		<div
			class="bg-primary h-full origin-left scale-x-(--progress) [transition:scale_200ms_ease-out,opacity_150ms_ease-out_150ms] motion-reduce:[transition:none]"
			class:opacity-0={progress === 1}
			style:--progress={progress}
		></div>
	</div>
{/if}
