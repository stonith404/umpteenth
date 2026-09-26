<script lang="ts">
	import { goto } from '$app/navigation';
	import { openConfirmDialog } from '$lib/components/confirm-dialog';
	import { Button } from '$lib/components/ui/button';
	import * as Card from '$lib/components/ui/card';
	import JobService from '$lib/services/job-service';
	import { apiErrorToast } from '$lib/utils/error-util';
	import { tryCatch } from '$lib/utils/try-catch-util';
	import Trash2Icon from '@lucide/svelte/icons/trash-2';
	import { toast } from 'svelte-sonner';
	import JobForm from './job-form.svelte';
	import McpServersCard from './mcp-servers-card.svelte';
	import SecretsCard from './secrets-card.svelte';
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

<JobForm {job} models={data.models} settings={data.settings} />
<McpServersCard jobId={job.id} attached={data.jobServers} servers={data.servers} />
<SecretsCard jobId={job.id} mappings={data.jobSecrets} secrets={data.secrets} />
<WebhookCard {job} />

<Card.Root class="ring-destructive/30">
	<Card.Header>
		<Card.Title>Delete job</Card.Title>
		<Card.Description>
			Stops the schedule and the webhook. Past runs and their outputs are kept.
		</Card.Description>
	</Card.Header>
	<Card.Footer>
		<Button variant="destructive" onclick={confirmDelete}>
			<Trash2Icon data-icon="inline-start" />
			Delete job
		</Button>
	</Card.Footer>
</Card.Root>
