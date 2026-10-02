<script lang="ts">
	import PageHeader from '#lib/components/page-header.svelte';
	import RunsTable from '#lib/components/runs/runs-table.svelte';
</script>

<svelte:head>
	<title>Runs · Umpteenth</title>
</svelte:head>

<PageHeader title="Runs" description="Every execution of every job, with its full timeline." />

<RunsTable />
