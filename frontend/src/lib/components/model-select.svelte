<script lang="ts">
	import type { Model } from '$lib/api/types';
	import * as Select from '$lib/components/ui/select';

	let {
		models,
		value = $bindable(''),
		id,
		noneLabel = 'Not set',
		invalid = false,
		disabled = false,
		includeUnavailable = false
	}: {
		models: Model[];
		// The selected model's ID, or an empty string for none
		value?: string;
		id?: string;
		// What choosing no model means here, e.g. "Workspace default (Claude Sonnet)"
		noneLabel?: string;
		invalid?: boolean;
		disabled?: boolean;
		// Also offers disabled and unlisted models, e.g. to test one before enabling it
		includeUnavailable?: boolean;
	} = $props();

	// bits-ui treats an empty value as "nothing selected", so "none" gets a sentinel of its own
	const NONE = '__none__';

	const offered = $derived(includeUnavailable ? models : models.filter(isAvailable));

	// Models grouped by provider, in the order the API sorted them
	const groups = $derived([...Map.groupBy(offered, (model) => model.providerName)]);

	const selected = $derived(models.find((m) => m.id === value));

	// A model that was deleted since it was picked stays visible, so saving doesn't silently change it
	const missing = $derived(value !== '' && !selected);

	function isAvailable(model: Model) {
		return model.enabled && model.unlistedAt === null;
	}

	function modelLabel(model: Model) {
		return model.label || model.model;
	}

	// Why a model that is still picked can't be picked again, shown next to its name
	function unavailableReason(model: Model) {
		if (!model.enabled) return 'disabled';
		if (model.unlistedAt !== null) return 'no longer listed';
		return '';
	}
</script>

<Select.Root
	type="single"
	value={value || NONE}
	onValueChange={(next) => (value = next === NONE ? '' : next)}
	{disabled}
>
	<Select.Trigger {id} aria-invalid={invalid} class="w-full">
		{#if selected}
			<span class="truncate">
				{modelLabel(selected)}
				<span class="text-muted-foreground">· {selected.providerName}</span>
				{#if unavailableReason(selected)}
					<span class="text-muted-foreground">· {unavailableReason(selected)}</span>
				{/if}
			</span>
		{:else if missing}
			<span class="text-muted-foreground truncate">Unknown model</span>
		{:else}
			<span class="text-muted-foreground truncate">{noneLabel}</span>
		{/if}
	</Select.Trigger>
	<Select.Content align="start" class="max-h-80">
		<Select.Item value={NONE} label={noneLabel} />
		{#each groups as [provider, providerModels] (provider)}
			<Select.Group>
				<Select.GroupHeading>{provider}</Select.GroupHeading>
				{#each providerModels as model (model.id)}
					<Select.Item value={model.id} label={modelLabel(model)}>
						<span class="flex flex-col">
							<span>{modelLabel(model)}</span>
							{#if model.label}
								<span class="text-muted-foreground font-mono text-xs font-normal">
									{model.model}
								</span>
							{/if}
						</span>
					</Select.Item>
				{/each}
			</Select.Group>
		{/each}
	</Select.Content>
</Select.Root>
