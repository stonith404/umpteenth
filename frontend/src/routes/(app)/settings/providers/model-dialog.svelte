<script lang="ts">
	import { isApiError } from '$lib/api/api-error';
	import type { CatalogModel, Model, ModelCaps, Provider } from '$lib/api/types';
	import { Button } from '$lib/components/ui/button';
	import { Checkbox } from '$lib/components/ui/checkbox';
	import * as Dialog from '$lib/components/ui/dialog';
	import * as Field from '$lib/components/ui/field';
	import { Input } from '$lib/components/ui/input';
	import * as Select from '$lib/components/ui/select';
	import { Switch } from '$lib/components/ui/switch';
	import ProviderService from '$lib/services/provider-service';
	import { apiErrorToast, getErrorMessage } from '$lib/utils/error-util';
	import { preventDefault } from '$lib/utils/event-util';
	import { formatPricePerMillion, formatTokens } from '$lib/utils/format-util';
	import { tryCatch } from '$lib/utils/try-catch-util';
	import { untrack } from 'svelte';
	import { capabilityLabels, type Capability } from './provider-meta';

	let {
		open = $bindable(false),
		model,
		providers,
		defaultProviderId,
		onSaved
	}: {
		open?: boolean;
		// The model to edit, or null to add one
		model: Model | null;
		providers: Provider[];
		defaultProviderId?: string;
		onSaved: (name: string, created: boolean) => void;
	} = $props();

	const MICROS = 1_000_000;
	const DEFAULT_CONTEXT = 128_000;

	const providerService = new ProviderService();

	// Prices are entered in dollars per 1M tokens and stored as integer micro-USD per 1M tokens
	type Prices = {
		in: number | null;
		out: number | null;
		cacheRead: number | null;
		cacheWrite: number | null;
	};

	let providerId = $state('');
	let modelName = $state('');
	let label = $state('');
	let prices = $state<Prices>({ in: null, out: null, cacheRead: null, cacheWrite: null });
	let context = $state<number | null>(DEFAULT_CONTEXT);
	let caps = $state<Record<Capability, boolean>>(emptyCaps());
	let followCatalog = $state(false);
	let catalog = $state<CatalogModel[]>([]);
	let errors = $state<Record<string, string>>({});
	let isLoading = $state(false);

	const provider = $derived(providers.find((p) => p.id === providerId));

	// A model its catalog lists can take the catalog's metadata on every refresh instead of values set here
	const catalogEntry = $derived(
		model && model.synced && provider?.modelSource === 'catalog'
			? catalog.find((c) => c.model === model.model)
			: undefined
	);
	const locked = $derived(!!catalogEntry && followCatalog);

	function emptyCaps(): Record<Capability, boolean> {
		return {
			tools: true,
			parallelTools: false,
			reasoning: false,
			jsonSchema: false,
			promptCache: false,
			vision: false
		};
	}

	$effect(() => {
		if (!open) return;
		untrack(() => {
			errors = {};
			providerId = model?.providerId ?? defaultProviderId ?? providers[0]?.id ?? '';
			modelName = model?.model ?? '';
			label = model?.label ?? '';
			prices = model
				? {
						in: model.price.in / MICROS,
						out: model.price.out / MICROS,
						cacheRead: model.price.cacheRead / MICROS,
						cacheWrite: model.price.cacheWrite / MICROS
					}
				: { in: null, out: null, cacheRead: null, cacheWrite: null };
			context = model ? model.caps.context || model.contextWindow : DEFAULT_CONTEXT;
			caps = model ? pickCaps(model.caps) : emptyCaps();
			followCatalog = model?.followCatalog ?? false;
		});
	});

	// Turning catalog following back on shows the values the model will take
	function onFollowChange(checked: boolean) {
		followCatalog = checked;
		if (checked && catalogEntry) applyCatalog(catalogEntry.model);
	}

	// The catalog of the chosen provider's kind prefills new models, e.g. with list prices
	$effect(() => {
		const kind = provider?.kind;
		catalog = [];
		if (kind !== 'anthropic' && kind !== 'openai') return;
		void tryCatch(providerService.catalog(kind)).then((result) => {
			if (provider?.kind === kind) catalog = result.data ?? [];
		});
	});

	function pickCaps(source: ModelCaps): Record<Capability, boolean> {
		return {
			tools: source.tools,
			parallelTools: source.parallelTools,
			reasoning: source.reasoning,
			jsonSchema: source.jsonSchema,
			promptCache: source.promptCache,
			vision: source.vision
		};
	}

	function applyCatalog(name: string) {
		const entry = catalog.find((c) => c.model === name);
		if (!entry) return;
		modelName = entry.model;
		label = entry.label;
		prices = {
			in: entry.price.in / MICROS,
			out: entry.price.out / MICROS,
			cacheRead: entry.price.cacheRead / MICROS,
			cacheWrite: entry.price.cacheWrite / MICROS
		};
		context = entry.caps.context;
		caps = pickCaps(entry.caps);
	}

	function toMicros(value: number | null) {
		return value === null || Number.isNaN(value) ? 0 : Math.round(value * MICROS);
	}

	function validate() {
		const next: Record<string, string> = {};
		if (!model && !providerId) next.provider = 'Pick a provider';
		if (!model && !modelName.trim()) next.model = 'Required';
		for (const [key, value] of Object.entries(prices)) {
			if (value !== null && (Number.isNaN(value) || value < 0)) next[key] = 'Must be 0 or more';
		}
		if (!context || context < 1) next.context = 'Must be at least 1';
		errors = next;
		return Object.keys(next).length === 0;
	}

	async function onSubmit() {
		if (!validate()) return;
		isLoading = true;
		const body = {
			price: {
				in: toMicros(prices.in),
				out: toMicros(prices.out),
				cacheRead: toMicros(prices.cacheRead),
				cacheWrite: toMicros(prices.cacheWrite)
			},
			caps: { ...caps, context: Math.round(context ?? DEFAULT_CONTEXT) }
		};

		// Saving values of its own stops a model from following the catalog, and an empty label clears it
		let request: Promise<unknown>;
		if (model && locked) {
			request = providerService.updateModel(model.id, { followCatalog: true });
		} else if (model) {
			request = providerService.updateModel(model.id, { ...body, label: label.trim() });
		} else {
			request = providerService.createModel({
				...body,
				label: label.trim() || undefined,
				providerId,
				model: modelName.trim()
			});
		}
		const result = await tryCatch(request);
		isLoading = false;
		if (result.error) {
			if (isApiError(result.error, 'validation_failed', 'invalid_field', 'already_in_use')) {
				errors = { model: getErrorMessage(result.error) };
			}
			apiErrorToast(result.error, 'Failed to save the model');
			return;
		}
		open = false;
		onSaved(label.trim() || modelName.trim(), !model);
	}
</script>

{#snippet priceInput(key: keyof Prices, text: string)}
	<Field.Field data-invalid={!!errors[key]}>
		<Field.Label for="model-price-{key}">{text}</Field.Label>
		<div class="relative">
			<span
				class="text-muted-foreground pointer-events-none absolute top-1/2 left-3 -translate-y-1/2 text-sm"
				>$</span
			>
			<Input
				id="model-price-{key}"
				type="number"
				step="any"
				min="0"
				placeholder="0"
				class="numeric pr-14 pl-6"
				bind:value={prices[key]}
				disabled={locked}
				aria-invalid={!!errors[key]}
			/>
			<span
				class="text-muted-foreground pointer-events-none absolute top-1/2 right-3 -translate-y-1/2 text-xs"
				>/ 1M</span
			>
		</div>
		{#if errors[key]}<Field.Error>{errors[key]}</Field.Error>{/if}
	</Field.Field>
{/snippet}

<Dialog.Root bind:open>
	<Dialog.Content class="sm:max-w-xl">
		<Dialog.Header>
			<Dialog.Title>{model ? `Edit ${model.label || model.model}` : 'Add model'}</Dialog.Title>
			<Dialog.Description>
				Prices are per 1M tokens and turn token usage into run costs.
			</Dialog.Description>
		</Dialog.Header>
		<form
			id="model-form"
			class="-mx-1 min-h-0 overflow-y-auto px-1"
			onsubmit={preventDefault(onSubmit)}
		>
			<Field.Group>
				{#if !model}
					<div class="grid gap-x-6 gap-y-5 sm:grid-cols-2">
						<Field.Field data-invalid={!!errors.provider}>
							<Field.Label for="model-provider" required>Provider</Field.Label>
							<Select.Root type="single" bind:value={providerId}>
								<Select.Trigger id="model-provider" class="w-full" aria-invalid={!!errors.provider}>
									{provider?.name ?? 'Pick a provider'}
								</Select.Trigger>
								<Select.Content>
									{#each providers as p (p.id)}
										<Select.Item value={p.id} label={p.name} />
									{/each}
								</Select.Content>
							</Select.Root>
							{#if errors.provider}<Field.Error>{errors.provider}</Field.Error>{/if}
						</Field.Field>
						<Field.Field>
							<Field.Label for="model-catalog">Prefill from catalog</Field.Label>
							<Select.Root
								type="single"
								value=""
								onValueChange={applyCatalog}
								disabled={catalog.length === 0}
							>
								<Select.Trigger id="model-catalog" class="w-full">
									<span class="text-muted-foreground">
										{catalog.length > 0 ? 'Pick a known model' : 'No catalog for this provider'}
									</span>
								</Select.Trigger>
								<Select.Content class="max-h-80">
									{#each catalog as entry (entry.model)}
										<Select.Item value={entry.model} label={entry.label}>
											<span class="flex flex-col">
												<span>{entry.label}</span>
												<span class="text-muted-foreground text-xs font-normal">
													{formatPricePerMillion(entry.price.in)} in · {formatPricePerMillion(
														entry.price.out
													)} out · {formatTokens(entry.caps.context)} context
												</span>
											</span>
										</Select.Item>
									{/each}
								</Select.Content>
							</Select.Root>
						</Field.Field>
					</div>
				{/if}
				{#if catalogEntry}
					<Field.Field orientation="horizontal">
						<Switch
							id="model-follow-catalog"
							checked={followCatalog}
							onCheckedChange={onFollowChange}
						/>
						<Field.Content>
							<Field.Label for="model-follow-catalog" class="font-normal">
								Follow the catalog
							</Field.Label>
							<Field.Description>
								The label, prices and capabilities update with every catalog refresh. Turn this off
								to set your own.
							</Field.Description>
						</Field.Content>
					</Field.Field>
				{/if}
				<div class="grid gap-x-6 gap-y-5 sm:grid-cols-2">
					<Field.Field data-invalid={!!errors.model}>
						<Field.Label for="model-name" required={!model}>Model ID</Field.Label>
						<Input
							id="model-name"
							bind:value={modelName}
							class="font-mono"
							placeholder="claude-sonnet-4-5"
							disabled={!!model}
							aria-invalid={!!errors.model}
						/>
						{#if errors.model}
							<Field.Error>{errors.model}</Field.Error>
						{:else}
							<Field.Description>The name the provider's API expects.</Field.Description>
						{/if}
					</Field.Field>
					<Field.Field>
						<Field.Label for="model-label">Label</Field.Label>
						<Input
							id="model-label"
							bind:value={label}
							maxlength={100}
							placeholder="Claude Sonnet"
							disabled={locked}
						/>
					</Field.Field>
				</div>
				<Field.Set>
					<Field.Legend variant="label">Prices</Field.Legend>
					<div class="grid gap-x-6 gap-y-5 sm:grid-cols-2">
						{@render priceInput('in', 'Input')}
						{@render priceInput('out', 'Output')}
						{@render priceInput('cacheRead', 'Cache read')}
						{@render priceInput('cacheWrite', 'Cache write')}
					</div>
				</Field.Set>
				<Field.Field data-invalid={!!errors.context}>
					<Field.Label for="model-context">Context window</Field.Label>
					<div class="relative sm:w-1/2">
						<Input
							id="model-context"
							type="number"
							min="1"
							class="numeric pr-16"
							bind:value={context}
							disabled={locked}
							aria-invalid={!!errors.context}
						/>
						<span
							class="text-muted-foreground pointer-events-none absolute top-1/2 right-3 -translate-y-1/2 text-xs"
							>tokens</span
						>
					</div>
					{#if errors.context}<Field.Error>{errors.context}</Field.Error>{/if}
				</Field.Field>
				<Field.Set>
					<Field.Legend variant="label">Capabilities</Field.Legend>
					<div class="grid gap-3 sm:grid-cols-2">
						{#each Object.entries(capabilityLabels) as [key, text] (key)}
							<Field.Field orientation="horizontal">
								<Checkbox
									id="model-cap-{key}"
									bind:checked={caps[key as Capability]}
									disabled={locked}
								/>
								<Field.Label for="model-cap-{key}" class="font-normal">{text}</Field.Label>
							</Field.Field>
						{/each}
					</div>
				</Field.Set>
			</Field.Group>
		</form>
		<Dialog.Footer>
			<Button variant="outline" onclick={() => (open = false)}>Cancel</Button>
			<Button type="submit" form="model-form" {isLoading}>{model ? 'Save' : 'Add model'}</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
