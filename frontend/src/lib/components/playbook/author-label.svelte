<script lang="ts">
	import { authorLabel } from '#lib/utils/playbook-util.js';
	import HammerIcon from '@lucide/svelte/icons/hammer';
	import PencilIcon from '@lucide/svelte/icons/pencil';
	import RotateCcwIcon from '@lucide/svelte/icons/rotate-ccw';
	import SparklesIcon from '@lucide/svelte/icons/sparkles';
	import type { Component } from 'svelte';

	let { author }: { author: string } = $props();

	const authorIcons: Record<string, Component> = {
		reflection: SparklesIcon,
		user: PencilIcon,
		rollback: RotateCcwIcon,
		compile: HammerIcon
	};

	const Icon = $derived(authorIcons[author]);
</script>

<span class="inline-flex items-center gap-1.5">
	{#if Icon}
		<Icon class="size-3.5 shrink-0" aria-hidden="true" />
	{/if}
	{authorLabel(author)}
</span>
