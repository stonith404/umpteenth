<!--
	The description of an error toast: why the action in its title failed, and the request ID operators match against the server log
-->
<script lang="ts">
	import RequestId from '#lib/components/request-id.svelte';

	let { reason, requestId }: { reason?: string; requestId?: string } = $props();
</script>

{#if reason}
	<p>{reason}</p>
{/if}
{#if requestId}
	<RequestId id={requestId} class={reason ? 'mt-1' : undefined} />
{/if}
