<script lang="ts">
	import { isApiError } from '#lib/api/api-error.js';
	import type { Job, JobSuggestion } from '#lib/api/types.js';
	import RelativeTime from '#lib/components/relative-time.svelte';
	import { Button } from '#lib/components/ui/button/index.js';
	import * as Card from '#lib/components/ui/card/index.js';
	import JobService from '#lib/services/job-service.js';
	import { apiErrorToast } from '#lib/utils/error-util.js';
	import { LIMIT_FIELDS } from '#lib/utils/job-util.js';
	import { tryCatch } from '#lib/utils/try-catch-util.js';
	import { invalidateAfterNavigation } from '#lib/utils/workspace-events.js';
	import CircleQuestionMarkIcon from '@lucide/svelte/icons/circle-question-mark';
	import GaugeIcon from '@lucide/svelte/icons/gauge';
	import KeyRoundIcon from '@lucide/svelte/icons/key-round';
	import type { Component } from 'svelte';
	import { toast } from 'svelte-sonner';

	let { job, suggestions }: { job: Job; suggestions: JobSuggestion[] } = $props();

	const jobService = new JobService();

	const kinds: Record<JobSuggestion['kind'], { label: string; icon: Component }> = {
		limits: { label: 'New limits', icon: GaugeIcon },
		credentials: { label: 'Credentials', icon: KeyRoundIcon },
		instruction: { label: 'Instruction', icon: CircleQuestionMarkIcon }
	};

	// The suggestion whose request is running, so only its button spins and the others wait
	let busy = $state<{ id: string; action: 'apply' | 'dismiss' } | null>(null);

	// The limits a limits suggestion changes, as the settings label them, e.g. `Memory 1024 MB → 4096 MB`
	// The before side is the job's own limit now, and a limit someone changed by hand since the suggestion was raised is kept, like Apply on the server does
	// Spend is never suggested, so the cost limit is left out
	function limitRows(s: JobSuggestion) {
		const unit = (suffix: string | undefined, value: number) =>
			suffix ? `${value} ${suffix}` : String(value);
		return LIMIT_FIELDS.filter((f) => f.key !== 'maxCostUsd').flatMap((f) => {
			const key = f.key as keyof NonNullable<JobSuggestion['limits']>;
			const after = s.limits?.[key];
			if (after === undefined) return [];
			const suffix = 'suffix' in f ? f.suffix : undefined;
			const seen = s.limitsBefore?.[key];
			const own = job.limits[key];
			const before = own ?? seen;
			return [
				{
					key,
					label: f.label,
					before: before === undefined ? null : unit(suffix, before),
					after: unit(suffix, after),
					kept: own !== undefined && seen !== undefined && own !== seen
				}
			];
		});
	}

	// Applying a limits suggestion whose every limit was changed by hand since leaves the job as it is and only closes it
	function keepsEveryLimit(s: JobSuggestion) {
		const rows = limitRows(s);
		return rows.length > 0 && rows.every((row) => row.kept);
	}

	// Applying sets the limits of a limits suggestion, and for the other kinds says the owner made the change by hand
	async function apply(s: JobSuggestion) {
		const changesLimits = s.kind === 'limits' && !keepsEveryLimit(s);
		busy = { id: s.id, action: 'apply' };
		try {
			const result = await tryCatch(jobService.applySuggestion(job.id, s.id));
			if (result.error) {
				apiErrorToast(result.error, 'The suggestion could not be applied');
				// Someone resolved it meanwhile and no event reports that, so the list is reloaded to drop the stale row
				if (isApiError(result.error, 'conflict', 'not_found'))
					await invalidateAfterNavigation('app:job');
				return;
			}
			toast.success(changesLimits ? 'Applied the new limits' : 'Marked the suggestion as done');
			// The buttons stay disabled until the list no longer shows the resolved suggestion
			await invalidateAfterNavigation('app:job');
		} finally {
			busy = null;
		}
	}

	async function dismiss(s: JobSuggestion) {
		busy = { id: s.id, action: 'dismiss' };
		try {
			const result = await tryCatch(jobService.dismissSuggestion(job.id, s.id));
			if (result.error) {
				apiErrorToast(result.error, 'The suggestion could not be dismissed');
				// Someone resolved it meanwhile and no event reports that, so the list is reloaded to drop the stale row
				if (isApiError(result.error, 'conflict', 'not_found'))
					await invalidateAfterNavigation('app:job');
				return;
			}
			toast.success('Dismissed the suggestion');
			// The buttons stay disabled until the list no longer shows the dismissed suggestion
			await invalidateAfterNavigation('app:job');
		} finally {
			busy = null;
		}
	}
</script>

<section
	id="suggestions"
	class="flex scroll-mt-6 flex-col gap-4"
	aria-labelledby="suggestions-heading"
>
	<div class="flex flex-col gap-1">
		<h2 id="suggestions-heading" class="text-lg font-semibold">Suggestions</h2>
		<p class="text-muted-foreground text-sm">
			Reflection found changes only you can make. Nothing changes until you apply one, and the job
			follows its instruction as written meanwhile.
		</p>
	</div>

	<Card.Root>
		<Card.Content>
			<ul class="divide-y">
				{#each suggestions as s (s.id)}
					{@const kind = kinds[s.kind]}
					{@const fromRun = s.runId !== null && s.runNumber !== null}
					<li class="py-4 first:pt-0 last:pb-0">
						<article
							class="flex flex-col gap-3 sm:flex-row sm:items-start"
							aria-labelledby="suggestion-{s.id}"
						>
							<div class="flex min-w-0 flex-1 items-start gap-3">
								<span
									class="bg-muted text-muted-foreground grid size-6 shrink-0 place-items-center rounded-md"
									aria-hidden="true"
								>
									<kind.icon class="size-3.5" />
								</span>
								<div class="flex min-w-0 flex-1 flex-col gap-1.5">
									<h3 id="suggestion-{s.id}" class="text-sm font-medium">
										{kind.label}
									</h3>
									<!-- The model wrote the text from run data, so it stays plain text and is never rendered as markdown -->
									<p class="text-sm break-words">{s.text}</p>
									<!-- A scripted job's main script doesn't read the instruction, so the owner learns how an edit reaches it -->
									{#if s.kind === 'instruction' && job.nextMode === 'scripted' && job.selfImprove}
										<p class="text-muted-foreground text-xs">
											This job runs as a script, so after you edit the instruction, reflection on
											the next run updates the main script to match.
										</p>
									{/if}
									{#if s.kind === 'limits'}
										<ul class="numeric flex flex-col gap-0.5 text-sm">
											{#each limitRows(s) as row (row.key)}
												<li>
													<span class="text-muted-foreground">{row.label}</span>
													{#if row.kept}
														<span class="font-medium">{row.before}</span>
														<span class="text-muted-foreground"
															>· changed since, Apply keeps it</span
														>
													{:else}
														{#if row.before}
															{row.before} →
														{/if}
														<span class="font-medium">{row.after}</span>
													{/if}
												</li>
											{/each}
										</ul>
									{/if}
									<!-- Reads as one line such as `From run #4 · raised by 3 runs · 2 hours ago`, wrapping between its phrases -->
									<p class="text-muted-foreground flex flex-wrap gap-x-1 text-xs">
										{#if fromRun}
											<span
												>From <a href="/runs/{s.runId}" class="text-foreground link-underline"
													>run #{s.runNumber}</a
												></span
											>
										{/if}
										{#if s.seen > 1}
											<span
												>{#if fromRun}·{/if}
												{fromRun ? 'raised' : 'Raised'} by {s.seen} runs</span
											>
										{/if}
										<span
											>{#if fromRun || s.seen > 1}·{/if}
											<RelativeTime value={s.updatedAt} /></span
										>
									</p>
								</div>
							</div>

							<!-- Actions sit to the right from small screens up, and wrap under the text on phones -->
							<div class="flex flex-wrap gap-2 pl-9 sm:shrink-0 sm:justify-end sm:pl-0">
								{#if s.kind === 'limits'}
									<Button
										size="sm"
										isLoading={busy?.id === s.id && busy.action === 'apply'}
										disabled={busy !== null}
										onclick={() => apply(s)}
									>
										{keepsEveryLimit(s) ? 'Mark as done' : 'Apply'}
									</Button>
								{:else}
									<Button
										size="sm"
										variant="outline"
										href={s.kind === 'instruction'
											? `/jobs/${job.id}/settings#job-instruction`
											: `/jobs/${job.id}/settings`}
									>
										{s.kind === 'instruction' ? 'Edit instruction' : 'Open settings'}
									</Button>
									<Button
										size="sm"
										variant="outline"
										isLoading={busy?.id === s.id && busy.action === 'apply'}
										disabled={busy !== null}
										onclick={() => apply(s)}
									>
										Mark as done
									</Button>
								{/if}
								<Button
									size="sm"
									variant="ghost-muted"
									isLoading={busy?.id === s.id && busy.action === 'dismiss'}
									disabled={busy !== null}
									onclick={() => dismiss(s)}
								>
									Dismiss
								</Button>
							</div>
						</article>
					</li>
				{/each}
			</ul>
		</Card.Content>
	</Card.Root>
</section>
