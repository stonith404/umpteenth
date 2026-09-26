<script lang="ts">
	import { isApiError } from '$lib/api/api-error';
	import type { Provider, ProviderCreate, ProviderUpdate } from '$lib/api/types';
	import { Button } from '$lib/components/ui/button';
	import { Checkbox } from '$lib/components/ui/checkbox';
	import * as Dialog from '$lib/components/ui/dialog';
	import * as Field from '$lib/components/ui/field';
	import { Input } from '$lib/components/ui/input';
	import * as Tabs from '$lib/components/ui/tabs';
	import ProviderService from '$lib/services/provider-service';
	import { apiErrorToast, getErrorMessage } from '$lib/utils/error-util';
	import { preventDefault } from '$lib/utils/event-util';
	import { tryCatch } from '$lib/utils/try-catch-util';
	import { untrack } from 'svelte';
	import { toast } from 'svelte-sonner';
	import { openAiPresets, providerKindLabel, usesCatalog } from './provider-meta';

	let {
		open = $bindable(false),
		provider,
		onSaved
	}: {
		open?: boolean;
		// The provider to edit, or null to add one
		provider: Provider | null;
		onSaved: (name: string, created: boolean) => void;
	} = $props();

	type Kind = 'anthropic' | 'openai';

	const providerService = new ProviderService();

	let kind = $state<Kind>('anthropic');
	let name = $state('');
	let baseUrl = $state('');
	let apiKey = $state('');
	let removeKey = $state(false);
	let errors = $state<Record<string, string>>({});
	let isLoading = $state(false);

	// A fresh dialog starts from the provider being edited, or from a new Anthropic provider
	$effect(() => {
		if (!open) return;
		untrack(() => {
			kind = provider?.kind === 'openai' ? 'openai' : 'anthropic';
			name = provider?.name ?? 'Anthropic';
			baseUrl = provider?.baseUrl ?? '';
			removeKey = false;
			errors = {};
		});
	});

	// Switching the kind of a new provider suggests a matching name, unless the user already typed one
	function onKindChange(next: string) {
		const previousDefault = kind === 'anthropic' ? 'Anthropic' : 'OpenAI';
		kind = next as Kind;
		if (!name.trim() || name === previousDefault) {
			name = kind === 'anthropic' ? 'Anthropic' : 'OpenAI';
		}
		baseUrl = '';
	}

	function applyPreset(preset: (typeof openAiPresets)[number]) {
		baseUrl = preset.baseUrl;
		if (!provider) name = preset.name;
	}

	function validate() {
		const next: Record<string, string> = {};
		if (!name.trim()) next.name = 'Required';
		if (baseUrl.trim() && !/^https?:\/\//.test(baseUrl.trim())) {
			next.baseUrl = 'Must start with http:// or https://';
		}
		if (!provider && kind === 'openai' && !baseUrl.trim() && !apiKey.trim()) {
			next.apiKey = 'Required for the OpenAI API';
		}
		errors = next;
		return Object.keys(next).length === 0;
	}

	async function onSubmit() {
		if (!validate()) return;
		isLoading = true;

		let request: Promise<unknown>;
		if (provider) {
			// An empty key field keeps the stored key, the checkbox removes it
			const body: ProviderUpdate = { name: name.trim(), baseUrl: baseUrl.trim() };
			if (removeKey) body.apiKey = '';
			else if (apiKey.trim()) body.apiKey = apiKey.trim();
			request = providerService.update(provider.id, body);
		} else {
			const body: ProviderCreate = {
				kind,
				name: name.trim(),
				baseUrl: baseUrl.trim() || undefined,
				apiKey: apiKey.trim() || undefined
			};
			request = providerService.create(body).then((created) => {
				// The provider is saved even when its server can't be reached, so that is a warning, not a failure
				if (created.sync.error) {
					toast.warning(`Failed to read the models of "${body.name}"`, {
						description: created.sync.error
					});
				}
			});
		}
		const result = await tryCatch(request);
		isLoading = false;

		if (result.error) {
			if (isApiError(result.error, 'validation_failed', 'invalid_field', 'already_in_use')) {
				const next: Record<string, string> = {};
				for (const f of result.error.fields) {
					next[f.field.replace(/^body\./, '').split('.')[0]] = f.message;
				}
				if (Object.keys(next).length === 0) next.name = getErrorMessage(result.error);
				errors = next;
			}
			apiErrorToast(result.error, 'Failed to save the provider');
			return;
		}
		open = false;
		onSaved(name.trim(), !provider);
	}
</script>

<!-- The typed API key is dropped once the dialog has closed, so it doesn't linger in memory until the next open -->
<Dialog.Root bind:open onOpenChangeComplete={(isOpen) => !isOpen && (apiKey = '')}>
	<Dialog.Content class="sm:max-w-lg">
		<Dialog.Header>
			<Dialog.Title>{provider ? `Edit ${provider.name}` : 'Add provider'}</Dialog.Title>
			<Dialog.Description>
				{provider
					? `${providerKindLabel(provider.kind)} provider. Its models keep working with the new settings.`
					: 'Connect an LLM API. OpenAI-compatible covers OpenAI itself, OpenRouter and local servers.'}
			</Dialog.Description>
		</Dialog.Header>
		<form novalidate id="provider-form" onsubmit={preventDefault(onSubmit)}>
			<Field.Group>
				{#if !provider}
					<Field.Field>
						<Field.Label>Kind</Field.Label>
						<Tabs.Root value={kind} onValueChange={onKindChange}>
							<Tabs.List aria-label="Provider kind">
								<Tabs.Trigger value="anthropic">Anthropic</Tabs.Trigger>
								<Tabs.Trigger value="openai">OpenAI-compatible</Tabs.Trigger>
							</Tabs.List>
						</Tabs.Root>
					</Field.Field>
				{/if}
				<Field.Field data-invalid={!!errors.name}>
					<Field.Label for="provider-name">Name</Field.Label>
					<Input
						id="provider-name"
						bind:value={name}
						maxlength={100}
						aria-invalid={!!errors.name}
					/>
					{#if errors.name}<Field.Error>{errors.name}</Field.Error>{/if}
				</Field.Field>
				<Field.Field data-invalid={!!errors.baseUrl}>
					<Field.Label for="provider-base-url" optional>Base URL</Field.Label>
					<Input
						id="provider-base-url"
						bind:value={baseUrl}
						type="url"
						mono
						placeholder={kind === 'anthropic'
							? 'https://api.anthropic.com'
							: 'https://api.openai.com/v1'}
						aria-invalid={!!errors.baseUrl}
					/>
					{#if kind === 'openai' || provider?.kind === 'openai'}
						<div class="flex flex-wrap gap-1.5">
							{#each openAiPresets as preset (preset.name)}
								<Button
									variant={baseUrl === preset.baseUrl ? 'secondary' : 'outline'}
									size="xs"
									onclick={() => applyPreset(preset)}
								>
									{preset.name}
								</Button>
							{/each}
						</div>
					{/if}
					{#if errors.baseUrl}
						<Field.Error>{errors.baseUrl}</Field.Error>
					{:else}
						<Field.Description>Leave empty for the provider's official API.</Field.Description>
					{/if}
				</Field.Field>
				<Field.Field data-invalid={!!errors.apiKey}>
					<!-- A key is needed for the official APIs, while local servers behind a base URL usually run without one -->
					<Field.Label for="provider-api-key" optional={!!provider || !!baseUrl.trim()}>
						API key
					</Field.Label>
					<Input
						id="provider-api-key"
						bind:value={apiKey}
						type="password"
						autocomplete="off"
						mono
						disabled={removeKey}
						placeholder={provider?.hasApiKey ? 'Leave empty to keep the stored key' : ''}
						aria-invalid={!!errors.apiKey}
					/>
					{#if errors.apiKey}
						<Field.Error>{errors.apiKey}</Field.Error>
					{:else}
						<Field.Description>
							Stored encrypted and never shown again. Local servers usually need none.
						</Field.Description>
					{/if}
				</Field.Field>
				{#if provider?.hasApiKey}
					<Field.Field orientation="horizontal">
						<Checkbox id="provider-remove-key" bind:checked={removeKey} />
						<Field.Label for="provider-remove-key" variant="choice">
							Remove the stored API key
						</Field.Label>
					</Field.Field>
				{/if}
				<p class="text-muted-foreground text-sm leading-snug">
					{#if usesCatalog(provider?.kind ?? kind, baseUrl)}
						Models, prices and capabilities come from the models.dev catalog and stay up to date.
					{:else}
						Models are read from the server's model list and kept in sync. Their capabilities are
						filled in from models.dev where the server doesn't report them.
					{/if}
				</p>
			</Field.Group>
		</form>
		<Dialog.Footer>
			<Button variant="outline" onclick={() => (open = false)}>Cancel</Button>
			<Button type="submit" form="provider-form" {isLoading}>
				{provider ? 'Save' : 'Add provider'}
			</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
