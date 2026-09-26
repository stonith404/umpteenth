<script lang="ts">
	import DatePicker from '$lib/components/form/date-picker.svelte';
	import * as Field from '$lib/components/ui/field';
	import { Input, type FormInputEvent } from '$lib/components/ui/input';
	import type { FormInput } from '$lib/utils/form-util';
	import { cn } from '$lib/utils/style';
	import type { Snippet } from 'svelte';
	import type { HTMLAttributes } from 'svelte/elements';

	type WithoutChildren = {
		children?: undefined;
		input?: FormInput<any>;
		labelFor?: never;
	};
	type WithChildren = {
		children: Snippet;
		input?: any;
		labelFor?: string;
	};

	let {
		input = $bindable(),
		label,
		description,
		placeholder,
		disabled = false,
		type = 'text',
		children,
		onInput,
		labelFor,
		readonly = false,
		inputClass,
		suffix,
		step,
		min,
		max,
		futureOnly = false,
		class: className,
		...restProps
	}: HTMLAttributes<HTMLDivElement> &
		(WithChildren | WithoutChildren) & {
			label?: string;
			description?: string | Snippet;
			placeholder?: string;
			disabled?: boolean;
			inputClass?: string;
			type?: 'text' | 'password' | 'email' | 'number' | 'date' | 'url';
			readonly?: boolean;
			suffix?: string;
			step?: number | 'any';
			min?: number;
			max?: number;
			futureOnly?: boolean;
			onInput?: (e: FormInputEvent) => void;
		} = $props();

	const id = $props.id();
	const inputId = $derived(labelFor ?? id);
</script>

<!-- The description sits below the control, so labels and inputs line up across a grid row whatever the description or error length -->
<Field.Field
	data-disabled={disabled}
	data-invalid={!!input?.error}
	class={cn('flex flex-col justify-start gap-2', className)}
	{...restProps}
>
	{#if label}
		<Field.Label required={input?.required} for={inputId}>{label}</Field.Label>
	{/if}
	{#if children}
		{@render children()}
	{:else if input}
		{#if type === 'date'}
			<DatePicker
				id={inputId}
				clearable={!input.required}
				{futureOnly}
				invalid={!!input.error}
				bind:value={() => input.value as Date | undefined, (value) => (input.value = value)}
			/>
		{:else}
			<div class="relative">
				<Input
					aria-invalid={!!input.error}
					class={cn(suffix && 'pr-16', type === 'number' && 'numeric', inputClass)}
					id={inputId}
					{placeholder}
					{type}
					{step}
					{min}
					{max}
					bind:value={
						() => input.value as string | number | undefined, (value) => (input.value = value)
					}
					{disabled}
					oninput={(e) => onInput?.(e)}
					{readonly}
				/>
				{#if suffix}
					<span
						class="text-muted-foreground pointer-events-none absolute top-1/2 right-3 -translate-y-1/2 text-xs"
					>
						{suffix}
					</span>
				{/if}
			</div>
		{/if}
	{/if}
	{#if input?.error}
		<Field.Error class="text-start">{input.error}</Field.Error>
	{:else if description}
		<Field.Description>
			{#if typeof description === 'string'}
				{description}
			{:else}
				{@render description()}
			{/if}
		</Field.Description>
	{/if}
</Field.Field>
