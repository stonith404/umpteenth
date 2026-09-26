<script lang="ts" module>
	type Token = { text: string; class?: string };

	// Matches JSON strings (keys when followed by a colon), literals and numbers in pretty-printed JSON
	const TOKEN_PATTERN =
		/("(?:\\u[a-fA-F0-9]{4}|\\[^u]|[^\\"])*")(\s*:)?|\b(true|false|null)\b|(-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?)/g;

	// Highlighting splits the text into many nodes, which is not worth it for very large documents
	const MAX_HIGHLIGHT_CHARS = 150_000;

	function tokenize(text: string): Token[] {
		if (text.length > MAX_HIGHLIGHT_CHARS) return [{ text }];

		const tokens: Token[] = [];
		let last = 0;
		for (const match of text.matchAll(TOKEN_PATTERN)) {
			const index = match.index ?? 0;
			if (index > last) tokens.push({ text: text.slice(last, index) });
			if (match[1]) {
				tokens.push({ text: match[1], class: match[2] ? 'json-key' : 'json-string' });
				if (match[2]) tokens.push({ text: match[2] });
			} else if (match[3]) {
				tokens.push({ text: match[3], class: 'json-literal' });
			} else {
				tokens.push({ text: match[0], class: 'json-number' });
			}
			last = index + match[0].length;
		}
		if (last < text.length) tokens.push({ text: text.slice(last) });
		return tokens;
	}
</script>

<script lang="ts">
	import { cn } from '$lib/utils/style';

	let {
		value,
		class: className
	}: {
		// Any JSON value, strings that contain JSON are shown as the string they are
		value: unknown;
		class?: string;
	} = $props();

	const text = $derived(JSON.stringify(value, null, 2) ?? 'undefined');
	const tokens = $derived(tokenize(text));
</script>

<pre
	data-slot="json-view"
	class={cn(
		'bg-muted/50 overflow-auto rounded-xl border px-3 py-2 font-mono text-xs leading-relaxed whitespace-pre-wrap break-words',
		className
	)}>{#each tokens as token, i (i)}{#if token.class}<span class={token.class}>{token.text}</span
			>{:else}{token.text}{/if}{/each}</pre>

<style>
	.json-key {
		color: oklch(0.5 0.15 280);
	}
	.json-string {
		color: oklch(0.52 0.14 150);
	}
	.json-number {
		color: oklch(0.55 0.16 50);
	}
	.json-literal {
		color: oklch(0.55 0.18 330);
	}

	:global(.dark) .json-key {
		color: oklch(0.78 0.11 280);
	}
	:global(.dark) .json-string {
		color: oklch(0.8 0.13 150);
	}
	:global(.dark) .json-number {
		color: oklch(0.8 0.13 60);
	}
	:global(.dark) .json-literal {
		color: oklch(0.78 0.14 330);
	}
</style>
