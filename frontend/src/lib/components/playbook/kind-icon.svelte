<script lang="ts">
	import type { PlaybookChangeKind } from '$lib/utils/playbook-util';
	import BookOpenIcon from '@lucide/svelte/icons/book-open';
	import ContainerIcon from '@lucide/svelte/icons/container';
	import FileCodeIcon from '@lucide/svelte/icons/file-code';
	import ListChecksIcon from '@lucide/svelte/icons/list-checks';
	import SquareTerminalIcon from '@lucide/svelte/icons/square-terminal';
	import WrenchIcon from '@lucide/svelte/icons/wrench';
	import type { Component } from 'svelte';

	let { kind }: { kind: PlaybookChangeKind } = $props();

	const kindIcons: Record<PlaybookChangeKind, Component> = {
		learning: BookOpenIcon,
		script: FileCodeIcon,
		dockerfile: ContainerIcon,
		setup: WrenchIcon,
		main: SquareTerminalIcon,
		verify: ListChecksIcon
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
