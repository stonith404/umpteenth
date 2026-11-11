<!--
	Kumo's toast on top of svelte-sonner: a popover surface washed with the outcome's tint, a filled icon and a title in the outcome's colour, and an always visible close button
	Sonner's own look is switched off (unstyled), so the classes below are the whole style, and only its stacking, swiping and timing remain
	Pages with a bar docked at the bottom of the window set --toast-clearance on the root to its height, so toasts rise above it instead of covering it
-->
<script lang="ts">
	import { Toaster as Sonner, type ToasterProps as SonnerProps } from 'svelte-sonner';
	import { mode } from 'mode-watcher';
	import CheckCircleIcon from 'phosphor-svelte/lib/CheckCircle';
	import CircleNotchIcon from 'phosphor-svelte/lib/CircleNotch';
	import InfoIcon from 'phosphor-svelte/lib/Info';
	import WarningIcon from 'phosphor-svelte/lib/Warning';
	import WarningOctagonIcon from 'phosphor-svelte/lib/WarningOctagon';
	import XIcon from 'phosphor-svelte/lib/X';

	let { toastOptions, ...restProps }: SonnerProps = $props();

	// The tint lies over the opaque popover colour as a one-colour gradient, so the whole toast carries its outcome lightly and nothing shows through
	// Success is tinted at 20% and the rest at 50%, like Kumo, whose success tint is the strongest
	// Sonner colours dark-mode descriptions itself, hence the important modifier on the description
	const classes = {
		toast:
			'grid w-[340px] grid-cols-[minmax(0,1fr)] items-start gap-x-2 rounded-[12px] bg-popover bg-linear-to-b p-4 font-sans text-popover-foreground shadow-lg ring-1 ring-border has-[>[data-icon]]:grid-cols-[auto_minmax(0,1fr)]',
		icon: 'mt-0.5 flex size-4',
		content: 'flex min-w-0 flex-col gap-1 pr-4',
		title: 'text-[0.975rem] leading-5 font-medium',
		description: 'text-[0.925rem] leading-5 text-foreground/70!',
		closeButton:
			'absolute top-2 right-2 grid size-5 place-items-center rounded-sm text-muted-foreground outline-none hover:bg-current/15 focus-visible:ring-2 focus-visible:ring-ring',
		// Kumo's toast actions are its default, secondary buttons, set under the text
		actionButton:
			'col-[-2] mt-2 h-7 justify-self-start rounded-md border border-border bg-card px-2 text-sm font-medium text-foreground shadow-xs hover:bg-accent',
		cancelButton:
			'col-[-2] mt-2 h-7 justify-self-start rounded-md px-2 text-sm font-medium text-foreground hover:bg-accent',
		success:
			'from-success-tint/20 to-success-tint/20 [&_[data-close-button]]:text-success-foreground [&>[data-icon]]:text-success-foreground [&_[data-title]]:text-success-foreground',
		error:
			'from-destructive-tint/50 to-destructive-tint/50 [&_[data-close-button]]:text-destructive [&>[data-icon]]:text-destructive [&_[data-title]]:text-destructive',
		warning:
			'from-warning-tint/50 to-warning-tint/50 [&_[data-close-button]]:text-warning-foreground [&>[data-icon]]:text-warning-foreground [&_[data-title]]:text-warning-foreground',
		info: 'from-info-tint/50 to-info-tint/50 [&_[data-close-button]]:text-info-foreground [&>[data-icon]]:text-info-foreground [&_[data-title]]:text-info-foreground'
	};
</script>

<!-- Kumo's corner: 32px from the edges and 340px wide, and on phones 16px from the edges across the whole width -->
<Sonner
	theme={mode.current}
	position="bottom-right"
	gap={12}
	offset={{
		top: '2rem',
		right: '2rem',
		bottom: 'calc(2rem + var(--toast-clearance, 0px))',
		left: '2rem'
	}}
	mobileOffset={{
		top: '1rem',
		right: '1rem',
		bottom: 'calc(1rem + var(--toast-clearance, 0px))',
		left: '1rem'
	}}
	toastOptions={{
		...toastOptions,
		unstyled: true,
		classes: { ...classes, ...toastOptions?.classes }
	}}
	{...restProps}
>
	{#snippet loadingIcon()}
		<CircleNotchIcon class="size-4 animate-spin" />
	{/snippet}
	{#snippet successIcon()}
		<CheckCircleIcon weight="fill" class="size-4" />
	{/snippet}
	{#snippet errorIcon()}
		<WarningOctagonIcon weight="fill" class="size-4" />
	{/snippet}
	{#snippet warningIcon()}
		<WarningIcon weight="fill" class="size-4" />
	{/snippet}
	{#snippet infoIcon()}
		<InfoIcon weight="fill" class="size-4" />
	{/snippet}
	{#snippet closeIcon()}
		<XIcon class="size-3" />
	{/snippet}
</Sonner>

<style>
	/* Kumo's stack: a toast behind the front one peeks out 12px above it and shrinks by a tenth, anchored at its bottom edge */
	/* Its content fades out until hovering spreads the stack, so only the tinted edges show */
	:global([data-sonner-toaster] [data-sonner-toast]) {
		transform-origin: bottom;
	}

	:global([data-sonner-toaster] [data-sonner-toast][data-expanded='false'][data-front='false']) {
		--stack-scale: calc(1 - var(--toasts-before) * 0.1);
		--y: translateY(
				calc(
					var(--toasts-before) * var(--gap) * -1 - (1 - var(--stack-scale)) *
						var(--front-toast-height)
				)
			)
			scale(var(--stack-scale));
	}

	:global(
		[data-sonner-toaster] [data-sonner-toast][data-expanded='false'][data-front='false'] > *
	) {
		opacity: 0;
	}
</style>
