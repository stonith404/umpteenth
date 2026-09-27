<script lang="ts">
	import { ConfirmDialog } from '$lib/components/confirm-dialog';
	import { Toaster } from '$lib/components/ui/sonner';
	import { ModeWatcher } from 'mode-watcher';
	import type { Snippet } from 'svelte';
	import '../app.css';

	let { children }: { children: Snippet } = $props();
</script>

{@render children()}

<Toaster />
<ConfirmDialog />
<ModeWatcher disableTransitions={false} />
