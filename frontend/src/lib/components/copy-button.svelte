<script lang="ts">
	import * as InputGroup from '#lib/components/ui/input-group/index.js';
	import CheckIcon from '@lucide/svelte/icons/check';
	import CopyIcon from '@lucide/svelte/icons/copy';
	import { toast } from 'svelte-sonner';

	let { value, label = 'Copy' }: { value: string; label?: string } = $props();

	const RESET_MS = 2000;

	let copied = $state(false);
	let resetTimeout: ReturnType<typeof setTimeout> | undefined;

	async function copy() {
		// The clipboard API only exists in secure contexts, so plain-HTTP deployments get a hint instead of a silent failure
		try {
			await navigator.clipboard.writeText(value);
		} catch {
			toast.error('Failed to copy', { description: 'Select the text and copy it manually.' });
			return;
		}

		copied = true;
		clearTimeout(resetTimeout);
		resetTimeout = setTimeout(() => (copied = false), RESET_MS);
	}
</script>

<InputGroup.Button size="icon-xs" aria-label={copied ? 'Copied' : label} onclick={copy}>
	{#if copied}
		<CheckIcon />
	{:else}
		<CopyIcon />
	{/if}
</InputGroup.Button>
