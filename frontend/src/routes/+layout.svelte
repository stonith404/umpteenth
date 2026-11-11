<script lang="ts">
	import { ConfirmDialog } from '#lib/components/confirm-dialog/index.js';
	import { Toaster } from '#lib/components/ui/sonner/index.js';
	import { ModeWatcher } from 'mode-watcher';
	import IconContext from 'phosphor-svelte/lib/IconContext';
	import type { Snippet } from 'svelte';
	import '../app.css';

	let { children }: { children: Snippet } = $props();
</script>

<!-- Phosphor marks every icon as an image, but nearly all of ours sit beside a label, so they are hidden unless one opts back in with aria-hidden="false" and its own aria-label -->
<IconContext values={{ size: 16, 'aria-hidden': 'true' }}>
	{@render children()}

	<Toaster />
	<ConfirmDialog />
</IconContext>
<ModeWatcher disableTransitions={false} />
