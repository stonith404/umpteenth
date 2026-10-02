<script lang="ts">
	import { Label as LabelPrimitive } from 'bits-ui';
	import { cn } from '#lib/utils/style.js';

	let {
		ref = $bindable(null),
		class: className,
		children,
		sentence = false,
		...restProps
	}: LabelPrimitive.RootProps & {
		// Sets a label written as a full sentence, such as a confirmation prompt, in regular weight with room between its wrapped lines
		sentence?: boolean;
	} = $props();
</script>

<LabelPrimitive.Root
	bind:ref
	data-slot="label"
	class={cn(
		'gap-2 text-sm leading-none font-medium group-data-[disabled=true]:opacity-50 peer-disabled:opacity-50 flex items-center select-none group-data-[disabled=true]:pointer-events-none peer-disabled:cursor-not-allowed',
		sentence && 'leading-snug font-normal',
		className
	)}
	{...restProps}
>
	{@render children?.()}
</LabelPrimitive.Root>
