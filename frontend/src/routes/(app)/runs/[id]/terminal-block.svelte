<script lang="ts">
	import { cn } from '$lib/utils/style';

	let {
		content,
		command,
		live = false,
		error = false,
		label
	}: {
		content?: string;
		// Shown as the prompt line above the output, e.g. the bash command the agent ran
		command?: string;
		// Streams output, so the block follows new lines like a terminal unless the user scrolled up
		live?: boolean;
		error?: boolean;
		// Accessible name of the scrollable region
		label: string;
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

<div
	bind:this={scroller}
	onscroll={onScroll}
	role="region"
	aria-label={label}
	data-slot="terminal"
	class={cn(
		'max-h-80 overflow-auto rounded-lg border-terminal-border bg-terminal border px-3 py-2 font-mono text-xs leading-relaxed text-terminal-foreground',
		error && 'border-terminal-error-border'
	)}
>
	{#if command}
		<pre class="whitespace-pre-wrap break-words text-terminal-command"><span
				class="text-terminal-prompt select-none"
				>$ </span>{command}</pre>
	{/if}
	{#if content}
		<pre
			class={cn(
				'whitespace-pre-wrap break-words',
				command && 'mt-1',
				error && 'text-terminal-error'
			)}>{content}</pre>
	{/if}
	{#if live}
		<span
			class="inline-block h-3.5 w-1.5 bg-terminal-cursor animate-pulse align-middle"
			aria-hidden="true"
		></span>
	{/if}
</div>
