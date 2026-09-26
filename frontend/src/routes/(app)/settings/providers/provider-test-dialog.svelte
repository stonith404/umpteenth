<script lang="ts">
	import type { Model, Provider, ProviderTestResult } from '$lib/api/types';
	import ModelSelect from '$lib/components/model-select.svelte';
	import * as Alert from '$lib/components/ui/alert';
	import { Button } from '$lib/components/ui/button';
	import * as Dialog from '$lib/components/ui/dialog';
	import * as Field from '$lib/components/ui/field';
	import ProviderService from '$lib/services/provider-service';
	import { apiErrorToast } from '$lib/utils/error-util';
	import { formatDuration } from '$lib/utils/format-util';
	import { tryCatch } from '$lib/utils/try-catch-util';
	import CircleAlertIcon from '@lucide/svelte/icons/circle-alert';
	import CircleCheckIcon from '@lucide/svelte/icons/circle-check';
	import { untrack } from 'svelte';

	let { provider = $bindable(null) }: { provider: Provider | null } = $props();

	const providerService = new ProviderService();

	let models = $state<Model[]>([]);
	let modelId = $state('');
	let result = $state<ProviderTestResult | null>(null);
	let testing = $state(false);

	// The test goes through one of the provider's own models, so they are loaded when the dialog opens
	$effect(() => {
		const current = provider;
		if (!current) return;
		untrack(async () => {
			result = null;
			models = [];
			modelId = '';
			const response = await tryCatch(providerService.listAllModels({ provider: current.id }));
			if (provider !== current) return;
			if (response.error) {
				apiErrorToast(response.error, 'Failed to load the models');
				return;
			}
			models = response.data;

			// An enabled model is the likeliest to be the one worth testing
			modelId = (models.find((m) => m.enabled && m.unlistedAt === null) ?? models[0])?.id ?? '';
		});
	});

	async function runTest() {
		if (!provider || !modelId) return;
		testing = true;
		result = null;
		const response = await tryCatch(providerService.test(provider.id, modelId));
		testing = false;
		if (response.error) {
			apiErrorToast(response.error, 'Failed to test the provider');
			return;
		}
		result = response.data;
	}
</script>

<Dialog.Root open={provider !== null} onOpenChange={(open) => !open && (provider = null)}>
	<Dialog.Content class="sm:max-w-lg">
		<Dialog.Header>
			<Dialog.Title>Test {provider?.name}</Dialog.Title>
			<Dialog.Description>
				Sends a tiny prompt to check the API key, the base URL and the model name.
			</Dialog.Description>
		</Dialog.Header>
		<div class="flex flex-col gap-4">
			<Field.Field>
				<Field.Label for="provider-test-model">Model</Field.Label>
				{#if models.length > 0}
					<ModelSelect
						id="provider-test-model"
						{models}
						includeUnavailable
						bind:value={modelId}
						noneLabel="Pick a model"
					/>
				{:else}
					<p class="text-muted-foreground text-sm">
						The provider has no models yet. Add one below to test it.
					</p>
				{/if}
			</Field.Field>
			{#if result}
				{#if result.ok}
					<Alert.Root variant="success">
						<CircleCheckIcon />
						<Alert.Title>Replied in {formatDuration(result.latencyMs)}</Alert.Title>
						<Alert.Description>
							<p class="font-mono text-xs break-all whitespace-pre-wrap">
								{result.reply || '(empty reply)'}
							</p>
						</Alert.Description>
					</Alert.Root>
				{:else}
					<Alert.Root variant="destructive">
						<CircleAlertIcon />
						<Alert.Title>Failed after {formatDuration(result.latencyMs)}</Alert.Title>
						<Alert.Description>
							<p class="font-mono text-xs break-all whitespace-pre-wrap">{result.error}</p>
						</Alert.Description>
					</Alert.Root>
				{/if}
			{/if}
		</div>
		<Dialog.Footer>
			<Button variant="outline" onclick={() => (provider = null)}>Close</Button>
			<Button onclick={runTest} isLoading={testing} disabled={!modelId}>Send test prompt</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
