<script lang="ts">
	import RunsTable from '#lib/components/runs/runs-table.svelte';

	let { data } = $props();
</script>

<svelte:head>
	<title>Runs · {data.job.name} · Umpteenth</title>
</svelte:head>

<RunsTable jobId={data.job.id} label="Runs of {data.job.name}" />
