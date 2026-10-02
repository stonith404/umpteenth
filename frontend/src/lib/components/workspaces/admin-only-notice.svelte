<script lang="ts">
	import * as Alert from '#lib/components/ui/alert/index.js';
	import LockIcon from '@lucide/svelte/icons/lock';

	// Explains why a page a member can read has nothing to click
	let { children }: { children: import('svelte').Snippet } = $props();
</script>

<Alert.Root>
	<LockIcon />
	<Alert.Description>{@render children()}</Alert.Description>
</Alert.Root>
