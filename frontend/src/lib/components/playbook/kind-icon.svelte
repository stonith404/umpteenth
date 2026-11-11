<script lang="ts">
	import type { PlaybookChangeKind } from '#lib/utils/playbook-util.js';
	import BookOpenIcon from 'phosphor-svelte/lib/BookOpen';
	import CubeIcon from 'phosphor-svelte/lib/Cube';
	import FileCodeIcon from 'phosphor-svelte/lib/FileCode';
	import LightbulbIcon from 'phosphor-svelte/lib/Lightbulb';
	import ListChecksIcon from 'phosphor-svelte/lib/ListChecks';
	import TerminalWindowIcon from 'phosphor-svelte/lib/TerminalWindow';
	import WrenchIcon from 'phosphor-svelte/lib/Wrench';
	import type { Component } from 'svelte';

	let { kind }: { kind: PlaybookChangeKind } = $props();

	const kindIcons: Record<PlaybookChangeKind, Component> = {
		learning: BookOpenIcon,
		script: FileCodeIcon,
		dockerfile: CubeIcon,
		setup: WrenchIcon,
		main: TerminalWindowIcon,
		verify: ListChecksIcon,
		suggestion: LightbulbIcon
	};

	const Icon = $derived(kindIcons[kind] ?? FileCodeIcon);
</script>

<!-- The same icon marks a part of the playbook on the playbook page and in a version's changes -->
<span
	class="bg-muted text-muted-foreground grid size-6 shrink-0 place-items-center rounded-md"
	aria-hidden="true"
>
	<Icon class="size-3.5" />
</span>
