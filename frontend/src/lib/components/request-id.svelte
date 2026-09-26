<!--
	The ID of a failed request, which operators match against the server log
	Toasts and error pages show it the same way: small, muted and in the code font, with a click selecting the whole ID for copying
-->
<script lang="ts">
	import { cn } from '$lib/utils/style';

	let { id, class: className }: { id: string; class?: string } = $props();
</script>

<p class={cn('text-muted-foreground font-mono text-xs leading-4', className)}>
	Request ID: <span class="select-all">{id}</span>
</p>
