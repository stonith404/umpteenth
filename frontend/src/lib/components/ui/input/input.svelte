<script lang="ts">
	import type { HTMLInputAttributes, HTMLInputTypeAttribute } from 'svelte/elements';
	import { cn, type WithElementRef } from '$lib/utils/style.js';

	type InputType = Exclude<HTMLInputTypeAttribute, 'file'>;

	type Props = WithElementRef<
		Omit<HTMLInputAttributes, 'type'> &
			({ type: 'file'; files?: FileList } | { type?: InputType; files?: undefined })
	> & {
		// Sets the value in the monospace font, for identifiers, keys and code, and 'xs' also shrinks it for long machine values like tokens
		mono?: boolean | 'xs';
		// Leaves room at the end for a unit laid over the field, like the suffix of a form input, and 'short' for a unit of a few characters like '/ 1M'
		suffixed?: boolean | 'short';
		// Leaves room at the start for a symbol laid over the field, like a currency sign
		prefixed?: boolean;
		// Sets the value in tabular figures, for amounts and counts
		numeric?: boolean;
	};

	let {
		ref = $bindable(null),
		value = $bindable(),
		type,
		files = $bindable(),
		class: className,
		mono = false,
		suffixed = false,
		prefixed = false,
		numeric = false,
		'data-slot': dataSlot = 'input',
		...restProps
	}: Props = $props();
</script>

{#if type === 'file'}
	<input
		bind:this={ref}
		data-slot={dataSlot}
		class={cn(
			'bg-card shadow-xs focus-visible:border-foreground/50 focus-visible:ring-foreground/50 aria-invalid:ring-destructive/50 aria-invalid:border-destructive h-9 rounded-lg border border-input px-3 py-1 text-base transition-[color,box-shadow,background-color] file:h-7 file:text-sm file:font-medium focus-visible:ring-[0.5px] file:text-foreground placeholder:text-muted-foreground w-full min-w-0 outline-none file:inline-flex file:border-0 file:bg-transparent disabled:pointer-events-none disabled:cursor-not-allowed disabled:opacity-50',
			mono && 'font-mono',
			mono === 'xs' && 'text-xs',
			suffixed && 'pr-16',
			suffixed === 'short' && 'pr-14',
			prefixed && 'pl-6',
			numeric && 'numeric',
			className
		)}
		type="file"
		bind:files
		bind:value
		{...restProps}
	/>
{:else}
	<input
		bind:this={ref}
		data-slot={dataSlot}
		class={cn(
			'bg-card shadow-xs focus-visible:border-foreground/50 focus-visible:ring-foreground/50 aria-invalid:ring-destructive/50 aria-invalid:border-destructive h-9 rounded-lg border border-input px-3 py-1 text-base transition-[color,box-shadow,background-color] file:h-7 file:text-sm file:font-medium focus-visible:ring-[0.5px] file:text-foreground placeholder:text-muted-foreground w-full min-w-0 outline-none file:inline-flex file:border-0 file:bg-transparent disabled:pointer-events-none disabled:cursor-not-allowed disabled:opacity-50',
			mono && 'font-mono',
			mono === 'xs' && 'text-xs',
			suffixed && 'pr-16',
			suffixed === 'short' && 'pr-14',
			prefixed && 'pl-6',
			numeric && 'numeric',
			className
		)}
		{type}
		bind:value
		{...restProps}
	/>
{/if}
