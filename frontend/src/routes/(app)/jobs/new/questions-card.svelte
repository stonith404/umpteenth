<script lang="ts">
	import type { JobQuestion } from '$lib/api/types';
	import { Button } from '$lib/components/ui/button';
	import * as Card from '$lib/components/ui/card';
	import * as Field from '$lib/components/ui/field';
	import { Input } from '$lib/components/ui/input';
	import { cn } from '$lib/utils/style';
	import ArrowLeftIcon from '@lucide/svelte/icons/arrow-left';
	import ArrowRightIcon from '@lucide/svelte/icons/arrow-right';
	import CheckIcon from '@lucide/svelte/icons/check';
	import SparklesIcon from '@lucide/svelte/icons/sparkles';
	import { onMount, tick } from 'svelte';
	import { prefersReducedMotion } from 'svelte/motion';
	import { fly } from 'svelte/transition';

	let {
		questions,
		answers = $bindable([]),
		backLabel,
		locked = false,
		onback,
		onsubmit,
		oncancel
	}: {
		// What the compile step couldn't settle from the description
		questions: JobQuestion[];
		// One answer per question, in the same order
		answers?: string[];
		// Where going back from the first question leads, the description or the spec under review
		backLabel: string;
		// While the answers compile, the card shows them all and only offers to cancel
		locked?: boolean;
		onback: () => void;
		onsubmit: () => void;
		oncancel: () => void;
	} = $props();

	// One question at a time, so each gets the whole card and the answers before it read as a trail above
	let step = $state(0);
	let direction = $state(1);
	let error = $state<string | null>(null);

	// Answers typed into "Something else", kept apart so picking an option and coming back doesn't lose them
	let custom = $state<boolean[]>(questions.map(() => false));
	let customText = $state<string[]>(questions.map(() => ''));

	let root = $state<HTMLElement | null>(null);

	const question = $derived(questions[step]);
	const options = $derived(question?.options ?? []);
	const last = $derived(step === questions.length - 1);

	// The answers so far show above the question, and all of them while they compile
	const trail = $derived(locked ? questions.length : step);

	onMount(() => focusStep());

	// Focus follows the step, so the keyboard alone is enough to get through the questions
	async function focusStep() {
		await tick();
		const target =
			root?.querySelector<HTMLElement>('[data-step-input]') ??
			root?.querySelector<HTMLElement>('input[type="radio"]:checked') ??
			root?.querySelector<HTMLElement>('input[type="radio"]');
		target?.focus();
	}

	function go(to: number) {
		if (locked || to === step) return;
		direction = to > step ? 1 : -1;
		step = to;
		error = null;
		void focusStep();
	}

	function pick(option: string) {
		answers[step] = option;
		custom[step] = false;
		error = null;
	}

	function pickCustom() {
		custom[step] = true;
		answers[step] = customText[step];
		error = null;
	}

	function typeCustom(value: string) {
		customText[step] = value;
		pickCustom();
	}

	// The last question hands the answers over, every other one moves on once it is answered
	function next(event?: SubmitEvent) {
		event?.preventDefault();
		if (locked) return;
		if (!answers[step]?.trim()) {
			error = options.length > 0 ? 'Pick an option or type your own answer' : 'Required';
			void focusStep();
			return;
		}
		if (last) onsubmit();
		else go(step + 1);
	}

	function back() {
		if (step === 0) onback();
		else go(step - 1);
	}

	// Enter on a picked option continues, digits pick the numbered options and the one after them jumps into "Something else"
	function onkeydown(event: KeyboardEvent) {
		if (locked || event.metaKey || event.ctrlKey || event.altKey) return;
		const target = event.target as HTMLElement;

		// Browsers only submit on Enter from a text field, but a picked option should continue just the same
		if (event.key === 'Enter' && target.matches('input[type="radio"]')) {
			event.preventDefault();
			next();
			return;
		}
		if (target.matches('input[type="text"], input:not([type])')) return;
		const n = Number(event.key);
		if (!Number.isInteger(n) || n < 1 || n > options.length + 1 || options.length === 0) return;
		event.preventDefault();
		if (n <= options.length) {
			pick(options[n - 1]);
			root?.querySelectorAll<HTMLInputElement>('input[type="radio"]')[n - 1]?.focus();
		} else {
			pickCustom();
			void focusStep();
		}
	}

	// A new question slides in from the side it comes from, the one it replaces is gone at once so the two never stack
	function stepIn(node: Element) {
		return fly(node, { x: 12 * direction, duration: prefersReducedMotion.current ? 0 : 180 });
	}
</script>

<!-- A form, so Enter continues like the button does -->
<!-- The keydown handler only catches the digit shortcuts bubbling up from the options, every control inside stays reachable on its own -->
<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
<form aria-label="Questions about the job" bind:this={root} onsubmit={next} {onkeydown}>
	<Card.Root>
		<Card.Header>
			<Card.Title>A few questions first</Card.Title>
			<Card.Description>
				The description leaves these open. Your answers are added to the instruction.
			</Card.Description>
			<Card.Action>
				<div class="flex flex-col items-end gap-1.5">
					<span class="text-muted-foreground text-xs tabular-nums" aria-live="polite">
						{locked ? 'All answered' : `Question ${step + 1} of ${questions.length}`}
					</span>
					<!-- Answered questions can be jumped back to, the ones ahead only once everything before them is answered -->
					<div class="flex gap-0.75">
						{#each questions as q, i (i)}
							{@const answered = !!answers[i]?.trim()}
							<button
								type="button"
								class={cn(
									'bg-fill focus-visible:ring-ring h-1 w-5.5 rounded-xs outline-none focus-visible:ring-2 disabled:cursor-default',
									answered && 'bg-success',
									i === step && !locked && 'bg-primary'
								)}
								aria-label="Question {i + 1}: {q.question}"
								aria-current={i === step && !locked ? 'step' : undefined}
								disabled={locked || !answers.slice(0, i).every((a) => a?.trim())}
								onclick={() => go(i)}
							></button>
						{/each}
					</div>
				</div>
			</Card.Action>
		</Card.Header>
		<Card.Content>
			<div class="flex flex-col gap-4">
				<!-- The answers so far, each a way back to its question -->
				{#if trail > 0}
					<ol class={cn('flex flex-col', !locked && 'border-hairline border-b pb-3')}>
						{#each questions.slice(0, trail) as q, i (i)}
							<li class="-mx-1.5">
								<button
									type="button"
									class="hover:bg-accent focus-visible:ring-ring grid-cols-answer-row grid w-full items-center gap-2 rounded-md px-1.5 py-1 text-left text-sm outline-none focus-visible:ring-2 disabled:hover:bg-transparent"
									title={locked ? undefined : 'Change this answer'}
									disabled={locked}
									onclick={() => go(i)}
								>
									<CheckIcon class="text-success size-3.5" />
									<span class="text-muted-foreground truncate">{q.question}</span>
									<span class="max-w-answer truncate font-medium">{answers[i]}</span>
								</button>
							</li>
						{/each}
					</ol>
				{/if}

				{#if !locked && question}
					{#key step}
						<fieldset in:stepIn class="flex min-w-0 flex-col gap-3">
							<legend class="mb-3 text-lg leading-snug font-medium text-balance">
								{question.question}
							</legend>
							{#if options.length > 0}
								<div class="flex flex-col gap-1.5">
									{#each options as option, i (option)}
										{@const picked = !custom[step] && answers[step] === option}
										<label
											class={cn(
												'bg-card border-border hover:bg-accent has-focus-visible:ring-ring flex min-h-10 cursor-pointer items-center gap-2.5 rounded-lg border px-2.5 py-1.5 shadow-xs has-focus-visible:ring-2',
												picked &&
													'border-primary bg-info-tint hover:bg-info-tint ring-primary ring-1'
											)}
										>
											<input
												type="radio"
												class="sr-only"
												name="question-{step}"
												value={option}
												checked={picked}
												onchange={() => pick(option)}
											/>
											<kbd
												class={cn(
													'bg-card text-muted-foreground ring-border inline-flex size-4.5 shrink-0 items-center justify-center rounded-sm text-2xs font-mono ring-1',
													picked && 'bg-primary text-primary-foreground ring-primary'
												)}
												aria-hidden="true"
											>
												{i + 1}
											</kbd>
											<span class="flex-1">{option}</span>
											<CheckIcon class={cn('text-primary size-4', !picked && 'invisible')} />
										</label>
									{/each}
									<!-- Typing picks this row, so a custom answer never needs a click on the radio first -->
									<label
										class={cn(
											'bg-card border-border has-focus-visible:ring-ring flex min-h-10 cursor-text items-center gap-2.5 rounded-lg border px-2.5 py-1.5 shadow-xs has-focus-visible:ring-2',
											custom[step] && 'border-primary bg-info-tint ring-primary ring-1',
											error && !custom[step] && 'border-destructive'
										)}
									>
										<input
											type="radio"
											class="sr-only"
											name="question-{step}"
											value=""
											checked={custom[step]}
											tabindex={-1}
											onchange={pickCustom}
											aria-label="Something else"
										/>
										<kbd
											class={cn(
												'bg-card text-muted-foreground ring-border inline-flex size-4.5 shrink-0 items-center justify-center rounded-sm text-2xs font-mono ring-1',
												custom[step] && 'bg-primary text-primary-foreground ring-primary'
											)}
											aria-hidden="true"
										>
											{options.length + 1}
										</kbd>
										<input
											type="text"
											data-step-input={custom[step] ? '' : undefined}
											class="placeholder:text-muted-foreground min-w-0 flex-1 bg-transparent text-base outline-none"
											placeholder="Something else…"
											aria-label="Something else"
											aria-invalid={!!error}
											value={customText[step]}
											onfocus={() => customText[step].trim() && pickCustom()}
											oninput={(event) => typeCustom(event.currentTarget.value)}
										/>
										<CheckIcon
											class={cn(
												'text-primary size-4',
												!(custom[step] && answers[step]?.trim()) && 'invisible'
											)}
										/>
									</label>
								</div>
							{:else}
								<Input
									data-step-input
									aria-label={question.question}
									aria-invalid={!!error}
									bind:value={answers[step]}
									oninput={() => (error = null)}
								/>
							{/if}
							{#if error}<Field.Error>{error}</Field.Error>{/if}
						</fieldset>
					{/key}
				{/if}
			</div>
		</Card.Content>
		<Card.Footer class="flex flex-wrap justify-between">
			<!-- The ghost button's icon lines up with the content's edge, like on the description card -->
			<!-- While the answers compile the only way out is Cancel, as on the description card -->
			{#if !locked}
				<Button variant="ghost" class="-ml-2.5" onclick={back}>
					<ArrowLeftIcon data-icon="inline-start" />
					{step === 0 ? backLabel : 'Previous'}
				</Button>
			{/if}
			<div class="ml-auto">
				{#if locked}
					<Button variant="outline" onclick={oncancel}>Cancel</Button>
				{:else if last}
					<Button type="submit">
						<SparklesIcon data-icon="inline-start" />
						Compile
					</Button>
				{:else}
					<Button type="submit">
						Next
						<ArrowRightIcon data-icon="inline-end" />
					</Button>
				{/if}
			</div>
		</Card.Footer>
	</Card.Root>
</form>
