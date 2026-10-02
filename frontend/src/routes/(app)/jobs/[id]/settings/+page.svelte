<script lang="ts">
	import { goto } from '$app/navigation';
	import { openConfirmDialog } from '$lib/components/confirm-dialog';
	import DangerZone from '$lib/components/danger-zone.svelte';
	import * as Card from '$lib/components/ui/card';
	import JobService from '$lib/services/job-service';
	import { apiErrorToast } from '$lib/utils/error-util';
	import { tryCatch } from '$lib/utils/try-catch-util';
	import Trash2Icon from '@lucide/svelte/icons/trash-2';
	import { toast } from 'svelte-sonner';
	import GeneralCard from './general-card.svelte';
	import LearningCard from './learning-card.svelte';
	import LoadError from './load-error.svelte';
	import McpServersCard from './mcp-servers-card.svelte';
	import SandboxCard from './sandbox-card.svelte';
	import ScheduleCard from './schedule-card.svelte';
	import SecretsCard from './secrets-card.svelte';
	import SkillsCard from './skills-card.svelte';
	import SpecCard from './spec-card.svelte';
	import WebhookCard from './webhook-card.svelte';

	let { data } = $props();

	const jobService = new JobService();

	const job = $derived(data.job);

	function confirmDelete() {
		openConfirmDialog({
			title: `Delete ${job.name}`,
			message:
				'The job stops running and disappears from lists. Its past runs stay available in the run history.',
			confirm: {
				label: 'Delete job',
				destructive: true,
				action: async () => {
					const result = await tryCatch(jobService.delete(job.id));
					if (result.error) {
						apiErrorToast(result.error, 'Failed to delete the job');
						return;
					}
					toast.success(`Deleted "${job.name}"`);
					await goto('/jobs');
				}
			}
		});
	}
</script>

<svelte:head>
	<title>Settings · {job.name} · Umpteenth</title>
</svelte:head>

<div class="flex flex-col gap-6">
	<!-- Each card saves its own fields, so saving one never sends another card's edits along -->
	<GeneralCard
		{job}
		models={data.models.data ?? []}
		modelsError={data.models.error}
		settings={data.settings}
	/>
	<!-- A rebuilt spec replaces the card's values, which its form only reads when it starts -->
	{#key JSON.stringify( [job.spec.goal, job.spec.successCriteria, job.spec.inputs, job.spec.outputs, job.spec.sideEffects] )}
		<SpecCard {job} />
	{/key}
	<ScheduleCard {job} />
	<SandboxCard {job} settings={data.settings} networks={data.networks} />
	<LearningCard {job} />

	<!-- Without the job's own attachments the card can't be edited safely, since saving would replace them -->
	{#if data.jobServers.data}
		<McpServersCard
			jobId={job.id}
			attached={data.jobServers.data}
			servers={data.servers.data}
			serversError={data.servers.error}
		/>
	{:else}
		<Card.Root>
			<Card.Header>
				<Card.Title>MCP servers</Card.Title>
				<Card.Description>The job can call the tools of attached servers.</Card.Description>
			</Card.Header>
			<Card.Content>
				<LoadError title="Couldn't load the job's MCP servers" error={data.jobServers.error} />
			</Card.Content>
		</Card.Root>
	{/if}

	{#if data.jobSkills.data}
		<SkillsCard
			jobId={job.id}
			attached={data.jobSkills.data}
			skills={data.skills.data}
			skillsError={data.skills.error}
		/>
	{:else}
		<Card.Root>
			<Card.Header>
				<Card.Title>Skills</Card.Title>
				<Card.Description>Every run gets the attached skills in /ump/skills.</Card.Description>
			</Card.Header>
			<Card.Content>
				<LoadError title="Couldn't load the job's skills" error={data.jobSkills.error} />
			</Card.Content>
		</Card.Root>
	{/if}

	{#if data.jobSecrets.data}
		<SecretsCard
			jobId={job.id}
			mappings={data.jobSecrets.data}
			secrets={data.secrets.data}
			secretsError={data.secrets.error}
		/>
	{:else}
		<Card.Root>
			<Card.Header>
				<Card.Title>Secrets</Card.Title>
				<Card.Description>Secrets reach the sandbox as environment variables.</Card.Description>
			</Card.Header>
			<Card.Content>
				<LoadError title="Couldn't load the job's secrets" error={data.jobSecrets.error} />
			</Card.Content>
		</Card.Root>
	{/if}

	<WebhookCard {job} />

	<DangerZone
		actions={[
			{
				title: 'Delete this job',
				description: 'Stops the schedule and the webhook. Past runs and their outputs are kept.',
				label: 'Delete job',
				icon: Trash2Icon,
				onclick: confirmDelete
			}
		]}
	/>
</div>
