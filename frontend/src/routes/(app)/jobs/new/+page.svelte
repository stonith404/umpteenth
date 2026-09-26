<script lang="ts">
	import { goto } from '$app/navigation';
	import { isApiError } from '$lib/api/api-error';
	import type { JobIOField, JobSpec } from '$lib/api/types';
	import CodeEditor from '$lib/components/code/code-editor.svelte';
	import IoFieldsEditor from '$lib/components/form/io-fields-editor.svelte';
	import StringListEditor from '$lib/components/form/string-list-editor.svelte';
	import ScheduleEditor from '$lib/components/jobs/schedule-editor.svelte';
	import PageHeader from '$lib/components/page-header.svelte';
	import * as Alert from '$lib/components/ui/alert';
	import { Button } from '$lib/components/ui/button';
	import * as Card from '$lib/components/ui/card';
	import * as Field from '$lib/components/ui/field';
	import { Input } from '$lib/components/ui/input';
	import * as Select from '$lib/components/ui/select';
	import { Switch } from '$lib/components/ui/switch';
	import { Textarea } from '$lib/components/ui/textarea';
	import JobService from '$lib/services/job-service';
	import { describeCron } from '$lib/utils/cron-util';
	import { apiErrorToast, getErrorMessage } from '$lib/utils/error-util';
	import {
		cleanSpec,
		DEFAULT_IMAGE_TOOLS,
		emptySpec,
		localTimezone,
		type NetworkPolicy
	} from '$lib/utils/job-util';
	import { tryCatch } from '$lib/utils/try-catch-util';
	import ArrowLeftIcon from '@lucide/svelte/icons/arrow-left';
	import CircleAlertIcon from '@lucide/svelte/icons/circle-alert';
	import PencilLineIcon from '@lucide/svelte/icons/pencil-line';
	import PlayIcon from '@lucide/svelte/icons/play';
	import SparklesIcon from '@lucide/svelte/icons/sparkles';
	import TriangleAlertIcon from '@lucide/svelte/icons/triangle-alert';
	import { onMount } from 'svelte';
	import { toast } from 'svelte-sonner';
	import CompileProgress from './compile-progress.svelte';
	import McpMatcher from './mcp-matcher.svelte';

	let { data } = $props();

	const jobService = new JobService();

	// The description survives a reload, since writing a good one takes a while
	const DRAFT_KEY = 'umpteenth:new-job-draft';

	const examples = [
		{
			label: 'Stale PR digest',
			text: "Every weekday at 8:00 Berlin time, look at open pull requests in acme/api that have had no activity for 3+ days. Post a short summary to the #eng Slack channel, grouped by author. If there are none, don't post."
		},
		{
			label: 'Uptime check',
			text: 'Every 15 minutes, check that https://example.com responds with HTTP 200 within 2 seconds. Remember the last status in state and only report when it changes.'
		},
		{
			label: 'Release notes',
			text: 'When triggered by a webhook with a GitHub release payload, summarize the changes since the previous release in plain language and return the summary as the output "notes".'
		}
	];

	type Phase = 'describe' | 'compiling' | 'review';

	let phase = $state<Phase>('describe');
	let instruction = $state('');
	let compileError = $state<unknown>(null);
	let compileStartedAt = $state(0);
	let compileAbort: AbortController | null = null;

	// The editable spec and the job fields derived from it
	let spec = $state<JobSpec>(emptySpec());
	let name = $state('');
	let cron = $state('');
	let timezone = $state('');
	let compiledSchedule = $state<{ cron: string; human: string } | null>(null);
	let network = $state<NetworkPolicy>('internet');
	let useDockerfile = $state(false);
	let dockerfile = $state('');
	let attachedServers = $state<string[]>([]);
	let successCriteria = $state<string[]>([]);
	let sideEffects = $state<string[]>([]);
	let inputs = $state<JobIOField[]>([]);
	let outputs = $state<JobIOField[]>([]);

	let saving = $state<'save' | 'run' | null>(null);
	let errors = $state<Record<string, string>>({});

	const defaultDockerfile = `FROM ghcr.io/stonith404/umpteenth-sandbox:latest\n\n# Install extra tools here, e.g.\n# RUN apt-get update && apt-get install -y --no-install-recommends ffmpeg && rm -rf /var/lib/apt/lists/*\n`;

	onMount(() => {
		try {
			instruction = sessionStorage.getItem(DRAFT_KEY) ?? '';
		} catch {
			// Storage can be unavailable, e.g. in private windows, the draft is a convenience only
		}
	});

	$effect(() => {
		try {
			if (instruction) sessionStorage.setItem(DRAFT_KEY, instruction);
			else sessionStorage.removeItem(DRAFT_KEY);
		} catch {
			// See above
		}
	});

	async function compile() {
		if (!instruction.trim()) return;
		compileError = null;
		compileAbort = new AbortController();
		compileStartedAt = Date.now();
		phase = 'compiling';

		const controller = compileAbort;
		const result = await tryCatch(jobService.compile(instruction.trim(), controller.signal));

		// A cancelled compile has already returned to the description
		if (controller.signal.aborted) return;
		compileAbort = null;

		if (result.error) {
			compileError = result.error;
			phase = 'describe';
			return;
		}
		applySpec(result.data);
		phase = 'review';
	}

	function cancelCompile() {
		compileAbort?.abort();
		compileAbort = null;
		phase = 'describe';
	}

	// Copies a compiled spec into the editable state and preselects the MCP servers it names
	function applySpec(compiled: JobSpec) {
		spec = compiled;
		name = compiled.title;
		cron = compiled.schedule?.cron ?? '';
		timezone = compiled.schedule?.timezone ?? '';
		compiledSchedule = compiled.schedule
			? { cron: compiled.schedule.cron, human: compiled.schedule.human }
			: null;
		network = compiled.network === 'none' ? 'none' : 'internet';
		useDockerfile = !!compiled.dockerfile;
		dockerfile = compiled.dockerfile ?? defaultDockerfile;
		successCriteria = [...(compiled.successCriteria ?? [])];
		sideEffects = [...(compiled.sideEffects ?? [])];
		inputs = (compiled.inputs ?? []).map((f) => ({ ...f, description: f.description ?? '' }));
		outputs = (compiled.outputs ?? []).map((f) => ({ ...f, description: f.description ?? '' }));

		const needed = new Set((compiled.mcp ?? []).map((need) => need.server.toLowerCase()));
		attachedServers = data.mcpServers
			.filter((server) => needed.has(server.name.toLowerCase()))
			.map((server) => server.id);
		errors = {};
	}

	// Skips the compile step, e.g. when no utility model is configured
	function fillManually() {
		applySpec({ ...emptySpec(), title: '' });
		cron = '';
		timezone = localTimezone();
		useDockerfile = false;
		phase = 'review';
	}

	function startOver() {
		phase = 'describe';
		compileError = null;
	}

	// The compiled plain-words schedule is kept while the expression is unchanged, otherwise it is described again
	function scheduleHuman() {
		if (compiledSchedule && compiledSchedule.cron === cron.trim()) return compiledSchedule.human;
		return describeCron(cron) ?? '';
	}

	function buildSpec(): JobSpec {
		const clean = (items: string[]) => items.map((s) => s.trim()).filter(Boolean);
		const cleanFields = (fields: JobIOField[]) =>
			fields
				.filter((f) => f.name.trim())
				.map((f) => ({ ...f, name: f.name.trim(), description: f.description?.trim() }));
		const hasSchedule = cron.trim() !== '';

		return cleanSpec({
			...spec,
			title: name.trim(),
			goal: spec.goal.trim(),
			schedule: hasSchedule
				? { cron: cron.trim(), timezone: timezone || 'UTC', human: scheduleHuman() }
				: undefined,
			successCriteria: clean(successCriteria),
			sideEffects: clean(sideEffects),
			inputs: cleanFields(inputs),
			outputs: cleanFields(outputs),
			network,
			dockerfile: useDockerfile && dockerfile.trim() ? dockerfile : null
		});
	}

	function validate() {
		const next: Record<string, string> = {};
		if (!name.trim()) next.name = 'Required';
		if (!instruction.trim()) next.instruction = 'Required';
		errors = next;
		return Object.keys(next).length === 0;
	}

	// Maps API field errors (e.g. `body.name` or `cron`) onto the inputs they belong to
	function applyFieldErrors(fields: { field: string; message: string }[]) {
		const next: Record<string, string> = {};
		for (const f of fields) {
			const key =
				f.field
					.replace(/^body\./, '')
					.split('.')
					.pop() ?? f.field;
			next[key] = f.message;
		}
		errors = next;
	}

	async function save(run: boolean) {
		if (!validate()) {
			toast.error('Please fix the errors before saving');
			return;
		}
		saving = run ? 'run' : 'save';

		// Create the job first, everything else is attached to it
		const hasSchedule = cron.trim() !== '';
		const created = await tryCatch(
			jobService.create({
				name: name.trim(),
				instruction: instruction.trim(),
				spec: buildSpec(),
				network,
				cron: hasSchedule ? cron.trim() : undefined,
				timezone: hasSchedule ? timezone || 'UTC' : undefined
			})
		);
		if (created.error) {
			saving = null;
			if (isApiError(created.error, 'validation_failed', 'invalid_field')) {
				applyFieldErrors(created.error.fields);
			}
			apiErrorToast(created.error, 'Failed to create the job');
			return;
		}
		const job = created.data;
		clearDraft();

		// Attach the chosen MCP servers, a failure leaves a usable job that can be fixed in its settings
		if (attachedServers.length > 0) {
			const attached = await tryCatch(
				jobService.setMcpServers(
					job.id,
					attachedServers.map((serverId) => ({ serverId, allowedTools: null }))
				)
			);
			if (attached.error) {
				saving = null;
				apiErrorToast(attached.error, 'The job was created, but attaching MCP servers failed');
				await goto(`/jobs/${job.id}/settings`);
				return;
			}
		}

		// Start the first run right away when asked to, and follow it live
		if (run) {
			const triggered = await tryCatch(jobService.runNow(job.id));
			saving = null;
			if (triggered.error) {
				apiErrorToast(triggered.error, 'The job was created, but starting a run failed');
				await goto(`/jobs/${job.id}`);
				return;
			}
			toast.success(`Created "${job.name}" and started a run`);
			await goto(`/runs/${triggered.data.runId}`);
			return;
		}

		saving = null;
		toast.success(`Created "${job.name}"`);
		await goto(`/jobs/${job.id}`);
	}

	function clearDraft() {
		try {
			sessionStorage.removeItem(DRAFT_KEY);
		} catch {
			// See onMount
		}
	}

	function onDescribeKeydown(event: KeyboardEvent) {
		if (event.key === 'Enter' && (event.metaKey || event.ctrlKey)) {
			event.preventDefault();
			void compile();
		}
	}
</script>

<svelte:head>
	<title>New job · Umpteenth</title>
</svelte:head>

<PageHeader
	title="New job"
	description="Describe what should happen in plain language, then review the compiled spec."
/>

{#if phase === 'describe'}
	<Card.Root>
		<Card.Header>
			<Card.Title>Describe the job</Card.Title>
			<Card.Description>
				Say what to do, when to do it and where the results should go. Mention the services it
				needs, like GitHub or Slack.
			</Card.Description>
		</Card.Header>
		<Card.Content class="flex flex-col gap-4">
			<Textarea
				bind:value={instruction}
				aria-label="Describe the job"
				placeholder="Every weekday at 8:00 Berlin time, look at open pull requests in acme/api that have had no activity for 3+ days…"
				class="min-h-48 text-base md:text-base"
				onkeydown={onDescribeKeydown}
			/>
			<div class="flex flex-wrap items-center gap-2">
				<span class="text-muted-foreground text-xs">Examples:</span>
				{#each examples as example (example.label)}
					<Button variant="outline" size="xs" onclick={() => (instruction = example.text)}>
						{example.label}
					</Button>
				{/each}
			</div>
			{#if compileError}
				<Alert.Root variant="destructive">
					<CircleAlertIcon />
					<Alert.Title>Compiling failed</Alert.Title>
					<Alert.Description>
						<p>{getErrorMessage(compileError, 'The job could not be compiled')}</p>
						<p>You can try again, or fill in the spec yourself.</p>
					</Alert.Description>
				</Alert.Root>
			{/if}
		</Card.Content>
		<Card.Footer class="flex flex-wrap justify-between gap-2">
			<Button variant="ghost" onclick={fillManually}>
				<PencilLineIcon data-icon="inline-start" />
				Fill in manually
			</Button>
			<div class="flex items-center gap-3">
				<span class="text-muted-foreground hidden text-xs sm:inline">⌘ + Enter</span>
				<Button disabled={!instruction.trim()} onclick={compile}>
					<SparklesIcon data-icon="inline-start" />
					{compileError ? 'Try again' : 'Compile'}
				</Button>
			</div>
		</Card.Footer>
	</Card.Root>
{:else if phase === 'compiling'}
	<CompileProgress startedAt={compileStartedAt} onCancel={cancelCompile} />
{:else}
	<div class="grid items-start gap-6 lg:grid-cols-[minmax(0,1fr)_20rem]">
		<div class="flex min-w-0 flex-col gap-6">
			{#if spec.warnings && spec.warnings.length > 0}
				<Alert.Root variant="warning">
					<TriangleAlertIcon />
					<Alert.Title>Check before saving</Alert.Title>
					<Alert.Description>
						<ul class="list-disc pl-4">
							{#each spec.warnings as warning, i (i)}
								<li>{warning}</li>
							{/each}
						</ul>
					</Alert.Description>
				</Alert.Root>
			{/if}

			<Card.Root>
				<Card.Header>
					<Card.Title>Job</Card.Title>
					<Card.Description>The name and goal show up in lists and run pages.</Card.Description>
				</Card.Header>
				<Card.Content>
					<Field.Group>
						<Field.Field data-invalid={!!errors.name}>
							<Field.Label for="job-name" required>Name</Field.Label>
							<Input
								id="job-name"
								bind:value={name}
								maxlength={200}
								placeholder="e.g. Stale PR digest"
								aria-invalid={!!errors.name}
							/>
							{#if errors.name}<Field.Error>{errors.name}</Field.Error>{/if}
						</Field.Field>
						<Field.Field>
							<Field.Label for="job-goal">Goal</Field.Label>
							<Textarea
								id="job-goal"
								bind:value={spec.goal}
								placeholder="One sentence describing the outcome"
							/>
						</Field.Field>
						<Field.Field data-invalid={!!errors.instruction}>
							<Field.Label for="job-instruction" required>Instruction</Field.Label>
							<Textarea
								id="job-instruction"
								bind:value={instruction}
								class="min-h-28"
								aria-invalid={!!errors.instruction}
							/>
							{#if errors.instruction}
								<Field.Error>{errors.instruction}</Field.Error>
							{:else}
								<Field.Description>
									The agent follows this text on every run. After larger changes, compile again to
									refresh the spec.
								</Field.Description>
							{/if}
						</Field.Field>
					</Field.Group>
				</Card.Content>
			</Card.Root>

			<Card.Root>
				<Card.Header>
					<Card.Title>Schedule</Card.Title>
					<Card.Description>When the job runs by itself.</Card.Description>
				</Card.Header>
				<Card.Content>
					<ScheduleEditor
						bind:cron
						bind:timezone
						cronError={errors.cron}
						timezoneError={errors.timezone}
					/>
				</Card.Content>
			</Card.Root>

			<Card.Root>
				<Card.Header>
					<Card.Title>Success criteria</Card.Title>
					<Card.Description>
						Checkable statements that decide whether a run succeeded.
					</Card.Description>
				</Card.Header>
				<Card.Content>
					<StringListEditor
						bind:items={successCriteria}
						label="Success criterion"
						placeholder="e.g. Exactly one message is posted to #eng"
						addLabel="Add criterion"
					/>
				</Card.Content>
			</Card.Root>

			<Card.Root>
				<Card.Header>
					<Card.Title>Inputs and outputs</Card.Title>
					<Card.Description>
						Inputs arrive as <code class="font-mono text-xs">/ump/input.json</code>. Outputs are
						small values each run reports, tracked across runs.
					</Card.Description>
				</Card.Header>
				<Card.Content class="flex flex-col gap-6">
					<Field.Set>
						<Field.Legend variant="label">Inputs</Field.Legend>
						<IoFieldsEditor bind:fields={inputs} label="Input" addLabel="Add input" />
					</Field.Set>
					<Field.Set>
						<Field.Legend variant="label">Outputs</Field.Legend>
						<IoFieldsEditor bind:fields={outputs} label="Output" addLabel="Add output" />
					</Field.Set>
				</Card.Content>
			</Card.Root>

			<Card.Root>
				<Card.Header>
					<Card.Title>MCP servers</Card.Title>
					<Card.Description
						>External services the job needs, matched to your servers.</Card.Description
					>
				</Card.Header>
				<Card.Content>
					<McpMatcher
						needs={spec.mcp ?? []}
						servers={data.mcpServers}
						bind:selected={attachedServers}
					/>
				</Card.Content>
			</Card.Root>

			<Card.Root>
				<Card.Header>
					<Card.Title>Environment</Card.Title>
					<Card.Description>Network access and the tools installed in the sandbox.</Card.Description
					>
				</Card.Header>
				<Card.Content>
					<Field.Group>
						<Field.Field>
							<Field.Label for="job-network">Network</Field.Label>
							<Select.Root type="single" bind:value={network}>
								<Select.Trigger id="job-network" class="w-full sm:w-64">
									{network === 'internet' ? 'Internet access' : 'No network'}
								</Select.Trigger>
								<Select.Content>
									<Select.Item value="internet" label="Internet access" />
									<Select.Item value="none" label="No network" />
								</Select.Content>
							</Select.Root>
							<Field.Description>
								MCP servers over HTTP connect from the host and work either way.
							</Field.Description>
						</Field.Field>
						<Field.Field orientation="horizontal">
							<Switch id="job-dockerfile" bind:checked={useDockerfile} />
							<Field.Content>
								<Field.Label for="job-dockerfile">Custom environment</Field.Label>
								<Field.Description>
									The default image already has {DEFAULT_IMAGE_TOOLS.join(', ')}. Add a Dockerfile
									only for anything else, it is built once before the first run.
								</Field.Description>
							</Field.Content>
						</Field.Field>
						{#if useDockerfile}
							<CodeEditor
								bind:value={dockerfile}
								language="dockerfile"
								label="Dockerfile"
								class="h-56"
							/>
						{/if}
					</Field.Group>
				</Card.Content>
			</Card.Root>

			<Card.Root>
				<Card.Header>
					<Card.Title>Side effects</Card.Title>
					<Card.Description>What the job changes outside its sandbox.</Card.Description>
				</Card.Header>
				<Card.Content>
					<StringListEditor
						bind:items={sideEffects}
						label="Side effect"
						placeholder="e.g. Posts to Slack"
						addLabel="Add side effect"
					/>
				</Card.Content>
			</Card.Root>
		</div>

		<Card.Root class="lg:sticky lg:top-20">
			<Card.Header>
				<Card.Title>Ready to save?</Card.Title>
				<Card.Description>You can change everything later in the job's settings.</Card.Description>
			</Card.Header>
			<Card.Content>
				<dl class="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 text-sm">
					<dt class="text-muted-foreground">Schedule</dt>
					<dd class="truncate">
						{cron.trim() ? scheduleHuman() || cron : 'On demand'}
					</dd>
					<dt class="text-muted-foreground">MCP servers</dt>
					<dd>{attachedServers.length || 'None'}</dd>
					<dt class="text-muted-foreground">Network</dt>
					<dd>{network === 'internet' ? 'Internet' : 'None'}</dd>
					<dt class="text-muted-foreground">Image</dt>
					<dd>{useDockerfile ? 'Custom Dockerfile' : 'Default'}</dd>
				</dl>
			</Card.Content>
			<Card.Footer class="flex flex-col items-stretch gap-2">
				<Button isLoading={saving === 'run'} disabled={saving !== null} onclick={() => save(true)}>
					<PlayIcon data-icon="inline-start" />
					Save & run
				</Button>
				<Button
					variant="outline"
					isLoading={saving === 'save'}
					disabled={saving !== null}
					onclick={() => save(false)}
				>
					Save
				</Button>
				<Button variant="ghost" disabled={saving !== null} onclick={startOver}>
					<ArrowLeftIcon data-icon="inline-start" />
					Back to description
				</Button>
			</Card.Footer>
		</Card.Root>
	</div>
{/if}
