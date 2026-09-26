<script lang="ts">
	import { Button } from '$lib/components/ui/button';
	import * as Card from '$lib/components/ui/card';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Spinner } from '$lib/components/ui/spinner';
	import { Clock } from '$lib/utils/clock.svelte';
	import { cn } from '$lib/utils/style';
	import CheckIcon from '@lucide/svelte/icons/check';
	import SparklesIcon from '@lucide/svelte/icons/sparkles';

	let { startedAt, onCancel }: { startedAt: number; onCancel: () => void } = $props();

	// Compiling takes 10-60 seconds, so the steps advance on a timer to show that work is happening
	// They describe what the utility model produces, the backend reports no real progress
	const steps = [
		{ label: 'Reading your description', at: 0 },
		{ label: 'Working out the schedule', at: 3 },
		{ label: 'Writing success criteria', at: 7 },
		{ label: 'Matching MCP servers', at: 12 },
		{ label: 'Checking the environment', at: 18 },
		{ label: 'Reviewing side effects and warnings', at: 26 }
	];

	const clock = new Clock(250);
	const elapsed = $derived(Math.max(0, Math.floor((clock.now - startedAt) / 1000)));
	const current = $derived(steps.findLastIndex((step) => elapsed >= step.at));
</script>

<div class="flex flex-col gap-6" aria-live="polite" aria-busy="true">
	<Card.Root class="relative overflow-hidden">
		<div
			aria-hidden="true"
			class="pointer-events-none absolute inset-x-0 top-0 h-1 overflow-hidden bg-violet-500/10"
		>
			<div class="compile-bar h-full w-1/3 rounded-full bg-violet-500/70"></div>
		</div>
		<Card.Header>
			<Card.Title class="flex items-center gap-2">
				<SparklesIcon class="size-4 text-violet-500" />
				Compiling your job
			</Card.Title>
			<Card.Description>
				The utility model turns your description into a spec you can review. This usually takes 10
				to 60 seconds.
			</Card.Description>
			<Card.Action>
				<span class="text-muted-foreground numeric text-sm">{elapsed}s</span>
			</Card.Action>
		</Card.Header>
		<Card.Content>
			<ol class="flex flex-col gap-2.5">
				{#each steps as step, i (step.label)}
					<li
						class={cn(
							'flex items-center gap-3 text-sm transition-opacity duration-500',
							i > current && 'opacity-40'
						)}
					>
						<span class="flex size-5 items-center justify-center">
							{#if i < current}
								<CheckIcon class="size-4 text-emerald-600 dark:text-emerald-400" />
							{:else if i === current}
								<Spinner class="size-4" />
							{:else}
								<span class="bg-muted-foreground/40 size-1.5 rounded-full"></span>
							{/if}
						</span>
						{step.label}
					</li>
				{/each}
			</ol>
		</Card.Content>
		<Card.Footer>
			<Button variant="outline" onclick={onCancel}>Cancel</Button>
		</Card.Footer>
	</Card.Root>

	<div class="grid gap-6 md:grid-cols-2" aria-hidden="true">
		{#each [0, 1] as i (i)}
			<Card.Root>
				<Card.Header>
					<Skeleton class="h-5 w-40" />
					<Skeleton class="h-4 w-64" />
				</Card.Header>
				<Card.Content class="flex flex-col gap-3">
					<Skeleton class="h-9 w-full" />
					<Skeleton class="h-9 w-4/5" />
					<Skeleton class="h-9 w-3/5" />
				</Card.Content>
			</Card.Root>
		{/each}
	</div>
</div>

<style>
	.compile-bar {
		animation: compile-slide 1.6s ease-in-out infinite;
	}

	@keyframes compile-slide {
		0% {
			transform: translateX(-100%);
		}
		100% {
			transform: translateX(300%);
		}
	}

	@media (prefers-reduced-motion: reduce) {
		.compile-bar {
			animation: none;
		}
	}
</style>
