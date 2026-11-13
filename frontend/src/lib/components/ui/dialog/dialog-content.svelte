<script lang="ts">
	import { Dialog as DialogPrimitive } from 'bits-ui';
	import DialogPortal from './dialog-portal.svelte';
	import type { Snippet } from 'svelte';
	import * as Dialog from './index.js';
	import { cn, type WithoutChildrenOrChild } from '#lib/utils/style.js';
	import type { ComponentProps } from 'svelte';
	import XIcon from 'phosphor-svelte/lib/X';

	let {
		ref = $bindable(null),
		class: className,
		portalProps,
		children,
		showCloseButton = true,
		variant = 'default',
		...restProps
	}: WithoutChildrenOrChild<DialogPrimitive.ContentProps> & {
		portalProps?: WithoutChildrenOrChild<ComponentProps<typeof DialogPortal>>;
		children: Snippet;
		showCloseButton?: boolean;
		// 'palette' is Kumo's command palette: flush to the edges for its own search row and footer, and higher up the screen
		variant?: 'default' | 'palette';
	} = $props();
</script>

<DialogPortal {...portalProps}>
	<Dialog.Overlay />
	<!-- A flex column scrolls as a whole by default, but a body marked min-h-0 overflow-y-auto shrinks and scrolls on its own so the header and footer stay put -->
	<DialogPrimitive.Content
		bind:ref
		data-slot="dialog-content"
		class={cn(
			'bg-card text-card-foreground data-open:animate-in data-closed:animate-out data-closed:fade-out-0 data-open:fade-in-0 data-closed:zoom-out-95 data-open:zoom-in-95 ring-border fixed top-8 left-1/2 z-50 flex max-h-[calc(100dvh-4rem)] w-full max-w-[calc(100%-2rem)] -translate-x-1/2 flex-col gap-6 overflow-y-auto rounded-[12px] p-6 text-base shadow-[0_20px_25px_-5px_rgb(0_0_0/0.03),0_8px_10px_-6px_rgb(0_0_0/0.03)] ring-1 duration-150 outline-none sm:top-16 sm:max-h-[calc(100dvh-8rem)] sm:max-w-md',
			variant === 'palette' && 'top-[10vh] gap-0 p-0 sm:top-[10vh]',
			className
		)}
		{...restProps}
	>
		{@render children?.()}
		{#if showCloseButton}
			<DialogPrimitive.Close data-slot="dialog-close">
				{#snippet child({ props })}
					<!-- Kumo's close is a bare 24px icon in the corner, in line with the 20px title, that only darkens on hover -->
					<button
						{...props}
						class="text-muted-foreground hover:text-foreground focus-visible:ring-ring absolute top-6.5 right-6 inline-flex size-6 items-center justify-center rounded outline-none focus-visible:ring-2"
					>
						<XIcon class="size-4" />
						<span class="sr-only">Close</span>
					</button>
				{/snippet}
			</DialogPrimitive.Close>
		{/if}
	</DialogPrimitive.Content>
</DialogPortal>
