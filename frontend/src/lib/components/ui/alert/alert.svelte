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
	import { cn, type WithElementRef } from '#lib/utils/style.js';
	import type { HTMLAttributes } from 'svelte/elements';

	let {
		ref = $bindable(null),
		class: className,
		variant = 'default',
		children,
		...restProps
	}: WithElementRef<HTMLAttributes<HTMLDivElement>> & {
		variant?: AlertVariant;
	} = $props();
</script>

<div
	bind:this={ref}
	data-slot="alert"
	role="alert"
	class={cn(alertVariants({ variant }), className)}
	{...restProps}
>
	{@render children?.()}
</div>
