<script lang="ts">
	import { authorLabel } from '#lib/utils/playbook-util.js';
	import HammerIcon from 'phosphor-svelte/lib/Hammer';
	import PencilSimpleIcon from 'phosphor-svelte/lib/PencilSimple';
	import ArrowCounterClockwiseIcon from 'phosphor-svelte/lib/ArrowCounterClockwise';
	import SparkleIcon from 'phosphor-svelte/lib/Sparkle';
	import type { Component } from 'svelte';

	let { author }: { author: string } = $props();

	const authorIcons: Record<string, Component> = {
		reflection: SparkleIcon,
		user: PencilSimpleIcon,
		rollback: ArrowCounterClockwiseIcon,
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
