<script lang="ts">
	import { renderMarkdown } from '#lib/utils/markdown-util.js';

	let { source }: { source: string } = $props();

	// The HTML is sanitized by DOMPurify, so rendering it with {@html} is safe
	const html = $derived(renderMarkdown(source));
</script>

<div data-slot="markdown" class="markdown text-sm leading-relaxed wrap-anywhere">
	<!-- eslint-disable-next-line svelte/no-at-html-tags -->
	{@html html}
</div>

<style>
	.markdown :global(> * + *) {
		margin-top: 0.75em;
	}
	.markdown :global(h1),
	.markdown :global(h2),
	.markdown :global(h3),
	.markdown :global(h4) {
		font-weight: 600;
		line-height: 1.3;
		margin-top: 1.25em;
	}
	.markdown :global(h1) {
		font-size: 1.25em;
	}
	.markdown :global(h2) {
		font-size: 1.125em;
	}
	.markdown :global(h3),
	.markdown :global(h4) {
		font-size: 1em;
	}
	.markdown :global(> :first-child) {
		margin-top: 0;
	}
	.markdown :global(ul) {
		list-style: disc;
		padding-left: 1.25em;
	}
	.markdown :global(ol) {
		list-style: decimal;
		padding-left: 1.25em;
	}
	.markdown :global(li + li) {
		margin-top: 0.25em;
	}
	.markdown :global(a) {
		text-decoration: underline;
		text-underline-offset: 3px;
	}
	.markdown :global(code) {
		font-family: var(--font-mono);
		font-size: 0.875em;
		background: var(--muted);
		border-radius: 0.375rem;
		padding: 0.1em 0.35em;
	}
	.markdown :global(pre) {
		font-family: var(--font-mono);
		font-size: 0.8125rem;
		background: var(--muted);
		border-radius: 0.75rem;
		padding: 0.75rem 1rem;
		overflow-x: auto;
	}
	.markdown :global(pre code) {
		background: transparent;
		padding: 0;
	}
	.markdown :global(blockquote) {
		border-left: 3px solid var(--border);
		padding-left: 0.75em;
		color: var(--muted-foreground);
	}
	.markdown :global(table) {
		width: 100%;
		border-collapse: collapse;
		font-size: 0.875em;
	}
	.markdown :global(th),
	.markdown :global(td) {
		border: 1px solid var(--border);
		padding: 0.35em 0.6em;
		text-align: left;
	}
	.markdown :global(hr) {
		border-color: var(--border);
	}
</style>
