<!--
	Cloudflare's table of contents beside a long settings page: the section headings in a column with a line on its left, where a short bar marks the section in view
	Usage: <TableOfContents items={[{ label: 'General' }, { label: 'Danger zone', danger: true }]} />
-->
<script lang="ts" module>
	// The anchor of a section named `label`, e.g. 'Spend and retention' → 'spend-and-retention'
	export function anchorId(label: string) {
		return label
			.toLowerCase()
			.replace(/[^a-z0-9]+/g, '-')
			.replace(/^-|-$/g, '');
	}
</script>

<script lang="ts">
	import { cn } from '#lib/utils/style.js';
	import { onMount } from 'svelte';

	let { items }: { items: { label: string; danger?: boolean }[] } = $props();

	let current = $state<string>();

	// The section in view is the last one whose top has passed the upper third of the window
	onMount(() => {
		const update = () => {
			let found = items[0] && anchorId(items[0].label);
			for (const item of items) {
				const el = document.getElementById(anchorId(item.label));
				if (el && el.getBoundingClientRect().top < window.innerHeight / 3)
					found = anchorId(item.label);
			}
			current = found;
		};
		update();
		window.addEventListener('scroll', update, { passive: true });
		return () => window.removeEventListener('scroll', update);
	});
</script>

<nav aria-label="On this page" class="border-l">
	<ul class="sticky top-14.5 flex flex-col py-2">
		{#each items as item (item.label)}
			{@const id = anchorId(item.label)}
			<li>
				<a
					href="#{id}"
					aria-current={current === id ? 'location' : undefined}
					class={cn(
						'relative block py-1.5 pl-7 text-base transition-colors',
						item.danger
							? 'text-destructive'
							: current === id
								? 'text-foreground'
								: 'text-muted-foreground hover:text-foreground',
						current === id &&
							'before:bg-foreground before:absolute before:inset-y-1 before:-left-px before:w-0.5 before:rounded-full'
					)}
				>
					{item.label}
				</a>
			</li>
		{/each}
	</ul>
</nav>
