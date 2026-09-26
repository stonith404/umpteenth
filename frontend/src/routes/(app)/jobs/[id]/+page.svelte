<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import Markdown from '$lib/components/markdown.svelte';
	import { Badge } from '$lib/components/ui/badge';
	import { Button } from '$lib/components/ui/button';
	import * as Card from '$lib/components/ui/card';
	import * as Empty from '$lib/components/ui/empty';
	import * as Tabs from '$lib/components/ui/tabs';
	import { formatDuration, formatMicroCost } from '$lib/utils/format-util';
	import ChartColumnIcon from '@lucide/svelte/icons/chart-column';
	import PencilIcon from '@lucide/svelte/icons/pencil';
	import GraduationChart from './graduation-chart.svelte';

	let { data } = $props();

	const job = $derived(data.job);
	const stats = $derived(data.stats);
	const runs = $derived(stats.runs ?? []);
	const spec = $derived(job.spec);

	const ranges = [
		{ value: '7d', label: '7 days' },
		{ value: '30d', label: '30 days' },
		{ value: '90d', label: '90 days' }
	];

	function setRange(range: string) {
		const url = new URL(page.url);
		if (range === '30d') url.searchParams.delete('range');
		else url.searchParams.set('range', range);
		void goto(url, { noScroll: true, keepFocus: true, replaceState: true });
	}
</script>

<svelte:head>
	<title>{job.name} · Umpteenth</title>
</svelte:head>

<div class="flex flex-wrap items-center justify-between gap-3">
	<h2 class="text-lg font-semibold">Performance</h2>
	<Tabs.Root value={data.range} onValueChange={setRange}>
		<Tabs.List aria-label="Period">
			{#each ranges as range (range.value)}
				<Tabs.Trigger value={range.value}>{range.label}</Tabs.Trigger>
			{/each}
		</Tabs.List>
	</Tabs.Root>
</div>

<div class="grid grid-cols-2 gap-4 md:grid-cols-4">
	{#each [{ label: 'Runs', value: String(runs.length) }, { label: 'Success rate', value: runs.length > 0 ? `${Math.round(stats.successRate * 100)}%` : '—' }, { label: 'Average cost', value: runs.length > 0 ? formatMicroCost(stats.avgCost) : '—' }, { label: 'Median duration', value: stats.p50Ms > 0 ? formatDuration(stats.p50Ms) : '—' }] as tile (tile.label)}
		<Card.Root size="sm" class="gap-1">
			<Card.Header>
				<Card.Description>{tile.label}</Card.Description>
			</Card.Header>
			<Card.Content>
				<p class="text-2xl font-semibold">{tile.value}</p>
			</Card.Content>
		</Card.Root>
	{/each}
</div>

<Card.Root>
	<Card.Header>
		<Card.Title>Graduation</Card.Title>
		<Card.Description>
			Cost and duration of every run. As the playbook grows, runs move from Explore to Assisted to
			Scripted and get cheaper.
		</Card.Description>
	</Card.Header>
	<Card.Content>
		{#if runs.length > 0}
			<GraduationChart {runs} versions={stats.versions ?? []} />
		{:else}
			<Empty.Root class="py-10">
				<Empty.Header>
					<Empty.Media variant="icon">
						<ChartColumnIcon />
					</Empty.Media>
					<Empty.Title>No runs in this period</Empty.Title>
					<Empty.Description
						>Runs show up here with their cost, duration and mode.</Empty.Description
					>
				</Empty.Header>
			</Empty.Root>
		{/if}
	</Card.Content>
</Card.Root>

<div class="grid items-start gap-6 lg:grid-cols-2">
	<Card.Root>
		<Card.Header>
			<Card.Title>Instruction</Card.Title>
			<Card.Action>
				<Button variant="ghost" size="icon-sm" href="/jobs/{job.id}/settings" aria-label="Edit">
					<PencilIcon />
				</Button>
			</Card.Action>
		</Card.Header>
		<Card.Content>
			<Markdown source={job.instruction} />
		</Card.Content>
	</Card.Root>

	<Card.Root>
		<Card.Header>
			<Card.Title>Spec</Card.Title>
			{#if spec.goal}
				<Card.Description>{spec.goal}</Card.Description>
			{/if}
		</Card.Header>
		<Card.Content class="flex flex-col gap-5 text-sm">
			{#if spec.successCriteria && spec.successCriteria.length > 0}
				<section class="flex flex-col gap-2">
					<h3 class="text-muted-foreground text-xs font-medium">Success criteria</h3>
					<ul class="flex list-disc flex-col gap-1 pl-4">
						{#each spec.successCriteria as criterion, i (i)}
							<li>{criterion}</li>
						{/each}
					</ul>
				</section>
			{/if}
			{#each [{ title: 'Inputs', fields: spec.inputs ?? [] }, { title: 'Outputs', fields: spec.outputs ?? [] }] as group (group.title)}
				{#if group.fields.length > 0}
					<section class="flex flex-col gap-2">
						<h3 class="text-muted-foreground text-xs font-medium">{group.title}</h3>
						<ul class="flex flex-col gap-1.5">
							{#each group.fields as field (field.name)}
								<li class="flex flex-wrap items-baseline gap-2">
									<span class="font-mono text-xs">{field.name}</span>
									<Badge variant="outline" class="font-mono font-normal">{field.type}</Badge>
									{#if field.description}
										<span class="text-muted-foreground">{field.description}</span>
									{/if}
								</li>
							{/each}
						</ul>
					</section>
				{/if}
			{/each}
			{#if spec.mcp && spec.mcp.length > 0}
				<section class="flex flex-col gap-2">
					<h3 class="text-muted-foreground text-xs font-medium">Services</h3>
					<ul class="flex flex-col gap-1.5">
						{#each spec.mcp as need, i (i)}
							<li>
								<span class="font-mono text-xs">{need.server}</span>
								<span class="text-muted-foreground">· {need.why}</span>
							</li>
						{/each}
					</ul>
				</section>
			{/if}
			{#if spec.sideEffects && spec.sideEffects.length > 0}
				<section class="flex flex-col gap-2">
					<h3 class="text-muted-foreground text-xs font-medium">Side effects</h3>
					<div class="flex flex-wrap gap-1.5">
						{#each spec.sideEffects as effect, i (i)}
							<Badge variant="secondary" class="font-normal">{effect}</Badge>
						{/each}
					</div>
				</section>
			{/if}
			<section class="flex flex-wrap gap-x-6 gap-y-2">
				<span>
					<span class="text-muted-foreground">Network</span>
					{job.network === 'none' ? 'None' : 'Internet'}
				</span>
				<span>
					<span class="text-muted-foreground">Concurrency</span>
					<span class="capitalize">{job.concurrency}</span>
				</span>
				<span>
					<span class="text-muted-foreground">Learning</span>
					{job.selfImprove ? 'On' : 'Off'}
				</span>
			</section>
		</Card.Content>
	</Card.Root>
</div>
