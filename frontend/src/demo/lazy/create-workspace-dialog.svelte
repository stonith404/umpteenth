<!--
@component
The app's dialog for a new workspace, loaded once it first opens, since the demo never offers more than one workspace.
The demo build points `#lib/components/workspaces/create-workspace-dialog.svelte` here, see vite.demo.config.ts.
-->
<script lang="ts">
	let { open = $bindable(false) }: { open?: boolean } = $props();

	const dialog = $derived(
		open ? import('../../lib/components/workspaces/create-workspace-dialog.svelte') : null
	);
</script>

{#if dialog}
	{#await dialog then { default: CreateWorkspaceDialog }}
		<CreateWorkspaceDialog bind:open />
	{/await}
{/if}
