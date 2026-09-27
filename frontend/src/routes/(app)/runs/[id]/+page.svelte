<script lang="ts">
	import type { PageData } from './$types';
	import RunView from './run-view.svelte';

	let { data }: { data: PageData } = $props();
</script>

<!-- Navigating to another run (e.g. after a retry) starts a fresh view with its own stream -->
{#key data.run.id}
	<RunView initialRun={data.run} />
{/key}
