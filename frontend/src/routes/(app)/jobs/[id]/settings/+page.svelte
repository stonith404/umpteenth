<script lang="ts">
	import { goto } from '$app/navigation';
	import { openConfirmDialog } from '#lib/components/confirm-dialog/index.js';
	import DangerZone from '#lib/components/danger-zone.svelte';
	import SettingsSection from '#lib/components/settings-section.svelte';
	import TableOfContents from '#lib/components/table-of-contents.svelte';
	import JobService from '#lib/services/job-service.js';
	import { apiErrorToast } from '#lib/utils/error-util.js';
	import { tryCatch } from '#lib/utils/try-catch-util.js';
	import TrashIcon from 'phosphor-svelte/lib/Trash';
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

<!-- Cloudflare's settings layout: a column of sections beside a table of contents that follows the scroll -->
<div class="lg:grid-cols-toc grid grid-cols-1 gap-8">
	<div class="flex min-w-0 flex-col gap-10">
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
			<SettingsSection
				title="MCP servers"
				description="The job can call the tools of attached servers."
			>
				<LoadError title="Couldn't load the job's MCP servers" error={data.jobServers.error} />
			</SettingsSection>
		{/if}

		{#if data.jobSkills.data}
			<SkillsCard
				jobId={job.id}
				attached={data.jobSkills.data}
				skills={data.skills.data}
				skillsError={data.skills.error}
			/>
		{:else}
			<SettingsSection
				title="Skills"
				description="Every run gets the attached skills in /ump/skills."
			>
				<LoadError title="Couldn't load the job's skills" error={data.jobSkills.error} />
			</SettingsSection>
		{/if}

		{#if data.jobSecrets.data}
			<SecretsCard
				jobId={job.id}
				mappings={data.jobSecrets.data}
				secrets={data.secrets.data}
				secretsError={data.secrets.error}
			/>
		{:else}
			<SettingsSection
				title="Secrets"
				description="Secrets reach the sandbox as environment variables."
			>
				<LoadError title="Couldn't load the job's secrets" error={data.jobSecrets.error} />
			</SettingsSection>
		{/if}

		<WebhookCard {job} />

		<DangerZone
			actions={[
				{
					title: 'Delete this job',
					description: 'Stops the schedule and the webhook. Past runs and their outputs are kept.',
					label: 'Delete job',
					icon: TrashIcon,
					onclick: confirmDelete
				}
			]}
		/>
	</div>
	<div class="max-lg:hidden">
		<TableOfContents
			items={[
				{ label: 'General' },
				{ label: 'Spec' },
				{ label: 'Schedule' },
				{ label: 'Sandbox' },
				{ label: 'Learning' },
				{ label: 'MCP servers' },
				{ label: 'Skills' },
				{ label: 'Secrets' },
				{ label: 'Webhook' },
				{ label: 'Danger zone', danger: true }
			]}
		/>
	</div>
</div>
