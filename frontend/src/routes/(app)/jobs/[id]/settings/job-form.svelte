<script lang="ts">
	import { invalidate } from '$app/navigation';
	import type { Job, JobPatch, Model, WorkspaceSettings } from '$lib/api/types';
	import FormInput from '$lib/components/form/form-input.svelte';
	import ScheduleEditor from '$lib/components/jobs/schedule-editor.svelte';
	import ModelSelect from '$lib/components/model-select.svelte';
	import * as Card from '$lib/components/ui/card';
	import * as Field from '$lib/components/ui/field';
	import * as Select from '$lib/components/ui/select';
	import { Switch } from '$lib/components/ui/switch';
	import { Textarea } from '$lib/components/ui/textarea';
	import JobService from '$lib/services/job-service';
	import { describeCron } from '$lib/utils/cron-util';
	import { createForm, deepEqual } from '$lib/utils/form-util';
	import { cleanSpec, concurrencyOptions, type ConcurrencyPolicy } from '$lib/utils/job-util';
	import { trackFormChanges } from '$lib/utils/unsaved-changes-util.svelte';
	import { untrack } from 'svelte';
	import { z } from 'zod/v4';

	let {
		job,
		models,
		settings
	}: {
		job: Job;
		models: Model[];
		settings: WorkspaceSettings;
	} = $props();

	const jobService = new JobService();

	// Empty number inputs mean "use the workspace default", so they become null rather than a validation error
	const toNull = (v: unknown) =>
		v === '' || v === undefined || v === null || Number.isNaN(v) ? null : v;
	const optionalNumber = (schema: z.ZodNumber) =>
		z.preprocess(toNull, schema.nullable().optional());

	// The bounds mirror the backend's `LimitOverrides` validation
	const formSchema = z.object({
		name: z.string().min(1, 'Required').max(200),
		instruction: z.string().min(1, 'Required').max(20000),
		modelId: z.string(),
		cron: z.string().max(100),
		timezone: z.string().max(100),
		enabled: z.boolean(),
		concurrency: z.enum(['skip', 'queue', 'parallel']),
		image: z.string().max(500),
		network: z.enum(['internet', 'allowlist', 'none']),
		allowedDomains: z.string().max(10000),
		allowPrivateNetwork: z.boolean(),
		runAsRoot: z.boolean(),
		timeoutSeconds: optionalNumber(z.number().int().min(30).max(86400)),
		maxTurns: optionalNumber(z.number().int().min(1).max(1000)),
		maxCostUsd: optionalNumber(z.number().min(0)),
		cpus: optionalNumber(z.number().min(0.1).max(64)),
		memoryMb: optionalNumber(z.number().int().min(64).max(262144)),
		pidsLimit: optionalNumber(z.number().int().min(16).max(65536)),
		selfImprove: z.boolean(),
		modePin: z.enum(['', 'explore', 'assisted', 'scripted'])
	});
	type FormValues = z.infer<typeof formSchema>;

	const networkLabels: Record<FormValues['network'], string> = {
		internet: 'Internet access',
		allowlist: 'Allowed domains only',
		none: 'No network'
	};

	const domainsPlaceholder = 'api.github.com\n*.githubusercontent.com';

	// The allow-list is edited one domain per line
	function domainLines(text: string) {
		return text
			.split('\n')
			.map((line) => line.trim())
			.filter(Boolean);
	}

	function valuesFrom(j: Job): FormValues {
		return {
			name: j.name,
			instruction: j.instruction,
			modelId: j.modelId ?? '',
			cron: j.cron ?? '',
			timezone: j.timezone ?? '',
			enabled: j.enabled,
			concurrency: (j.concurrency as ConcurrencyPolicy) || 'skip',
			image: j.image ?? '',
			network: (j.network as FormValues['network']) || 'internet',
			allowedDomains: (j.allowedDomains ?? []).join('\n'),
			allowPrivateNetwork: j.allowPrivateNetwork,
			runAsRoot: j.runAsRoot,
			timeoutSeconds: j.limits.timeoutSeconds ?? null,
			maxTurns: j.limits.maxTurns ?? null,
			maxCostUsd: j.limits.maxCostUsd ?? null,
			cpus: j.limits.cpus ?? null,
			memoryMb: j.limits.memoryMb ?? null,
			pidsLimit: j.limits.pidsLimit ?? null,
			selfImprove: j.selfImprove,
			modePin: (j.modePin as FormValues['modePin']) ?? ''
		};
	}

	const form = createForm(formSchema, valuesFrom(job));
	const inputs = form.inputs;

	const LIMIT_KEYS = [
		'timeoutSeconds',
		'maxTurns',
		'maxCostUsd',
		'cpus',
		'memoryMb',
		'pidsLimit'
	] as const satisfies (keyof FormValues)[];

	// The update endpoint merges, so only the fields that differ from the form's baseline are sent
	// That way saving never undoes a change made elsewhere in the meantime, e.g. pausing the job from the header
	async function save(values: FormValues) {
		const baseline = form.getBaseline();
		const changed = (...keys: (keyof FormValues)[]) =>
			keys.some((key) => !deepEqual(values[key], baseline[key]));
		const patch: JobPatch = {};

		// Plain fields are copied as they are, and optional text fields send an empty string to clear their value
		if (changed('name')) patch.name = values.name;
		if (changed('instruction')) patch.instruction = values.instruction;
		if (changed('modelId')) patch.modelId = values.modelId;
		if (changed('image')) patch.image = values.image;
		if (changed('modePin')) patch.modePin = values.modePin;
		if (changed('enabled')) patch.enabled = values.enabled;
		if (changed('concurrency')) patch.concurrency = values.concurrency;
		if (changed('network')) patch.network = values.network;
		if (changed('allowedDomains')) patch.allowedDomains = domainLines(values.allowedDomains);
		if (changed('allowPrivateNetwork')) patch.allowPrivateNetwork = values.allowPrivateNetwork;
		if (changed('runAsRoot')) patch.runAsRoot = values.runAsRoot;
		if (changed('selfImprove')) patch.selfImprove = values.selfImprove;

		// The backend replaces the limit overrides as a whole, so all of them are sent once any changed
		if (changed(...LIMIT_KEYS)) {
			const entries = LIMIT_KEYS.map((key) => [key, values[key]] as const);
			patch.limits = Object.fromEntries(entries.filter(([, v]) => v !== null && v !== undefined));
		}

		// Cron and timezone travel together, and removing the schedule clears the timezone as well
		const cron = values.cron.trim();
		const timezone = cron ? values.timezone || 'UTC' : '';
		if (changed('cron', 'timezone')) {
			patch.cron = cron;
			patch.timezone = timezone;
		}

		// The spec repeats the title, network and schedule, so it is rebuilt whenever one of them changed
		if (changed('name', 'network', 'cron', 'timezone')) {
			const compiled = job.spec.schedule;
			const human = compiled?.cron === cron ? compiled.human : (describeCron(cron) ?? '');
			patch.spec = cleanSpec({
				...job.spec,
				title: values.name,
				// The spec says what the job needs, and an allow-list job does reach the internet
				network: values.network === 'none' ? 'none' : 'internet',
				schedule: cron ? { cron, timezone, human } : undefined
			});
		}

		// Edits that only differ in form, e.g. a number typed and deleted again, leave nothing to send
		if (Object.keys(patch).length === 0) return;

		await jobService.update(job.id, patch);
		await invalidate('app:job');
	}

	trackFormChanges(() => form, save);

	// Whether the form holds unsaved edits, re-evaluated through the store whenever a save or discard moves the baseline
	const dirty = $derived.by(() => {
		void $inputs;
		return form.isDirty();
	});

	// Changes made elsewhere, e.g. the enabled switch in the header, move the form along unless it holds unsaved edits
	// This also runs once a save or discard leaves the form clean, so changes that landed while it held edits show up then
	$effect(() => {
		const next = valuesFrom(job);
		if (dirty) return;
		untrack(() => {
			form.commit(next);
			form.reset();
		});
	});

	const agentModel = $derived(models.find((m) => m.id === settings.agentModelId));
	const concurrencyDescription = $derived(
		concurrencyOptions.find((o) => o.value === $inputs.concurrency.value)?.description
	);

	const modePinOptions = [
		{
			value: 'auto',
			label: 'Automatic',
			description: 'The job graduates on its own as it learns.'
		},
		{ value: 'explore', label: 'Explore', description: 'Always runs with the agent from scratch.' },
		{
			value: 'assisted',
			label: 'Assisted',
			description: 'Always runs with the agent and the playbook.'
		},
		{
			value: 'scripted',
			label: 'Scripted',
			description: 'Runs the graduated script, falling back to the agent.'
		}
	];
	// bits-ui treats an empty value as "nothing selected", so "no pin" gets a sentinel of its own
	const modePin = $derived(
		modePinOptions.find((o) => o.value === ($inputs.modePin.value || 'auto'))
	);
</script>

<Card.Root>
	<Card.Header>
		<Card.Title>General</Card.Title>
		<Card.Description>What the job does and which model does it.</Card.Description>
	</Card.Header>
	<Card.Content>
		<Field.Group>
			<FormInput label="Name" bind:input={$inputs.name} />
			<FormInput
				label="Instruction"
				labelFor="job-instruction"
				input={$inputs.instruction}
				description="The agent follows this text on every run. Markdown is supported."
			>
				<Textarea
					id="job-instruction"
					bind:value={$inputs.instruction.value}
					aria-invalid={!!$inputs.instruction.error}
					class="min-h-32"
				/>
			</FormInput>
			<FormInput
				label="Model"
				labelFor="job-model"
				input={{ ...$inputs.modelId, required: false }}
				description="The model that drives the agent for this job."
			>
				<ModelSelect
					id="job-model"
					{models}
					bind:value={$inputs.modelId.value}
					noneLabel={agentModel
						? `Workspace default (${agentModel.label || agentModel.model})`
						: 'Workspace default'}
				/>
			</FormInput>
		</Field.Group>
	</Card.Content>
</Card.Root>

<Card.Root>
	<Card.Header>
		<Card.Title>Schedule</Card.Title>
		<Card.Description
			>When the job runs by itself, and what happens when runs overlap.</Card.Description
		>
	</Card.Header>
	<Card.Content>
		<Field.Group>
			<Field.Field orientation="horizontal">
				<Switch id="job-enabled-setting" bind:checked={$inputs.enabled.value} />
				<Field.Content>
					<Field.Label for="job-enabled-setting">Enabled</Field.Label>
					<Field.Description>
						A paused job keeps its schedule and webhook but doesn't start runs from them.
					</Field.Description>
				</Field.Content>
			</Field.Field>
			<ScheduleEditor
				bind:cron={$inputs.cron.value}
				bind:timezone={$inputs.timezone.value}
				cronError={$inputs.cron.error}
				timezoneError={$inputs.timezone.error}
			/>
			<FormInput
				label="When runs overlap"
				labelFor="job-concurrency"
				input={{ ...$inputs.concurrency, required: false }}
				description={concurrencyDescription}
			>
				<Select.Root type="single" bind:value={$inputs.concurrency.value}>
					<Select.Trigger id="job-concurrency" class="w-full md:w-64">
						{concurrencyOptions.find((o) => o.value === $inputs.concurrency.value)?.label}
					</Select.Trigger>
					<Select.Content>
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
	</Card.Content>
</Card.Root>

<Card.Root>
	<Card.Header>
		<Card.Title>Sandbox</Card.Title>
		<Card.Description>
			Where runs execute. Empty fields use the workspace defaults shown as placeholders.
		</Card.Description>
	</Card.Header>
	<Card.Content>
		<Field.Group>
			<FormInput
				label="Base image"
				placeholder={settings.defaultImage}
				inputClass="font-mono"
				description="A Dockerfile in the Environment tab builds on top of this image."
				bind:input={$inputs.image}
			/>
			<div class="grid grid-cols-1 gap-x-6 gap-y-7 md:grid-cols-2">
				<FormInput
					label="Network"
					labelFor="job-network"
					input={{ ...$inputs.network, required: false }}
				>
					<Select.Root type="single" bind:value={$inputs.network.value}>
						<Select.Trigger id="job-network" class="w-full">
							{networkLabels[$inputs.network.value]}
						</Select.Trigger>
						<Select.Content>
							{#each Object.entries(networkLabels) as [value, label] (value)}
								<Select.Item {value} {label} />
							{/each}
						</Select.Content>
					</Select.Root>
				</FormInput>
				<Field.Field orientation="horizontal" class="self-end">
					<Switch id="job-root" bind:checked={$inputs.runAsRoot.value} />
					<Field.Content>
						<Field.Label for="job-root">Run as root</Field.Label>
						<Field.Description>Lets the agent install system packages at runtime.</Field.Description
						>
					</Field.Content>
				</Field.Field>
			</div>
			{#if $inputs.network.value === 'allowlist'}
				<FormInput
					label="Allowed domains"
					labelFor="job-allowed-domains"
					description="One per line. *.example.com allows every name below example.com. Everything else is blocked."
					input={$inputs.allowedDomains}
				>
					<Textarea
						id="job-allowed-domains"
						rows={4}
						class="font-mono"
						placeholder={domainsPlaceholder}
						bind:value={$inputs.allowedDomains.value}
						aria-invalid={!!$inputs.allowedDomains.error}
					/>
				</FormInput>
			{/if}
			{#if $inputs.network.value !== 'none'}
				<Field.Field orientation="horizontal">
					<Switch id="job-private-network" bind:checked={$inputs.allowPrivateNetwork.value} />
					<Field.Content>
						<Field.Label for="job-private-network">Allow private network</Field.Label>
						<Field.Description>
							Lets the job reach the LAN and the Docker host, which are blocked otherwise. Cloud
							metadata stays blocked.
						</Field.Description>
					</Field.Content>
				</Field.Field>
			{/if}
			<div class="grid grid-cols-1 gap-x-6 gap-y-7 md:grid-cols-3">
				<FormInput
					label="Timeout"
					type="number"
					suffix="seconds"
					placeholder={String(settings.defaultLimits.timeoutSeconds)}
					bind:input={$inputs.timeoutSeconds}
				/>
				<FormInput
					label="Max turns"
					type="number"
					placeholder={String(settings.defaultLimits.maxTurns)}
					bind:input={$inputs.maxTurns}
				/>
				<FormInput
					label="Max cost per run"
					type="number"
					step="any"
					suffix="USD"
					placeholder={String(settings.defaultLimits.maxCostUsd)}
					bind:input={$inputs.maxCostUsd}
				/>
				<FormInput
					label="CPUs"
					type="number"
					step="any"
					placeholder={String(settings.defaultLimits.cpus)}
					bind:input={$inputs.cpus}
				/>
				<FormInput
					label="Memory"
					type="number"
					suffix="MB"
					placeholder={String(settings.defaultLimits.memoryMb)}
					bind:input={$inputs.memoryMb}
				/>
				<FormInput
					label="Process limit"
					type="number"
					placeholder={String(settings.defaultLimits.pidsLimit)}
					bind:input={$inputs.pidsLimit}
				/>
			</div>
		</Field.Group>
	</Card.Content>
</Card.Root>

<Card.Root>
	<Card.Header>
		<Card.Title>Learning</Card.Title>
		<Card.Description>How the job improves its playbook from its own runs.</Card.Description>
	</Card.Header>
	<Card.Content>
		<Field.Group>
			<Field.Field orientation="horizontal">
				<Switch id="job-self-improve" bind:checked={$inputs.selfImprove.value} />
				<Field.Content>
					<Field.Label for="job-self-improve">Self-improve</Field.Label>
					<Field.Description>
						After runs, reflection turns what worked into playbook versions that go live right away.
						When off, the playbook is frozen and no reflection cost is spent.
					</Field.Description>
				</Field.Content>
			</Field.Field>
			<FormInput
				label="Mode"
				labelFor="job-mode-pin"
				input={{ ...$inputs.modePin, required: false }}
				description={modePin?.description}
			>
				<Select.Root
					type="single"
					value={$inputs.modePin.value || 'auto'}
					onValueChange={(value) =>
						($inputs.modePin.value = (value === 'auto' ? '' : value) as FormValues['modePin'])}
				>
					<Select.Trigger id="job-mode-pin" class="w-full md:w-64">
						{modePin?.label ?? 'Automatic'}
					</Select.Trigger>
					<Select.Content>
						{#each modePinOptions as option (option.value)}
							<Select.Item value={option.value} label={option.label} />
						{/each}
					</Select.Content>
				</Select.Root>
			</FormInput>
		</Field.Group>
	</Card.Content>
</Card.Root>
