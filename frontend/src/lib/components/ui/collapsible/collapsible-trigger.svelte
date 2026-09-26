<script lang="ts" module>
	import { type VariantProps, tv } from 'tailwind-variants';

	export const collapsibleTriggerVariants = tv({
		variants: {
			variant: {
				default: '',
				// A small muted text link whose chevron child turns while the content is open
				link: 'text-muted-foreground inline-flex w-fit items-center gap-1 text-xs link-underline [&>svg]:size-3.5 [&>svg]:transition-transform data-[state=open]:[&>svg]:rotate-90'
			}
		},
		defaultVariants: {
			variant: 'default'
		}
	});

	export type CollapsibleTriggerVariant = VariantProps<
		typeof collapsibleTriggerVariants
	>['variant'];
</script>

<script lang="ts">
	import { Collapsible as CollapsiblePrimitive } from 'bits-ui';
	import { cn } from '$lib/utils/style.js';

	let {
		ref = $bindable(null),
		class: className,
		variant = 'default',
		...restProps
	}: CollapsiblePrimitive.TriggerProps & { variant?: CollapsibleTriggerVariant } = $props();
</script>

<CollapsiblePrimitive.Trigger
	bind:ref
	data-slot="collapsible-trigger"
	class={cn(collapsibleTriggerVariants({ variant }), className)}
	{...restProps}
/>
