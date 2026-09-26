<script lang="ts">
	import { cn } from '$lib/utils/style';

	let {
		content,
		command,
		live = false,
		error = false,
		label,
		class: className
	}: {
		content?: string;
		// Shown as the prompt line above the output, e.g. the bash command the agent ran
		command?: string;
		// Streams output, so the block follows new lines like a terminal unless the user scrolled up
		live?: boolean;
		error?: boolean;
		// Accessible name of the scrollable region
		label?: string;
		class?: string;
	} = $props();

	let scroller: HTMLDivElement | undefined = $state();
	let follow = true;

	function onScroll() {
		if (!scroller) return;
		follow = scroller.scrollHeight - scroller.scrollTop - scroller.clientHeight < 24;
	}

	// New output keeps the block pinned to its end while following
	$effect(() => {
		void content;
		if (!live || !scroller || !follow) return;
		scroller.scrollTop = scroller.scrollHeight;
	});
</script>

<!-- The terminal keeps its dark look in both themes, so command output reads the same everywhere -->
<div
	bind:this={scroller}
	onscroll={onScroll}
	role="region"
	aria-label={label ?? 'Terminal output'}
	data-slot="terminal"
	class={cn(
		'max-h-80 overflow-auto rounded-xl border border-zinc-800 bg-zinc-950 px-3 py-2 font-mono text-xs leading-relaxed text-zinc-200',
		error && 'border-red-900/70',
		className
	)}
>
	{#if command}
		<pre class="whitespace-pre-wrap break-words text-zinc-100"><span
				class="text-emerald-400 select-none"
				>$ </span>{command}</pre>
	{/if}
	{#if content}
		<pre
			class={cn(
				'whitespace-pre-wrap break-words',
				command && 'mt-1',
				error && 'text-red-300'
			)}>{content}</pre>
	{/if}
	{#if live}
		<span class="inline-block h-3.5 w-1.5 animate-pulse bg-zinc-300 align-middle" aria-hidden="true"
		></span>
	{/if}
</div>
