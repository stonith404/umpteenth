<script lang="ts">
	import { beforeNavigate } from '$app/navigation';
	import { Button } from '$lib/components/ui/button';
	import unsavedChanges from '$lib/stores/unsaved-changes-store.svelte';
	import { cn } from '$lib/utils/style';
	import CircleAlertIcon from '@lucide/svelte/icons/circle-alert';
	import CircleCheckIcon from '@lucide/svelte/icons/circle-check';
	import { tick } from 'svelte';
	import { cubicOut } from 'svelte/easing';

	// A 36px Kumo button row inside a 6px frame, so the bar's 12px corners stay concentric with the buttons' 8px ones
	const ROW_HEIGHT = 48;
	const BOTTOM_OFFSET = 20;
	const VIEWPORT_MARGIN = 16;
	const INSET = 16;
	const INSET_BUTTONS = 6;

	const MOTION =
		'duration-[380ms] ease-[cubic-bezier(0.22,1,0.36,1)] motion-reduce:transition-none';
	const LAYER = 'absolute top-0 flex items-center whitespace-nowrap';

	let shake = $state(false);
	let barWidth = $state<number>();
	let compact = $state(false);
	let frame = $state<HTMLElement>();
	let pendingMessage = $state<HTMLElement>();
	let statusMessage = $state<HTMLElement>();
	let actions = $state<HTMLElement>();

	const status = $derived(unsavedChanges.status);
	const showingStatus = $derived(!!status);
	const showingActions = $derived(unsavedChanges.hasChanges);
	const barOpen = $derived(showingActions || showingStatus);

	// Settled means in view, the other message waits a row height away
	const pendingSettled = $derived(!showingStatus && !compact);
	const statusSettled = $derived(showingStatus && !compact);

	function measure() {
		const message = showingStatus ? statusMessage : pendingMessage;
		if (!message || !actions || !frame) return;

		// offsetWidth ignores the scale the buttons carry while fading, and rounding the text up keeps its last glyph
		const end = showingActions ? actions.offsetWidth + INSET_BUTTONS : INSET;
		const roomy = INSET + Math.ceil(message.getBoundingClientRect().width) + end;

		// An unmeasured frame reports no width, which must not read as "nothing fits"
		const available = frame.clientWidth ? frame.clientWidth - 2 * VIEWPORT_MARGIN : Infinity;

		compact = showingActions && roomy > available;
		barWidth = Math.min(compact ? actions.offsetWidth + 2 * INSET_BUTTONS : roomy, available);
	}

	$effect(measure);

	// Catches what no state change announces: a longer message, a font loading, the viewport resizing
	$effect(() => {
		const layers = [pendingMessage, statusMessage, actions, frame].filter((layer) => !!layer);
		if (layers.length === 0) return;

		const observer = new ResizeObserver(measure);
		for (const layer of layers) observer.observe(layer);

		return () => observer.disconnect();
	});

	function rise(_node: HTMLElement, { duration = 220 } = {}) {
		return {
			duration,
			easing: cubicOut,
			css: (t: number, u: number) =>
				`opacity: ${t}; transform: translateY(${u * 8}px) scale(${0.98 + t * 0.02})`
		};
	}

	beforeNavigate((nav) => {
		if (!unsavedChanges.hasChanges) return;

		// Cancelling a full page unload makes the browser show its own "leave site?" prompt
		nav.cancel();
		if (nav.type === 'leave') return;

		shake = true;
	});

	// A fresh edit supersedes any status message left over from a previous save
	$effect(() => {
		if (unsavedChanges.hasChanges) unsavedChanges.clearStatus();
	});

	function handleSaveShortcut(event: KeyboardEvent) {
		if (event.key.toLowerCase() !== 's' || !(event.metaKey || event.ctrlKey)) return;
		if (!unsavedChanges.hasChanges || unsavedChanges.saving) return;

		event.preventDefault();
		void saveAll();
	}

	async function saveAll() {
		if ((await unsavedChanges.saveAll()) === 'invalid') await revealFirstInvalidField();
	}

	// Brings the first field that failed validation into view, switching to the tab that contains it if necessary
	async function revealFirstInvalidField() {
		await tick();
		const field = document.querySelector<HTMLElement>(
			'[aria-invalid="true"], [data-slot="field-error"]'
		);
		if (!field) return;

		for (
			let panel = field.closest<HTMLElement>('[role="tabpanel"]');
			panel;
			panel = panel.parentElement?.closest<HTMLElement>('[role="tabpanel"]') ?? null
		) {
			const value = panel.dataset.value;
			if (value === undefined) continue;
			panel
				.closest('[data-slot="tabs"]')
				?.querySelector<HTMLElement>(`[role="tab"][data-value="${CSS.escape(value)}"]`)
				?.click();
		}

		await tick();
		field.scrollIntoView({ block: 'center', behavior: 'smooth' });
		field.focus({ preventScroll: true });
	}
</script>

<svelte:window onkeydown={handleSaveShortcut} />

<!-- Belongs to the message, so it leaves with it and never counts towards the buttons' width -->
{#snippet gap()}
	<span class="w-4 shrink-0" aria-hidden="true"></span>
{/snippet}

<!-- A zero-height sticky anchor at the end of the content column, so the bar centers on the page rather than on the window behind the sidebar -->
{#if barOpen}
	<div class="pointer-events-none sticky bottom-0 z-50 h-0">
		<div
			bind:this={frame}
			class={cn('absolute inset-x-0 flex', compact ? 'justify-end' : 'justify-center')}
			style:bottom="{BOTTOM_OFFSET}px"
			style:padding-inline="{VIEWPORT_MARGIN}px"
			transition:rise
			class:animate-shake={shake}
			onanimationend={() => (shake = false)}
		>
			<!-- Kumo's floating control surface, the same one its toasts use: solid, 12px corners, a hairline ring and a soft drop shadow -->
			<div
				role="status"
				aria-live="polite"
				class={cn(
					'bg-popover text-popover-foreground ring-border pointer-events-auto relative isolate max-w-full overflow-hidden rounded-[12px] shadow-lg ring-1',
					`transition-[width] ${MOTION}`
				)}
				style:height="{ROW_HEIGHT}px"
				style:width={barWidth ? `${barWidth}px` : undefined}
			>
				<span
					bind:this={pendingMessage}
					class={cn(LAYER, 'gap-2.5', `transition-[translate] ${MOTION}`)}
					style:height="{ROW_HEIGHT}px"
					style:left="{INSET}px"
					style:translate={pendingSettled ? '0' : `0 -${ROW_HEIGHT}px`}
					inert={!pendingSettled}
				>
					<span class="bg-warning-foreground size-2 shrink-0 rounded-full" aria-hidden="true"
					></span>
					<span class="text-base font-medium">Unsaved changes</span>
					{#if unsavedChanges.dirtyCount > 1}
						<span
							class="numeric flex h-5 min-w-5 items-center justify-center rounded-full bg-neutral-200/80 px-1.5 text-xs font-medium text-neutral-800 dark:bg-neutral-800 dark:text-neutral-200"
							title="{unsavedChanges.dirtyCount} sections have unsaved changes"
						>
							{unsavedChanges.dirtyCount}
						</span>
					{/if}
					{@render gap()}
				</span>

				<span
					bind:this={statusMessage}
					class={cn(
						LAYER,
						'gap-2.5',
						status?.type === 'error' ? 'text-destructive' : 'text-success-foreground',
						`transition-[translate] ${MOTION}`
					)}
					style:height="{ROW_HEIGHT}px"
					style:left="{INSET}px"
					style:translate={statusSettled ? '0' : `0 ${ROW_HEIGHT}px`}
					inert={!statusSettled}
				>
					{#if status?.type === 'error'}
						<CircleAlertIcon class="size-4 shrink-0" />
					{:else if status}
						<CircleCheckIcon class="text-success size-4 shrink-0" />
					{/if}
					<span class="text-base font-medium">{status?.message ?? ''}</span>
					{#if showingActions}
						{@render gap()}
					{/if}
				</span>

				<span
					bind:this={actions}
					class={cn(
						LAYER,
						'gap-2',
						`transition-[opacity,scale] ${MOTION}`,
						!showingActions && 'scale-[0.96] opacity-0'
					)}
					style:height="{ROW_HEIGHT}px"
					style:right="{INSET_BUTTONS}px"
					inert={!showingActions}
				>
					<Button
						variant="outline"
						disabled={unsavedChanges.saving}
						onclick={() => unsavedChanges.discardAll()}
					>
						Discard
					</Button>
					<Button isLoading={unsavedChanges.saving} onclick={saveAll}>Save</Button>
				</span>
			</div>
		</div>
	</div>
{/if}
