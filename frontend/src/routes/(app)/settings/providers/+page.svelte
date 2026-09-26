<script lang="ts">
	import { invalidate } from '$app/navigation';
	import type { Model, Provider } from '$lib/api/types';
	import { openConfirmDialog } from '$lib/components/confirm-dialog';
	import { DataTable, renderSnippet, type TableQuery } from '$lib/components/data-table';
	import { Badge } from '$lib/components/ui/badge';
	import { Button } from '$lib/components/ui/button';
	import * as DropdownMenu from '$lib/components/ui/dropdown-menu';
	import * as Empty from '$lib/components/ui/empty';
	import { Switch } from '$lib/components/ui/switch';
	import * as Tooltip from '$lib/components/ui/tooltip';
	import RelativeTime from '$lib/components/relative-time.svelte';
	import ProviderService from '$lib/services/provider-service';
	import { apiErrorToast } from '$lib/utils/error-util';
	import { formatPricePerMillion, formatTokens } from '$lib/utils/format-util';
	import { tryCatch } from '$lib/utils/try-catch-util';
	import BotIcon from '@lucide/svelte/icons/bot';
	import CloudIcon from '@lucide/svelte/icons/cloud';
	import EllipsisIcon from '@lucide/svelte/icons/ellipsis';
	import PencilIcon from '@lucide/svelte/icons/pencil';
	import PlugZapIcon from '@lucide/svelte/icons/plug-zap';
	import PlusIcon from '@lucide/svelte/icons/plus';
	import RefreshCwIcon from '@lucide/svelte/icons/refresh-cw';
	import ToggleLeftIcon from '@lucide/svelte/icons/toggle-left';
	import ToggleRightIcon from '@lucide/svelte/icons/toggle-right';
	import Trash2Icon from '@lucide/svelte/icons/trash-2';
	import type { ColumnDef } from '@tanstack/table-core';
	import { toast } from 'svelte-sonner';
	import ModelDialog from './model-dialog.svelte';
	import ProviderDialog from './provider-dialog.svelte';
	import {
		capabilityLabels,
		modelSourceLabels,
		providerKindLabel,
		type Capability
	} from './provider-meta';
	import ProviderTestDialog from './provider-test-dialog.svelte';

	let { data } = $props();

	const providerService = new ProviderService();

	let providersTable: ReturnType<typeof DataTable<Provider>> | undefined = $state();
	let modelsTable: ReturnType<typeof DataTable<Model>> | undefined = $state();
	let providerDialogOpen = $state(false);
	let editingProvider = $state<Provider | null>(null);
	let testingProvider = $state<Provider | null>(null);
	let modelDialogOpen = $state(false);
	let editingModel = $state<Model | null>(null);
	let modelDefaultProvider = $state<string | undefined>();
	let syncing = $state<Record<string, boolean>>({});
	let toggling = $state<Record<string, boolean>>({});

	const providerOptions = $derived(data.providers.map((p) => ({ value: p.id, label: p.name })));

	// The same statuses the API filters by: enabled models are the ones jobs and defaults can pick
	const statusOptions = [
		{ value: 'enabled', label: 'Enabled' },
		{ value: 'disabled', label: 'Disabled' },
		{ value: 'unlisted', label: 'Not listed' }
	];

	const providerColumns: ColumnDef<Provider>[] = [
		{
			accessorKey: 'name',
			header: 'Name',
			meta: { sortKey: 'name', cellClass: 'font-medium' }
		},
		{
			accessorKey: 'kind',
			header: 'Kind',
			meta: { sortKey: 'kind' },
			cell: ({ row }) => renderSnippet(kindCell, row.original)
		},
		{
			accessorKey: 'baseUrl',
			header: 'Base URL',
			meta: { cellClass: 'max-w-72 truncate' },
			cell: ({ row }) => renderSnippet(baseUrlCell, row.original)
		},
		{
			accessorKey: 'hasApiKey',
			header: 'API key',
			cell: ({ row }) => renderSnippet(keyCell, row.original)
		},
		{
			accessorKey: 'modelCount',
			header: 'Models',
			cell: ({ row }) => renderSnippet(modelsCell, row.original)
		},
		{
			id: 'actions',
			header: () => renderSnippet(srOnly, 'Actions'),
			meta: { headerClass: 'w-0', cellClass: 'w-0 text-right' },
			cell: ({ row }) => renderSnippet(providerActionsCell, row.original)
		}
	];

	const modelColumns: ColumnDef<Model>[] = [
		{
			accessorKey: 'model',
			header: 'Model',
			meta: { sortKey: 'model', cellClass: 'max-w-64' },
			cell: ({ row }) => renderSnippet(modelCell, row.original)
		},
		{
			accessorKey: 'providerName',
			header: 'Provider',
			meta: { sortKey: 'provider' }
		},
		{
			id: 'priceIn',
			header: 'Input',
			meta: { sortKey: 'priceIn', headerClass: 'text-right', cellClass: 'text-right numeric' },
			cell: ({ row }) => formatPricePerMillion(row.original.price.in)
		},
		{
			id: 'priceOut',
			header: 'Output',
			meta: { sortKey: 'priceOut', headerClass: 'text-right', cellClass: 'text-right numeric' },
			cell: ({ row }) => formatPricePerMillion(row.original.price.out)
		},
		{
			id: 'cache',
			header: 'Cache read / write',
			meta: { headerClass: 'text-right', cellClass: 'text-right numeric text-muted-foreground' },
			cell: ({ row }) =>
				`${formatPricePerMillion(row.original.price.cacheRead)} / ${formatPricePerMillion(row.original.price.cacheWrite)}`
		},
		{
			accessorKey: 'contextWindow',
			header: 'Context',
			meta: {
				sortKey: 'contextWindow',
				headerClass: 'text-right',
				cellClass: 'text-right numeric'
			},
			cell: ({ row }) => formatTokens(row.original.contextWindow)
		},
		{
			id: 'caps',
			header: 'Capabilities',
			meta: { cellClass: 'max-w-72' },
			cell: ({ row }) => renderSnippet(capsCell, row.original)
		},
		{
			accessorKey: 'enabled',
			header: 'Enabled',
			meta: { sortKey: 'enabled', headerClass: 'w-0', cellClass: 'w-0' },
			cell: ({ row }) => renderSnippet(enabledCell, row.original)
		},
		{
			id: 'actions',
			header: () => renderSnippet(srOnly, 'Actions'),
			meta: { headerClass: 'w-0', cellClass: 'w-0 text-right' },
			cell: ({ row }) => renderSnippet(modelActionsCell, row.original)
		}
	];

	function openAddProvider() {
		editingProvider = null;
		providerDialogOpen = true;
	}

	function openEditProvider(provider: Provider) {
		editingProvider = provider;
		providerDialogOpen = true;
	}

	// Providers feed the model filter and pickers, so both tables and the page data reload
	async function reloadAll() {
		await invalidate('app:providers');
		await Promise.all([providersTable?.refresh(), modelsTable?.refresh()]);
	}

	function onProviderSaved(name: string, created: boolean) {
		toast.success(created ? `Added "${name}"` : `Saved "${name}"`);
		void reloadAll();
	}

	function confirmDeleteProvider(provider: Provider) {
		openConfirmDialog({
			title: `Delete ${provider.name}`,
			message: `Its ${provider.modelCount} ${provider.modelCount === 1 ? 'model is' : 'models are'} removed too, and jobs or defaults that use them fall back to the workspace defaults.`,
			confirm: {
				label: 'Delete',
				destructive: true,
				action: async () => {
					const result = await tryCatch(providerService.delete(provider.id));
					if (result.error) {
						apiErrorToast(result.error, 'Failed to delete the provider');
						return;
					}
					toast.success(`Deleted "${provider.name}"`);
					await reloadAll();
				}
			}
		});
	}

	// A sync reads the source now, e.g. right after pulling a model into Ollama
	async function syncModels(provider: Provider) {
		syncing[provider.id] = true;
		const result = await tryCatch(providerService.sync(provider.id));
		syncing[provider.id] = false;
		if (result.error) {
			apiErrorToast(result.error, 'Failed to sync the models');
			return;
		}
		const { added, updated, unlisted, error } = result.data;
		if (error) {
			toast.error(`Couldn't read the models of "${provider.name}"`, { description: error });
		} else if (added + updated + unlisted === 0) {
			toast.success(`The models of "${provider.name}" are up to date`);
		} else {
			const parts = [
				added && `${added} added`,
				updated && `${updated} updated`,
				unlisted && `${unlisted} no longer listed`
			].filter(Boolean);
			toast.success(`Synced "${provider.name}"`, { description: parts.join(', ') });
		}
		await reloadAll();
	}

	async function setProviderModelsEnabled(provider: Provider, enabled: boolean) {
		const result = await tryCatch(providerService.setModelsEnabled(provider.id, enabled));
		if (result.error) {
			apiErrorToast(result.error, `Failed to ${enabled ? 'enable' : 'disable'} the models`);
			return;
		}
		toast.success(`${enabled ? 'Enabled' : 'Disabled'} every model of "${provider.name}"`);
		await reloadAll();
	}

	// The switch updates the row right away and puts it back when the server refuses, e.g. for a default model
	async function setModelEnabled(model: Model, enabled: boolean) {
		toggling[model.id] = true;
		modelsTable?.updateRow(model.id, { enabled });
		const result = await tryCatch(providerService.updateModel(model.id, { enabled }));
		toggling[model.id] = false;
		if (result.error) {
			modelsTable?.updateRow(model.id, { enabled: !enabled });
			apiErrorToast(result.error, `Failed to ${enabled ? 'enable' : 'disable'} the model`);
			return;
		}
		await Promise.all([invalidate('app:providers'), providersTable?.refresh()]);
	}

	function openAddModel(providerId?: string) {
		editingModel = null;
		modelDefaultProvider = providerId;
		modelDialogOpen = true;
	}

	function openEditModel(model: Model) {
		editingModel = model;
		modelDialogOpen = true;
	}

	function onModelSaved(name: string, created: boolean) {
		toast.success(created ? `Added "${name}"` : `Saved "${name}"`);
		void reloadAll();
	}

	function confirmDeleteModel(model: Model) {
		openConfirmDialog({
			title: `Delete ${model.label || model.model}`,
			message: 'Jobs and defaults that use this model fall back to the workspace defaults.',
			confirm: {
				label: 'Delete',
				destructive: true,
				action: async () => {
					const result = await tryCatch(providerService.deleteModel(model.id));
					if (result.error) {
						apiErrorToast(result.error, 'Failed to delete the model');
						return;
					}
					toast.success(`Deleted "${model.label || model.model}"`);
					await reloadAll();
				}
			}
		});
	}

	function fetchModels(query: TableQuery) {
		return providerService.listModels(query);
	}
</script>

{#snippet srOnly(text: string)}
	<span class="sr-only">{text}</span>
{/snippet}

{#snippet kindCell(provider: Provider)}
	<Badge variant="outline" class="font-normal">{providerKindLabel(provider.kind)}</Badge>
{/snippet}

{#snippet baseUrlCell(provider: Provider)}
	{#if provider.baseUrl}
		<span class="font-mono text-xs" title={provider.baseUrl}>{provider.baseUrl}</span>
	{:else}
		<span class="text-muted-foreground">Official API</span>
	{/if}
{/snippet}

<!-- How many models can be picked, and where the list comes from with how its last sync went -->
{#snippet modelsCell(provider: Provider)}
	<span class="flex flex-col">
		<span class="numeric whitespace-nowrap">
			{provider.enabledModelCount} of {provider.modelCount} enabled
		</span>
		{#if provider.syncError}
			<Tooltip.Root>
				<Tooltip.Trigger>
					{#snippet child({ props })}
						<span {...props} class="w-fit cursor-default">
							<Badge variant="destructive">Sync failed</Badge>
						</span>
					{/snippet}
				</Tooltip.Trigger>
				<Tooltip.Content class="max-w-80">{provider.syncError}</Tooltip.Content>
			</Tooltip.Root>
		{:else}
			<span class="text-muted-foreground text-xs whitespace-nowrap">
				{#if provider.syncedAt}
					{modelSourceLabels[provider.modelSource]} · synced
					<RelativeTime value={provider.syncedAt} />
				{:else}
					{modelSourceLabels[provider.modelSource]}
				{/if}
			</span>
		{/if}
	</span>
{/snippet}

{#snippet keyCell(provider: Provider)}
	{#if provider.hasApiKey}
		<Badge variant="secondary">Set</Badge>
	{:else}
		<span class="text-muted-foreground">None</span>
	{/if}
{/snippet}

{#snippet providerActionsCell(provider: Provider)}
	<div class="flex justify-end gap-1">
		<Button variant="outline" size="sm" onclick={() => (testingProvider = provider)}>
			<PlugZapIcon data-icon="inline-start" />
			Test
		</Button>
		<DropdownMenu.Root>
			<DropdownMenu.Trigger>
				{#snippet child({ props })}
					<Button
						{...props}
						variant="ghost"
						size="icon-sm"
						aria-label="More actions for {provider.name}"
					>
						<EllipsisIcon />
					</Button>
				{/snippet}
			</DropdownMenu.Trigger>
			<DropdownMenu.Content align="end">
				<DropdownMenu.Item onSelect={() => openEditProvider(provider)}>
					<PencilIcon />
					Edit
				</DropdownMenu.Item>
				<DropdownMenu.Item onSelect={() => openAddModel(provider.id)}>
					<PlusIcon />
					Add model
				</DropdownMenu.Item>
				{#if provider.modelSource !== 'manual'}
					<DropdownMenu.Item disabled={syncing[provider.id]} onSelect={() => syncModels(provider)}>
						<RefreshCwIcon />
						Sync models
					</DropdownMenu.Item>
				{/if}
				{#if provider.modelCount > 0}
					<DropdownMenu.Item onSelect={() => setProviderModelsEnabled(provider, true)}>
						<ToggleRightIcon />
						Enable all models
					</DropdownMenu.Item>
					<DropdownMenu.Item onSelect={() => setProviderModelsEnabled(provider, false)}>
						<ToggleLeftIcon />
						Disable all models
					</DropdownMenu.Item>
				{/if}
				<DropdownMenu.Item variant="destructive" onSelect={() => confirmDeleteProvider(provider)}>
					<Trash2Icon />
					Delete
				</DropdownMenu.Item>
			</DropdownMenu.Content>
		</DropdownMenu.Root>
	</div>
{/snippet}

{#snippet modelCell(model: Model)}
	<span class="flex flex-col">
		<span class="flex items-center gap-2">
			<span class="truncate font-medium">{model.label || model.model}</span>
			{#if model.unlistedAt !== null}
				<Badge variant="warning" title="The provider stopped listing this model">Not listed</Badge>
			{/if}
		</span>
		{#if model.label}
			<span class="text-muted-foreground truncate font-mono text-xs">{model.model}</span>
		{/if}
	</span>
{/snippet}

{#snippet enabledCell(model: Model)}
	<Switch
		checked={model.enabled}
		disabled={toggling[model.id]}
		aria-label="{model.enabled ? 'Disable' : 'Enable'} {model.label || model.model}"
		onCheckedChange={(checked) => setModelEnabled(model, checked)}
	/>
{/snippet}

{#snippet capsCell(model: Model)}
	<span class="flex flex-wrap gap-1">
		{#each Object.entries(capabilityLabels) as [key, text] (key)}
			{#if model.caps[key as Capability]}
				<Badge variant="secondary" class="font-normal">{text}</Badge>
			{/if}
		{/each}
	</span>
{/snippet}

<!-- The same overflow menu as the providers table above, so both tables on the page act alike -->
{#snippet modelActionsCell(model: Model)}
	<div class="flex justify-end gap-1">
		<DropdownMenu.Root>
			<DropdownMenu.Trigger>
				{#snippet child({ props })}
					<Button
						{...props}
						variant="ghost"
						size="icon-sm"
						aria-label="More actions for {model.label || model.model}"
					>
						<EllipsisIcon />
					</Button>
				{/snippet}
			</DropdownMenu.Trigger>
			<DropdownMenu.Content align="end">
				<DropdownMenu.Item onSelect={() => openEditModel(model)}>
					<PencilIcon />
					Edit
				</DropdownMenu.Item>
				<!-- A listed model would come back with the next sync, so it can only be turned off -->
				{#if !model.synced || model.unlistedAt !== null}
					<DropdownMenu.Item variant="destructive" onSelect={() => confirmDeleteModel(model)}>
						<Trash2Icon />
						Delete
					</DropdownMenu.Item>
				{/if}
			</DropdownMenu.Content>
		</DropdownMenu.Root>
	</div>
{/snippet}

<svelte:head>
	<title>Providers & models · Umpteenth</title>
</svelte:head>

<div class="flex flex-col gap-10">
	<section class="flex flex-col gap-3">
		<div class="flex flex-col gap-1">
			<h2 class="text-lg font-semibold">Providers</h2>
			<p class="text-muted-foreground text-sm">The LLM APIs Umpteenth can call.</p>
		</div>
		<DataTable
			bind:this={providersTable}
			label="Providers"
			columns={providerColumns}
			fetchPage={(query) => providerService.list(query)}
			getRowId={(provider) => provider.id}
			defaultSort="name"
			defaultPageSize={10}
			urlPrefix="providers"
			searchPlaceholder="Search providers"
		>
			{#snippet actions()}
				<Button onclick={openAddProvider}>
					<PlusIcon data-icon="inline-start" />
					Add provider
				</Button>
			{/snippet}
			{#snippet empty()}
				<Empty.Root class="py-6">
					<Empty.Header>
						<Empty.Media variant="icon">
							<CloudIcon />
						</Empty.Media>
						<Empty.Title>No providers</Empty.Title>
						<Empty.Description
							>Add Anthropic or an OpenAI-compatible API to run jobs.</Empty.Description
						>
					</Empty.Header>
				</Empty.Root>
			{/snippet}
		</DataTable>
	</section>

	<section class="flex flex-col gap-3">
		<div class="flex flex-col gap-1">
			<h2 class="text-lg font-semibold">Models</h2>
			<p class="text-muted-foreground text-sm">
				Prices are per 1M tokens and turn token usage into the costs shown on runs. Only enabled
				models can be picked for jobs and defaults.
			</p>
		</div>
		<DataTable
			bind:this={modelsTable}
			label="Models"
			columns={modelColumns}
			fetchPage={fetchModels}
			getRowId={(model) => model.id}
			defaultSort="provider,model"
			urlPrefix="models"
			searchPlaceholder="Search models"
			filters={[
				{ key: 'provider', label: 'Provider', options: providerOptions },
				{ key: 'status', label: 'Status', options: statusOptions }
			]}
		>
			{#snippet actions()}
				<Button
					variant="outline"
					onclick={() => openAddModel()}
					disabled={data.providers.length === 0}
				>
					<PlusIcon data-icon="inline-start" />
					Add model
				</Button>
			{/snippet}
			{#snippet empty()}
				<Empty.Root class="py-6">
					<Empty.Header>
						<Empty.Media variant="icon">
							<BotIcon />
						</Empty.Media>
						<Empty.Title>No models</Empty.Title>
						<Empty.Description>Sync a provider's models, or add one by hand.</Empty.Description>
					</Empty.Header>
				</Empty.Root>
			{/snippet}
		</DataTable>
	</section>
</div>

<ProviderDialog
	bind:open={providerDialogOpen}
	provider={editingProvider}
	onSaved={onProviderSaved}
/>
<ProviderTestDialog bind:provider={testingProvider} />
<ModelDialog
	bind:open={modelDialogOpen}
	model={editingModel}
	providers={data.providers}
	defaultProviderId={modelDefaultProvider}
	onSaved={onModelSaved}
/>
