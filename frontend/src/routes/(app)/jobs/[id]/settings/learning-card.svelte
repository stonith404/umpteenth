<script lang="ts">
	import { invalidate } from '$app/navigation';
	import type { Job } from '$lib/api/types';
	import FormCard from '$lib/components/form/form-card.svelte';
	import * as Field from '$lib/components/ui/field';
	import { Switch } from '$lib/components/ui/switch';
	import JobService from '$lib/services/job-service';
	import { createForm } from '$lib/utils/form-util';
	import { z } from 'zod/v4';

	let { job }: { job: Job } = $props();

	const jobService = new JobService();

	const formSchema = z.object({
		selfImprove: z.boolean(),
		graduate: z.boolean()
	});

	const form = createForm(formSchema, {
		selfImprove: job.selfImprove,
		graduate: job.graduate
	});
	const inputs = form.inputs;

	async function save(values: z.infer<typeof formSchema>) {
		await jobService.update(job.id, values);
		await invalidate('app:job');
	}
</script>

<FormCard
	title="Learning"
	description="How the job improves its playbook from its own runs."
	dirty={form.isDirty()}
	saving={form.saving}
	onsubmit={() => form.submit(save)}
>
	<Field.Group>
		<Field.Field orientation="horizontal">
			<Switch id="job-self-improve" bind:checked={$inputs.selfImprove.value} />
			<Field.Content>
				<Field.Label for="job-self-improve">Self-improve</Field.Label>
				<Field.Description>
					After runs, reflection turns what worked into playbook versions that go live right away.
					When off, the playbook is frozen and reflection makes no model calls.
				</Field.Description>
			</Field.Content>
		</Field.Field>
		<Field.Field orientation="horizontal">
			<Switch id="job-graduate" bind:checked={$inputs.graduate.value} />
			<Field.Content>
				<Field.Label for="job-graduate">Graduate to a script</Field.Label>
				<Field.Description>
					When runs settle into a repeatable pattern, reflection writes a script that replaces the
					agent. When off, every run uses the agent with the playbook.
				</Field.Description>
			</Field.Content>
		</Field.Field>
	</Field.Group>
</FormCard>
