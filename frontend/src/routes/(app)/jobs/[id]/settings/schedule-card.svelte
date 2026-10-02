<script lang="ts">
	import { invalidate } from '$app/navigation';
	import type { Job } from '#lib/api/types.js';
	import FormCard from '#lib/components/form/form-card.svelte';
	import FormInput from '#lib/components/form/form-input.svelte';
	import ScheduleEditor from '#lib/components/jobs/schedule-editor.svelte';
	import * as Field from '#lib/components/ui/field/index.js';
	import * as Select from '#lib/components/ui/select/index.js';
	import JobService from '#lib/services/job-service.js';
	import { describeCron } from '#lib/utils/cron-util.js';
	import { createForm } from '#lib/utils/form-util.js';
	import { cleanSpec, concurrencyOptions, type ConcurrencyPolicy } from '#lib/utils/job-util.js';
	import { untrack } from 'svelte';
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

	// The schedule editor isn't a FormInput, so its errors are cleared on edit here, like FormInput does for its fields
	// The store also emits when an error is set, so only a value that differs from the last one seen counts as an edit
	for (const key of ['cron', 'timezone'] as const) {
		let seen = $inputs[key].value;
		$effect(() => {
			const current = $inputs[key].value;
			if (current === seen) return;
			seen = current;
			untrack(() => {
				if ($inputs[key].error) $inputs[key].error = null;
			});
		});
	}

	// Removing the schedule clears the timezone as well, and the spec repeats the schedule, so it goes along with it
	async function save({ cron, timezone, concurrency }: z.infer<typeof formSchema>) {
		timezone = cron ? timezone || 'UTC' : '';
		await jobService.updateSpec(
			job.id,
			(spec) => {
				const compiled = spec.schedule;
				const human = compiled?.cron === cron ? compiled.human : (describeCron(cron) ?? '');
				return cleanSpec({ ...spec, schedule: cron ? { cron, timezone, human } : undefined });
			},
			{ cron, timezone, concurrency }
		);
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
