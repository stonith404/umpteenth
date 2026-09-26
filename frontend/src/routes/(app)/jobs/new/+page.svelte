<script lang="ts">
	import { NEW_JOB_DRAFT_KEY } from '$lib/utils/job-util';
	import { goto } from '$app/navigation';
	import { isApiError } from '$lib/api/api-error';
	import type { JobIOField, JobQuestion, JobSpec } from '$lib/api/types';
	import CodeEditor from '$lib/components/code/code-editor.svelte';
	import { openConfirmDialog } from '$lib/components/confirm-dialog';
	import IoFieldsEditor from '$lib/components/form/io-fields-editor.svelte';
	import NetworkSelect, { networkLabel } from '$lib/components/form/network-select.svelte';
	import { revealFirstInvalidField } from '$lib/components/form/reveal-invalid-field';
	import StringListEditor from '$lib/components/form/string-list-editor.svelte';
	import ScheduleEditor from '$lib/components/jobs/schedule-editor.svelte';
	import PageHeader from '$lib/components/page-header.svelte';
	import * as Alert from '$lib/components/ui/alert';
	import { Button } from '$lib/components/ui/button';
	import * as Card from '$lib/components/ui/card';
	import * as Field from '$lib/components/ui/field';
	import { Input } from '$lib/components/ui/input';
	import { Switch } from '$lib/components/ui/switch';
	import { Textarea } from '$lib/components/ui/textarea';
	import JobService from '$lib/services/job-service';
	import { describeCron, isValidCron } from '$lib/utils/cron-util';
	import { apiErrorToast, getErrorMessage } from '$lib/utils/error-util';
	import {
		cleanSpec,
		DEFAULT_IMAGE_TOOLS,
		emptySpec,
		localTimezone,
		scheduleParts,
		type NetworkPolicy
	} from '$lib/utils/job-util';
	import { tryCatch } from '$lib/utils/try-catch-util';
	import { hasRole } from '$lib/utils/workspace-util';
	import ArrowLeftIcon from '@lucide/svelte/icons/arrow-left';
	import CircleAlertIcon from '@lucide/svelte/icons/circle-alert';
	import InfoIcon from '@lucide/svelte/icons/info';
	import PencilLineIcon from '@lucide/svelte/icons/pencil-line';
	import PlayIcon from '@lucide/svelte/icons/play';
	import SparklesIcon from '@lucide/svelte/icons/sparkles';
	import { onDestroy, tick, untrack } from 'svelte';
	import { cubicOut } from 'svelte/easing';
	import { prefersReducedMotion } from 'svelte/motion';
	import { toast } from 'svelte-sonner';
	import { fade, fly } from 'svelte/transition';
	import { dockerfileTemplate } from '../dockerfile-template';
	import CompileProgress from './compile-progress.svelte';
	import McpMatcher from './mcp-matcher.svelte';
	import QuestionsCard from './questions-card.svelte';

	let { data } = $props();

	const jobService = new JobService();

	// The description survives a reload, since writing a good one takes a while
	const DRAFT_KEY = NEW_JOB_DRAFT_KEY;

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

	type Phase = 'describe' | 'compiling' | 'questions' | 'review';

	let phase = $state<Phase>('describe');
	let instruction = $state(readDraft());
	let compileError = $state<unknown>(null);
	let compileStartedAt = $state(0);
	let compileAbort: AbortController | null = null;

	// Compiling again from the review returns there when it fails or is cancelled, so the edits made to the spec survive
	let compileFrom = $state<'describe' | 'review'>('describe');

	// What the compile step couldn't settle from the description, answered before the spec is reviewed
	let questions = $state<JobQuestion[]>([]);
	let answers = $state<string[]>([]);

	// From the questions until the review, the questions card takes the description card's place, also while the answers compile
	let answering = $state(false);

	// Whether the spec under review came from the compiler, as opposed to being filled in by hand
	let compiled = $state(false);
	const compileHint = $derived(
		compiled
			? 'Compiling it again replaces the spec below.'
			: 'Compiling it replaces the spec below.'
	);

	// Going back to the description keeps the spec under review, so it can be returned to until a compile replaces it
	let hasReview = $state(false);

	// The spec as it entered the review, which tells whether compiling again would throw away the user's edits
	let reviewBaseline = '';

	// Focus moves along when switching between the two steps, so keyboard users start where the new step starts
	let describeInput = $state<HTMLTextAreaElement | null>(null);
	let backToDescriptionButton = $state<HTMLButtonElement | null>(null);

	// Without a utility or agent model compiling can only fail, so the page says so up front and offers the manual way instead
	const needsModel = $derived(!data.canCompile || isApiError(compileError, 'unsupported'));
	const canManageSettings = $derived(hasRole(data.user, 'admin'));

	// API messages rarely end with a period, and the banner reads them as the first of two sentences
	const compileErrorMessage = $derived(
		getErrorMessage(compileError, 'The job could not be compiled').replace(/([^.!?])$/, '$1.')
	);

	// The hint names the modifier people actually press, like the sidebar's ⌘K
	const compileShortcut = /Mac|iPhone|iPad/.test(navigator.platform) ? '⌘ Enter' : 'Ctrl Enter';

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

	// Storage can be unavailable, e.g. in private windows, and the draft is a convenience only
	function readDraft() {
		try {
			return sessionStorage.getItem(DRAFT_KEY) ?? '';
		} catch {
			return '';
		}
	}

	function writeDraft(value: string) {
		try {
			if (value) sessionStorage.setItem(DRAFT_KEY, value);
			else sessionStorage.removeItem(DRAFT_KEY);
		} catch {
			// See readDraft
		}
	}

	$effect(() => writeDraft(instruction));

	// Moving between the steps fades the old one out before the new one rises in, both in one grid cell so they never stack
	const motion = (ms: number) => (prefersReducedMotion.current ? 0 : ms);
	const stepOut = (node: Element) => fade(node, { duration: motion(120) });
	const stepIn = (node: Element) =>
		fly(node, { y: 8, duration: motion(220), delay: motion(100), easing: cubicOut });

	// Leaving the page mid-compile stops the utility model instead of letting it finish for nobody
	onDestroy(() => compileAbort?.abort());

	// A compile replaces the spec under review, so edits made to it are only thrown away once the user agrees
	function compile() {
		if (!instruction.trim() || needsModel || phase === 'compiling') return;
		if (!reviewEdited()) {
			void runCompile();
			return;
		}
		openConfirmDialog({
			title: 'Discard the spec you edited?',
			message: 'Compiling replaces the spec under review, and the changes you made to it are lost.',
			confirm: {
				label: 'Discard and compile',
				destructive: true,
				// Not awaited, since the dialog would otherwise stay open with a spinner for the whole compile
				action: () => void runCompile()
			}
		});
	}

	// askQuestions is false once the questions of an earlier compile are answered, so answering never leads to another round
	// The text only becomes the instruction once it compiled, so cancelling the answers leaves the description as it was
	async function runCompile({ text = instruction.trim(), askQuestions = true } = {}) {
		// Answering keeps the origin of the compile that asked, so cancelling still returns there
		if (phase !== 'questions') compileFrom = phase === 'review' ? 'review' : 'describe';
		compileError = null;
		compileAbort = new AbortController();
		compileStartedAt = Date.now();
		phase = 'compiling';

		const controller = compileAbort;
		const result = await tryCatch(
			jobService.compile(text, { askQuestions, signal: controller.signal })
		);

		// A cancelled compile has already returned to where it started
		if (controller.signal.aborted) return;
		compileAbort = null;

		// A failed recompile keeps the reviewed spec, and a failed first compile shows why next to the description
		// Answers that fail to compile stay on their card, so trying again doesn't mean answering again
		if (result.error) {
			if (answering) {
				phase = 'questions';
				apiErrorToast(result.error, 'Failed to compile the job');
				return;
			}
			if (compileFrom === 'review') {
				phase = 'review';
				apiErrorToast(result.error, 'Failed to compile the job');
				return;
			}
			compileError = result.error;
			phase = 'describe';
			return;
		}

		// Open questions come before the review, since a spec compiled without their answers is only a guess
		if (result.data.questions && result.data.questions.length > 0) {
			showQuestions(result.data.questions);
			return;
		}
		instruction = text;
		applySpec(result.data.spec);
		compiled = true;
		enterReview();
	}

	// The card focuses its first question itself once it is in place
	function showQuestions(asked: JobQuestion[]) {
		questions = asked;
		answers = asked.map(() => '');
		answering = true;
		phase = 'questions';
	}

	// The answers become part of the instruction, which the agent reads on every run, and the spec is compiled again from it
	// The card only submits once every question is answered
	function submitAnswers() {
		const lines = questions.map((q, i) => `- ${q.question} ${answers[i].trim()}`);
		const text = `${instruction.trim()}\n\nClarifications:\n${lines.join('\n')}`;
		void runCompile({ text, askQuestions: false });
	}

	// Leaving the questions unanswered returns to where the compile started, and the spec under review there stays as it was
	function leaveQuestions() {
		if (compileFrom === 'review') void backToReview();
		else void backToDescription();
	}

	// Cancelling the answers' compile goes back to the last question rather than dropping them
	function cancelCompile() {
		compileAbort?.abort();
		compileAbort = null;
		phase = answering ? 'questions' : compileFrom;
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
		network = compiled.network;
		useDockerfile = !!compiled.dockerfile;
		dockerfile = compiled.dockerfile ?? dockerfileTemplate(data.defaultImage);
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
		applySpec(emptySpec());
		compiled = false;
		timezone = localTimezone();
		enterReview();
	}

	// Starts reviewing a freshly applied spec, remembering it as it was so later edits can be told apart
	function enterReview() {
		reviewBaseline = reviewFingerprint();
		hasReview = true;
		answering = false;
		phase = 'review';
	}

	// The spec as it would be saved plus the servers to attach, so edits that change nothing, like an empty row, don't count
	function reviewFingerprint() {
		return JSON.stringify({ spec: buildSpec(), servers: [...attachedServers].sort() });
	}

	function reviewEdited() {
		return hasReview && reviewFingerprint() !== reviewBaseline;
	}

	// Leaves the review as it is, so going back to the description loses nothing
	async function backToDescription() {
		answering = false;
		phase = 'describe';
		compileError = null;
		await tick();
		describeInput?.focus();
	}

	async function backToReview() {
		answering = false;
		phase = 'review';
		compileError = null;
		await tick();
		backToDescriptionButton?.focus();
	}

	// The compiled plain-words schedule is kept while the expression is unchanged, otherwise it is described again
	function scheduleHuman() {
		if (compiledSchedule && compiledSchedule.cron === cron.trim()) return compiledSchedule.human;
		return describeCron(cron) ?? '';
	}

	// The rail reads the schedule back like the jobs list does, and flags an expression the scheduler would reject
	// An expression without a description in words shows as it is, in mono like every other cron expression
	const schedule = $derived.by(() => {
		const plain = { zone: null, invalid: false, raw: false };
		if (!cron.trim()) return { ...plain, text: 'On demand' };
		if (!isValidCron(cron)) return { ...plain, text: 'Invalid schedule', invalid: true };
		const human = scheduleHuman();
		const parts = scheduleParts({
			cron: cron.trim(),
			timezone: timezone || 'UTC',
			scheduleHuman: human
		});
		return {
			text: parts?.label ?? cron.trim(),
			zone: parts?.zone ?? null,
			invalid: false,
			raw: !human
		};
	});

	// The rail names the servers to attach rather than counting them, since a count doesn't say which ones
	const attachedNames = $derived(
		data.mcpServers
			.filter((server) => attachedServers.includes(server.id))
			.map((server) => server.name)
			.join(', ')
	);

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

	// A field's error goes away as soon as the field is edited, instead of lingering on a fixed value until the next save
	function clearErrorOnEdit(key: string, value: () => unknown) {
		$effect(() => {
			value();
			untrack(() => {
				if (!(key in errors)) return;
				const next = { ...errors };
				delete next[key];
				errors = next;
			});
		});
	}

	clearErrorOnEdit('name', () => name);
	clearErrorOnEdit('instruction', () => instruction);
	clearErrorOnEdit('cron', () => cron);
	clearErrorOnEdit('timezone', () => timezone);
	clearErrorOnEdit('network', () => network);

	// The fields that show an error of their own
	const FIELD_ERROR_KEYS = ['name', 'instruction', 'cron', 'timezone', 'network'];

	// Maps API field errors (e.g. `body.name` or `cron`) onto the inputs they belong to
	// Returns whether every error landed on a field the page shows, since the others can only be reported in a toast
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
		const keys = Object.keys(next);
		return keys.length > 0 && keys.every((key) => FIELD_ERROR_KEYS.includes(key));
	}

	async function save(run: boolean) {
		// The first invalid field is brought into view rather than announced in a toast, which would sit on the save buttons on small screens
		if (!validate()) {
			await revealFirstInvalidField();
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
				const allShown = applyFieldErrors(created.error.fields);
				await revealFirstInvalidField();
				if (allShown) return;
			}
			apiErrorToast(created.error, 'Failed to create the job');
			return;
		}
		const job = created.data;
		writeDraft('');

		// Attach the chosen MCP servers, a failure leaves a usable job that can be fixed in its settings
		if (attachedServers.length > 0) {
			const attached = await tryCatch(
				jobService.setMcpServers(
					job.id,
					attachedServers.map((serverId) => ({ serverId, allowedTools: null }))
				)
			);
			// The job exists either way, so a toast confirms it next to the one that says what failed
			if (attached.error) {
				saving = null;
				toast.success(`Created "${job.name}"`);
				apiErrorToast(attached.error, 'Failed to attach the MCP servers');
				await goto(`/jobs/${job.id}/settings`);
				return;
			}
		}

		// Start the first run right away when asked to, and follow it live
		if (run) {
			const triggered = await tryCatch(jobService.runNow(job.id));
			saving = null;
			if (triggered.error) {
				toast.success(`Created "${job.name}"`);
				apiErrorToast(triggered.error, 'Failed to start a run');
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

<div class="grid-stack">
	{#if phase !== 'review'}
		<!-- The describe or questions card stays through compiling, so the progress rail can open next to it and hand the card's width over smoothly -->
		<!-- It spans the review's width, which is where the spec appears once compiling finishes -->
		<div class="flex max-w-6xl flex-col xl:flex-row xl:items-start" in:stepIn out:stepOut>
			{#if phase === 'compiling'}
				<CompileProgress startedAt={compileStartedAt} />
			{/if}
			<!-- The questions take the description's place while the rail closes, and keep it while their answers compile -->
			<div class="grid-stack min-w-0 flex-1">
				{#if answering}
					<div in:stepIn out:stepOut>
						<QuestionsCard
							{questions}
							bind:answers
							locked={phase === 'compiling'}
							backLabel={compileFrom === 'review' ? 'Back to review' : 'Back to description'}
							onback={leaveQuestions}
							onsubmit={submitAnswers}
							oncancel={cancelCompile}
						/>
					</div>
				{:else}
					<div in:stepIn out:stepOut>
						<Card.Root>
							<Card.Header>
								<Card.Title>Describe the job</Card.Title>
								<Card.Description>
									Say what to do, when to do it and where the results should go. Mention the
									services it needs, like GitHub or Slack.
								</Card.Description>
							</Card.Header>
							<Card.Content>
								<div class="flex flex-col gap-4">
									{#if needsModel}
										<Alert.Root variant="info">
											<InfoIcon />
											<Alert.Title>Compiling needs a utility model</Alert.Title>
											<Alert.Description>
												{#if canManageSettings}
													Set one in the workspace settings, or fill in the spec yourself.
												{:else}
													An admin of the workspace can set one in its settings. Until then, fill in
													the spec yourself.
												{/if}
											</Alert.Description>
											<!-- Outside the description, which underlines every link in it -->
											{#if canManageSettings}
												<div class="col-start-2 mt-2">
													<Button href="/settings/general" variant="outline" size="xs"
														>Open settings</Button
													>
												</div>
											{/if}
										</Alert.Root>
									{/if}
									<!-- Locked while compiling, since the spec on its way was compiled from the text as it was -->
									<Textarea
										bind:ref={describeInput}
										bind:value={instruction}
										aria-label="Describe the job"
										placeholder="Every weekday at 8:00 Berlin time, look at open pull requests in acme/api that have had no activity for 3+ days…"
										readonly={phase === 'compiling'}
										locked={phase === 'compiling'}
										class="min-h-48"
										onkeydown={onDescribeKeydown}
									/>
									<!-- Examples only fill an empty description, so picking one never overwrites a draft -->
									{#if !instruction.trim()}
										<div class="flex flex-wrap items-center gap-2">
											<span class="text-muted-foreground text-xs">Examples:</span>
											{#each examples as example (example.label)}
												<Button
													variant="outline"
													size="xs"
													onclick={() => (instruction = example.text)}
												>
													{example.label}
												</Button>
											{/each}
										</div>
									{/if}
									{#if compileError && !needsModel}
										<Alert.Root variant="destructive">
											<CircleAlertIcon />
											<Alert.Title>Compiling failed</Alert.Title>
											<Alert.Description>
												{compileErrorMessage} You can try again, or fill in the spec yourself.
											</Alert.Description>
										</Alert.Root>
									{/if}
								</div>
							</Card.Content>
							<Card.Footer class="flex flex-wrap justify-between">
								<!-- The ghost button's icon lines up with the textarea's edge, and it becomes the main way forward while compiling is unavailable -->
								<!-- Once a spec is under review, the way back to it takes the place of filling in an empty one, which would throw it away -->
								<!-- While compiling the only way out is Cancel, so the button fades out rather than leaving room for a second exit -->
								{#if phase === 'describe'}
									<div transition:fade={{ duration: 150 }}>
										<Button
											variant={needsModel ? 'outline' : 'ghost'}
											class={needsModel ? '' : '-ml-2.5'}
											onclick={hasReview ? backToReview : fillManually}
										>
											{#if hasReview}
												<ArrowLeftIcon data-icon="inline-start" />
												Back to review
											{:else}
												<PencilLineIcon data-icon="inline-start" />
												Fill in manually
											{/if}
										</Button>
									</div>
								{/if}
								<!-- Cancel takes the Compile button's place, right under the pointer that just pressed it -->
								<div class="ml-auto flex items-center gap-3">
									{#if phase === 'compiling'}
										<Button variant="outline" onclick={cancelCompile}>Cancel</Button>
									{:else}
										{#if !needsModel}
											<kbd class="text-muted-foreground hidden font-sans text-xs sm:inline">
												{compileShortcut}
											</kbd>
										{/if}
										<Button disabled={!instruction.trim() || needsModel} onclick={compile}>
											<SparklesIcon data-icon="inline-start" />
											{compileError && !needsModel ? 'Try again' : 'Compile'}
										</Button>
									{/if}
								</div>
							</Card.Footer>
						</Card.Root>
					</div>
				{/if}
			</div>
		</div>
	{:else}
		<!-- Below xl the form takes the whole width and the save actions float at the bottom of the window, from xl they sit in a rail -->
		<!-- Below xl this is a flex column rather than a grid, since a sticky grid item can't leave its own grid row -->
		<!-- From xl the way back sits in a row of its own above the form, so the rail still lines up with the first card -->
		<div
			class="flex max-w-6xl flex-col gap-6 xl:grid-cols-form-rail xl:grid xl:items-start"
			in:stepIn
			out:stepOut
		>
			<!-- Labelled and away from the save buttons at every width, so it never reads as part of saving, and the review stays for coming back to -->
			<!-- The ghost button's icon lines up with the cards' edge, and the negative margin keeps its padding from widening the gap under it -->
			<div class="-mb-2 xl:col-start-1 xl:row-start-1">
				<Button
					bind:ref={backToDescriptionButton}
					variant="ghost"
					size="sm"
					class="-ml-2"
					disabled={saving !== null}
					onclick={backToDescription}
				>
					<ArrowLeftIcon data-icon="inline-start" />
					Back to description
				</Button>
			</div>

			<div class="flex min-w-0 flex-col gap-6 xl:col-start-1 xl:row-start-2">
				<Card.Root>
					<Card.Header>
						<Card.Title>Job</Card.Title>
						<Card.Description>The name and goal show up in lists and run pages.</Card.Description>
					</Card.Header>
					<Card.Content>
						<Field.Group>
							<Field.Field data-invalid={!!errors.name}>
								<Field.Label for="job-name">Name</Field.Label>
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
								<Field.Label for="job-goal" optional>Goal</Field.Label>
								<Textarea
									id="job-goal"
									bind:value={spec.goal}
									placeholder="One sentence describing the outcome"
								/>
							</Field.Field>
							<Field.Field data-invalid={!!errors.instruction}>
								<Field.Label for="job-instruction">Instruction</Field.Label>
								<Textarea
									id="job-instruction"
									bind:value={instruction}
									class="min-h-28"
									aria-invalid={!!errors.instruction}
								/>
								{#if errors.instruction}
									<Field.Error>{errors.instruction}</Field.Error>
								{:else if needsModel}
									<Field.Description>The agent follows this text on every run.</Field.Description>
								{:else}
									<Field.Description>
										The agent follows this text on every run. {compileHint}
									</Field.Description>
								{/if}
								<!-- Wrapped, since the field stretches its direct children to the full width -->
								<!-- A spec filled in by hand was never compiled, so the button doesn't offer to compile it again -->
								{#if !needsModel}
									<div>
										<Button
											variant="outline"
											size="sm"
											disabled={!instruction.trim()}
											onclick={compile}
										>
											<SparklesIcon data-icon="inline-start" />
											{compiled ? 'Compile again' : 'Compile'}
										</Button>
									</div>
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
					<Card.Content>
						<div class="flex flex-col gap-6">
							<Field.Set>
								<Field.Legend variant="label">Inputs</Field.Legend>
								<IoFieldsEditor bind:fields={inputs} label="Input" addLabel="Add input" />
							</Field.Set>
							<Field.Set>
								<Field.Legend variant="label">Outputs</Field.Legend>
								<IoFieldsEditor bind:fields={outputs} label="Output" addLabel="Add output" />
							</Field.Set>
						</div>
					</Card.Content>
				</Card.Root>

				<Card.Root>
					<Card.Header>
						<Card.Title>MCP servers</Card.Title>
						<!-- Only a compiled spec knows which services the job needs, one filled in by hand leaves the choice to the user -->
						<Card.Description>
							{compiled
								? 'External services the job needs, matched to your servers.'
								: "The job's agent and scripts can use the tools of the servers you attach."}
						</Card.Description>
					</Card.Header>
					<Card.Content>
						<McpMatcher
							{compiled}
							needs={spec.mcp ?? []}
							servers={data.mcpServers}
							bind:selected={attachedServers}
						/>
					</Card.Content>
				</Card.Root>

				<!-- Named like the job settings' card, where the same network and image choices live later -->
				<Card.Root>
					<Card.Header>
						<Card.Title>Sandbox</Card.Title>
						<Card.Description>Network access and extra tools for the sandbox.</Card.Description>
					</Card.Header>
					<Card.Content>
						<Field.Group>
							<Field.Field data-invalid={!!errors.network}>
								<Field.Label for="job-network">Network</Field.Label>
								<NetworkSelect
									id="job-network"
									choices={['internet', 'none']}
									bind:value={network}
								/>
								{#if errors.network}
									<Field.Error>{errors.network}</Field.Error>
								{:else}
									<Field.Description>
										MCP servers you reach over HTTP keep working without network access.
									</Field.Description>
								{/if}
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
							<!-- Said where the Dockerfile is chosen, since saving works and only the image build would fail -->
							{#if useDockerfile && !data.imageBuilds}
								<p class="text-warning-foreground text-sm">
									This sandbox backend can't build images, so runs of a job with a Dockerfile fail.
								</p>
							{/if}
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

			<!-- One copy of the save actions: a summary rail from xl, a bar that stays at the bottom of the window below it -->
			<!-- The bar is only as wide as its buttons and centered, and spans the window on phones -->
			<aside
				aria-label="Save the job"
				data-slot="docked-save-bar"
				class="sticky bottom-4 z-10 max-xl:self-center max-sm:self-stretch xl:col-start-2 xl:row-start-2 xl:top-20 xl:bottom-auto"
			>
				<Card.Root variant="dock">
					<Card.Header class="max-xl:hidden">
						<Card.Title>Ready to save?</Card.Title>
						<Card.Description>
							You can change all of this later in the job's settings.
						</Card.Description>
					</Card.Header>
					<Card.Content class="max-xl:hidden">
						<dl class="grid-cols-label-value-clipped grid gap-x-4 gap-y-2 text-sm">
							<dt class="text-muted-foreground">Schedule</dt>
							<dd
								class={['truncate', schedule.invalid && 'text-warning-foreground']}
								title={cron.trim() ? `${cron.trim()} · ${timezone || 'UTC'}` : undefined}
							>
								<span class={[schedule.raw && 'font-mono text-xs']}>{schedule.text}</span>
								{#if schedule.zone}<span class="text-muted-foreground">· {schedule.zone}</span>{/if}
							</dd>
							<dt class="text-muted-foreground">MCP servers</dt>
							<dd class="truncate" title={attachedNames || undefined}>{attachedNames || 'None'}</dd>
							<dt class="text-muted-foreground">Network</dt>
							<dd class="truncate">{networkLabel(network)}</dd>
							<dt class="text-muted-foreground">Image</dt>
							<dd class="truncate">{useDockerfile ? 'Custom Dockerfile' : 'Default'}</dd>
						</dl>
					</Card.Content>
					<!-- On phones the two saves share the bar's width -->
					<Card.Footer class="max-sm:*:flex-1 xl:flex-col xl:items-stretch">
						<Button
							class="max-xl:order-last"
							isLoading={saving === 'run'}
							disabled={saving !== null}
							onclick={() => save(true)}
						>
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
					</Card.Footer>
				</Card.Root>
			</aside>
		</div>
	{/if}
</div>

<style>
	/* Below xl the save bar is docked at the bottom of the window, so toasts rise above it instead of covering its buttons */
	@media (width < 80rem) {
		:global(:root:has([data-slot='docked-save-bar'])) {
			--toast-clearance: 4rem;
		}
	}
</style>
