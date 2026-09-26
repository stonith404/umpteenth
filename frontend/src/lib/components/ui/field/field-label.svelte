<script lang="ts">
	import { Label } from '$lib/components/ui/label/index.js';
	import { cn } from '$lib/utils/style.js';
	import type { ComponentProps } from 'svelte';

	let {
		ref = $bindable(null),
		class: className,
		required = false,
		optional = false,
		children,
		...restProps
	}: ComponentProps<typeof Label> & {
		// Renders no marker, like Kumo, where required fields stay unmarked until validation says 'Required', and is only kept as a data attribute
		required?: boolean;
		// Appends a muted '(optional)' after the label, Kumo's marker for fields that may be left empty
		optional?: boolean;
	} = $props();
</script>

<Label
	bind:ref
	data-slot="field-label"
	data-required={required || undefined}
	class={cn(
		'has-data-checked:bg-input/30 gap-2 leading-snug group-data-[disabled=true]/field:opacity-50 has-[>[data-slot=field]]:rounded-2xl has-[>[data-slot=field]]:border *:data-[slot=field]:p-4 group/field-label peer/field-label flex w-fit leading-snug',
		'has-[>[data-slot=field]]:w-full has-[>[data-slot=field]]:flex-col',
		className
	)}
	{...restProps}
>
	{@render children?.()}
	{#if optional}
		<!-- Kumo keeps the marker 4px from the label, half the label's usual gap -->
		<span data-slot="field-label-optional" class="text-muted-foreground -ml-1 font-normal">
			(optional)
		</span>
	{/if}
</Label>
