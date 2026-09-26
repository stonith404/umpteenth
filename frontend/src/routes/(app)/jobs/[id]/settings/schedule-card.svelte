<script lang="ts">
	import { invalidate } from '$app/navigation';
	import type { Job } from '$lib/api/types';
	import FormCard from '$lib/components/form/form-card.svelte';
	import FormInput from '$lib/components/form/form-input.svelte';
	import ScheduleEditor from '$lib/components/jobs/schedule-editor.svelte';
	import * as Field from '$lib/components/ui/field';
	import * as Select from '$lib/components/ui/select';
	import JobService from '$lib/services/job-service';
	import { describeCron } from '$lib/utils/cron-util';
	import { createForm } from '$lib/utils/form-util';
	import { cleanSpec, concurrencyOptions, type ConcurrencyPolicy } from '$lib/utils/job-util';
	import { z } from 'zod/v4';

	let { job }: { job: Job } = $props();

	const jobService = new JobService();

	const formSchema = z.object({
		cron: z.string().max(100),
		timezone: z.string().max(100),
		concurrency: z.enum(['skip', 'queue', 'parallel'])
	});
	const form = createForm(formSchema, {
		cron: job.cron ?? '',
		// A schedule without a stored zone runs in UTC, which is also what saving it sends
		timezone: job.timezone || (job.cron ? 'UTC' : ''),
		concurrency: (job.concurrency as ConcurrencyPolicy) || 'skip'
	});
	const inputs = form.inputs;

	// Removing the schedule clears the timezone as well, and the spec repeats the schedule, so it goes along with it
	async function save({ cron, timezone, concurrency }: z.infer<typeof formSchema>) {
		timezone = cron ? timezone || 'UTC' : '';
		const compiled = job.spec.schedule;
		const human = compiled?.cron === cron ? compiled.human : (describeCron(cron) ?? '');
		const schedule = cron ? { cron, timezone, human } : undefined;
		await jobService.update(job.id, {
			cron,
			timezone,
			concurrency,
			spec: cleanSpec({ ...job.spec, schedule })
		});
		await invalidate('app:job');
	}

	const concurrency = $derived(
		concurrencyOptions.find((o) => o.value === $inputs.concurrency.value)
	);
</script>

<FormCard
	title="Schedule"
	description="When the job runs by itself, and what happens when runs overlap."
	dirty={form.isDirty()}
	saving={form.saving}
	onsubmit={() => form.submit(save)}
>
	<Field.Group>
		<ScheduleEditor
			bind:cron={$inputs.cron.value}
			bind:timezone={$inputs.timezone.value}
			cronError={$inputs.cron.error}
			timezoneError={$inputs.timezone.error}
		/>
		<FormInput
			label="When runs overlap"
			labelFor="job-concurrency"
			input={$inputs.concurrency}
			optional={false}
			description={concurrency?.description}
		>
			<Select.Root type="single" bind:value={$inputs.concurrency.value}>
				<Select.Trigger id="job-concurrency" class="w-full sm:max-w-72">
					{concurrency?.label}
				</Select.Trigger>
				<Select.Content align="start">
					{#each concurrencyOptions as option (option.value)}
						<Select.Item value={option.value} label={option.label}>
							<span class="flex flex-col">
								<span>{option.label}</span>
								<span class="text-muted-foreground text-xs font-normal whitespace-normal">
									{option.description}
								</span>
							</span>
						</Select.Item>
					{/each}
				</Select.Content>
			</Select.Root>
		</FormInput>
	</Field.Group>
</FormCard>
