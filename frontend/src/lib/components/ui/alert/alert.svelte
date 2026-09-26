<script lang="ts" module>
	import { tv, type VariantProps } from 'tailwind-variants';

	// Kumo's banner: a flat tinted block whose text and icon take the hue of the message
	export const alertVariants = tv({
		base: "grid gap-0.5 rounded-lg px-4 py-3 text-left text-base has-data-[slot=alert-action]:relative has-data-[slot=alert-action]:pr-18 has-[>svg]:grid-cols-[auto_1fr] has-[>svg]:gap-x-3 *:[svg]:row-span-2 *:[svg]:translate-y-0.5 *:[svg]:text-current *:[svg:not([class*='size-'])]:size-4 group/alert relative w-full",
		variants: {
			variant: {
				default: 'bg-foreground/5 text-foreground/70',
				destructive: 'bg-destructive-tint text-destructive',
				success: 'bg-success-tint text-success-foreground',
				info: 'bg-info-tint text-info-foreground',
				warning: 'bg-warning-tint text-warning-foreground'
			}
		},
		defaultVariants: {
			variant: 'default'
		}
	});

	export type AlertVariant = VariantProps<typeof alertVariants>['variant'];
</script>

<script lang="ts">
	import { cn, type WithElementRef } from '$lib/utils/style.js';
	import { LucideX } from '@lucide/svelte';
	import { onMount } from 'svelte';
	import type { HTMLAttributes } from 'svelte/elements';

	let {
		ref = $bindable(null),
		class: className,
		variant = 'default',
		children,
		onDismiss,
		dismissibleId = undefined,
		...restProps
	}: WithElementRef<HTMLAttributes<HTMLDivElement>> & {
		variant?: AlertVariant;
		onDismiss?: () => void;
		dismissibleId?: string;
	} = $props();

	let isVisible = $state(!dismissibleId);

	onMount(() => {
		if (dismissibleId) {
			const dismissedAlerts = JSON.parse(localStorage.getItem('dismissed-alerts') || '[]');
			isVisible = !dismissedAlerts.includes(dismissibleId);
		}
	});

	function dismiss() {
		onDismiss?.();
		if (dismissibleId) {
			const dismissedAlerts = JSON.parse(localStorage.getItem('dismissed-alerts') || '[]');
			localStorage.setItem('dismissed-alerts', JSON.stringify([...dismissedAlerts, dismissibleId]));
			isVisible = false;
		}
	}
</script>

{#if isVisible}
	<div
		bind:this={ref}
		data-slot="alert"
		role="alert"
		class={cn(alertVariants({ variant }), className)}
		{...restProps}
	>
		{@render children?.()}
		{#if dismissibleId || onDismiss}
			<button onclick={dismiss} class="absolute top-2.5 right-3 text-current">
				<LucideX class="size-4" />
			</button>
		{/if}
	</div>
{/if}
