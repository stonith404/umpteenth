<script lang="ts">
	import { Switch as SwitchPrimitive } from 'bits-ui';
	import { cn, type WithoutChildrenOrChild } from '$lib/utils/style.js';

	let {
		ref = $bindable(null),
		class: className,
		checked = $bindable(false),
		size = 'default',
		...restProps
	}: WithoutChildrenOrChild<SwitchPrimitive.RootProps> & {
		size?: 'sm' | 'default';
	} = $props();
</script>

<SwitchPrimitive.Root
	bind:ref
	bind:checked
	data-slot="switch"
	data-size={size}
	class={cn(
		// Kumo's switch is a squircle whose thumb fills the full height of the track, not a pill with an inset knob
		'data-checked:bg-primary data-checked:ring-[color-mix(in_oklch,var(--primary),black_10%)] data-unchecked:bg-neutral-200 data-unchecked:ring-neutral-300 dark:data-unchecked:bg-neutral-700 dark:data-unchecked:ring-neutral-600 ring-1 focus-visible:ring-2 focus-visible:ring-ring aria-invalid:ring-destructive shrink-0 rounded-[5px] supports-[corner-shape:squircle]:rounded-[10px] [corner-shape:squircle] data-[size=default]:h-4.5 data-[size=default]:w-9 data-[size=sm]:h-4 data-[size=sm]:w-8 peer group/switch relative inline-flex items-center transition-colors duration-150 ease-out outline-none motion-reduce:transition-none after:absolute after:-inset-x-3 after:-inset-y-2 data-disabled:cursor-not-allowed data-disabled:opacity-50',
		className
	)}
	{...restProps}
>
	<SwitchPrimitive.Thumb
		data-slot="switch-thumb"
		class={cn(
			'bg-card dark:data-checked:bg-blue-300 dark:data-unchecked:bg-neutral-800 rounded-[5px] shadow-[0_0_1px_0.5px_rgb(0_0_0/0.12),0_1px_2px_rgb(0_0_0/0.08)] supports-[corner-shape:squircle]:rounded-[10px] [corner-shape:squircle] dark:shadow-[0_0_1px_0.5px_rgb(255_255_255/0.1),0_1px_2px_rgb(0_0_0/0.3)] group-data-[size=default]/switch:size-4.5 group-data-[size=default]/switch:data-checked:translate-x-4.5 group-data-[size=sm]/switch:size-4 group-data-[size=sm]/switch:data-checked:translate-x-4 data-unchecked:translate-x-0 pointer-events-none block transition-transform duration-150 ease-out motion-reduce:transition-none',
			'rtl:data-[state=checked]:-translate-x-full'
		)}
	/>
</SwitchPrimitive.Root>
