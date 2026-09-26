<script lang="ts" module>
	export type NetworkChoice = 'internet' | 'allowlist' | 'none' | 'unrestricted';

	// One wording for the sandbox's network everywhere it is picked or summarized
	export const networkOptions: { value: NetworkChoice; label: string; description: string }[] = [
		{
			value: 'internet',
			label: 'Internet access',
			description: 'The sandbox reaches any public address through the egress proxy.'
		},
		{
			value: 'allowlist',
			label: 'Allowed domains only',
			description: 'The sandbox reaches only the domains you list.'
		},
		{
			value: 'none',
			label: 'No network',
			description: 'The sandbox is offline. MCP servers over HTTP still work.'
		},
		{
			value: 'unrestricted',
			label: 'Unrestricted network',
			description:
				'No network sandboxing. The sandbox reaches everything the host can, the LAN and cloud metadata included.'
		}
	];

	export function networkLabel(value: string) {
		return networkOptions.find((option) => option.value === value)?.label ?? value;
	}
</script>

<script lang="ts" generics="T extends NetworkChoice">
	import * as Select from '$lib/components/ui/select';
	import { cn } from '$lib/utils/style';

	let {
		value = $bindable(),
		choices,
		id,
		class: className
	}: {
		value: T;
		// The policies offered here, e.g. a new job's spec only knows internet and none
		choices: readonly T[];
		id?: string;
		class?: string;
	} = $props();

	const offered = $derived(networkOptions.filter((option) => choices.includes(option.value as T)));
</script>

<Select.Root type="single" bind:value={() => value, (next) => (value = next as T)}>
	<!-- A short choice needs no more than a short control, and max-width survives the field's full-width rule for its children -->
	<Select.Trigger {id} class={cn('w-full sm:max-w-72', className)}>
		{networkLabel(value)}
	</Select.Trigger>
	<!-- The options and their descriptions are wider than the capped trigger, so the list opens from its left edge instead of centered on it -->
	<Select.Content align="start">
		{#each offered as option (option.value)}
			<Select.Item value={option.value} label={option.label}>
				<span class="flex flex-col">
					<span>{option.label}</span>
					<span class="text-muted-foreground text-xs font-normal whitespace-normal">
						{option.description}
					</span>
				</span>
			</Select.Item>
		{/each}
	</Select.Content>
</Select.Root>
