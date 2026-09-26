<script lang="ts">
	import { invalidate } from '$app/navigation';
	import type { Job, WorkspaceSettings } from '$lib/api/types';
	import FormCard from '$lib/components/form/form-card.svelte';
	import FormInput from '$lib/components/form/form-input.svelte';
	import NetworkSelect, { type NetworkChoice } from '$lib/components/form/network-select.svelte';
	import * as Field from '$lib/components/ui/field';
	import { Switch } from '$lib/components/ui/switch';
	import { Textarea } from '$lib/components/ui/textarea';
	import JobService from '$lib/services/job-service';
	import { createForm } from '$lib/utils/form-util';
	import { cleanSpec } from '$lib/utils/job-util';
	import { z } from 'zod/v4';

	let {
		job,
		settings,
		networks = []
	}: {
		job: Job;
		settings: WorkspaceSettings;
		// The network policies the sandbox backend offers, empty when it couldn't be asked
		networks?: string[];
	} = $props();

	// The unrestricted network is up to the operator, but a job that already has it keeps showing it
	const networkChoices = $derived<NetworkChoice[]>(
		networks.includes('unrestricted') || job.network === 'unrestricted'
			? ['internet', 'allowlist', 'none', 'unrestricted']
			: ['internet', 'allowlist', 'none']
	);

	const jobService = new JobService();

	// Empty number inputs mean "use the workspace default", so they become null rather than a validation error
	const toNull = (v: unknown) =>
		v === '' || v === undefined || v === null || Number.isNaN(v) ? null : v;
	const optionalNumber = (schema: z.ZodNumber) =>
		z.preprocess(toNull, schema.nullable().optional());

	// The bounds mirror the backend's `LimitOverrides` validation
	const limitsSchema = z.object({
		timeoutSeconds: optionalNumber(z.number().int().min(30).max(86400)),
		maxTurns: optionalNumber(z.number().int().min(1).max(1000)),
		maxCostUsd: optionalNumber(z.number().min(0)),
		cpus: optionalNumber(z.number().min(0.1).max(64)),
		memoryMb: optionalNumber(z.number().int().min(64).max(262144))
	});
	const formSchema = z.object({
		image: z.string().max(500),
		network: z.enum(['internet', 'allowlist', 'none', 'unrestricted']),
		// One domain per line, tidied on save so the field shows what was stored
		allowedDomains: z
			.string()
			.max(10000)
			.transform((text) =>
				text
					.split('\n')
					.map((line) => line.trim())
					.filter(Boolean)
					.join('\n')
			),
		allowPrivateNetwork: z.boolean(),
		runAsRoot: z.boolean(),
		...limitsSchema.shape
	});

	const form = createForm(formSchema, {
		image: job.image ?? '',
		network: job.network,
		allowedDomains: (job.allowedDomains ?? []).join('\n'),
		allowPrivateNetwork: job.allowPrivateNetwork,
		runAsRoot: job.runAsRoot,
		timeoutSeconds: job.limits.timeoutSeconds ?? null,
		maxTurns: job.limits.maxTurns ?? null,
		maxCostUsd: job.limits.maxCostUsd ?? null,
		cpus: job.limits.cpus ?? null,
		memoryMb: job.limits.memoryMb ?? null
	});
	const inputs = form.inputs;

	// The backend replaces the limit overrides as a whole, so every set one is sent
	// The spec says what the job needs, and an allow-list job does reach the internet
	async function save(values: z.infer<typeof formSchema>) {
		const { image, network, allowedDomains, allowPrivateNetwork, runAsRoot, ...limits } = values;
		await jobService.update(job.id, {
			image,
			network,
			allowedDomains: allowedDomains.split('\n').filter(Boolean),
			allowPrivateNetwork,
			runAsRoot,
			limits: Object.fromEntries(Object.entries(limits).filter(([, value]) => value != null)),
			spec: cleanSpec({ ...job.spec, network: network === 'none' ? 'none' : 'internet' })
		});
		await invalidate('app:job');
	}

	// The placeholder spans two lines, which only a script string can hold
	const domainsPlaceholder = 'api.github.com\n*.githubusercontent.com';

	const limitFields = [
		{ key: 'timeoutSeconds', label: 'Timeout', suffix: 'seconds' },
		{ key: 'maxTurns', label: 'Max turns' },
		{ key: 'maxCostUsd', label: 'Max cost per run', suffix: 'USD', step: 'any' },
		{ key: 'cpus', label: 'CPUs', suffix: 'cores', step: 'any' },
		{ key: 'memoryMb', label: 'Memory', suffix: 'MB' }
	] as const;
</script>

{#snippet allowedDomainsDescription()}
	One per line. <code class="font-mono text-xs">*.example.com</code> allows every name below
	<code class="font-mono text-xs">example.com</code>. Everything else is blocked.
{/snippet}

<FormCard
	title="Sandbox"
	description="The image, network and limits of the container runs execute in."
	dirty={form.isDirty()}
	saving={form.saving}
	onsubmit={() => form.submit(save)}
>
	<Field.Group>
		<FormInput
			label="Base image"
			placeholder={settings.defaultImage}
			monospace
			description="Empty uses the workspace default. A Dockerfile in the Environment tab builds on top of this image."
			bind:input={$inputs.image}
		/>
		<!-- Every network setting follows the policy it refines, so the toggles form one column under the select -->
		<FormInput label="Network" labelFor="job-network" input={$inputs.network} optional={false}>
			<NetworkSelect id="job-network" choices={networkChoices} bind:value={$inputs.network.value} />
		</FormInput>
		{#if $inputs.network.value === 'allowlist'}
			<FormInput
				label="Allowed domains"
				labelFor="job-allowed-domains"
				optional={false}
				description={allowedDomainsDescription}
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
		<!-- Only the egress proxy tells private from public addresses, so the switch means nothing without it -->
		{#if $inputs.network.value === 'internet' || $inputs.network.value === 'allowlist'}
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
		<Field.Field orientation="horizontal">
			<Switch id="job-root" bind:checked={$inputs.runAsRoot.value} />
			<Field.Content>
				<Field.Label for="job-root">Run as root</Field.Label>
				<Field.Description>Lets the agent install system packages at runtime.</Field.Description>
			</Field.Content>
		</Field.Field>
		<!-- Every limit may stay empty, so the set explains that once instead of marking six fields optional -->
		<Field.Set>
			<Field.Legend>Limits</Field.Legend>
			<Field.Description>Empty fields use the workspace default, shown in grey.</Field.Description>
			<div class="grid grid-cols-2 gap-x-4 gap-y-6 md:grid-cols-3 md:gap-x-6 md:gap-y-7">
				{#each limitFields as field (field.key)}
					<FormInput
						label={field.label}
						type="number"
						suffix={'suffix' in field ? field.suffix : undefined}
						step={'step' in field ? field.step : undefined}
						optional={false}
						placeholder={String(settings.defaultLimits[field.key])}
						bind:input={$inputs[field.key]}
					/>
				{/each}
			</div>
		</Field.Set>
	</Field.Group>
</FormCard>
