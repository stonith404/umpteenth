<script lang="ts">
	import { invalidate } from '$app/navigation';
	import { page } from '$app/state';
	import type { Model, Provider } from '$lib/api/types';
	import { openConfirmDialog } from '$lib/components/confirm-dialog';
	import {
		DataTable,
		RowActions,
		actionsColumn,
		renderSnippet,
		type RowAction,
		type TableQuery
	} from '$lib/components/data-table';
	import { Badge } from '$lib/components/ui/badge';
	import { Button } from '$lib/components/ui/button';
	import * as Empty from '$lib/components/ui/empty';
	import { Switch } from '$lib/components/ui/switch';
	import * as Tooltip from '$lib/components/ui/tooltip';
	import RelativeTime from '$lib/components/relative-time.svelte';
	import AdminOnlyNotice from '$lib/components/workspaces/admin-only-notice.svelte';
	import ProviderService from '$lib/services/provider-service';
	import { apiErrorToast } from '$lib/utils/error-util';
	import { formatPricePerMillion, formatTokens } from '$lib/utils/format-util';
	import { tryCatch } from '$lib/utils/try-catch-util';
	import { hasRole } from '$lib/utils/workspace-util';
	import BotIcon from '@lucide/svelte/icons/bot';
	import CloudIcon from '@lucide/svelte/icons/cloud';
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

	// Members pick models for their jobs, and only admins change which providers and models the workspace has
	const canManage = $derived(hasRole(page.data.user!, 'admin'));

	let providersTable: ReturnType<typeof DataTable<Provider>> | undefined = $state();
	let modelsTable: ReturnType<typeof DataTable<Model>> | undefined = $state();
	let providerDialogOpen = $state(false);
	let editingProvider = $state<Provider | null>(null);
	let testingProvider = $state<Provider | null>(null);
	let modelDialogOpen = $state(false);
	let editingModel = $state<Model | null>(null);
	let modelDefaultProvider = $state<string | undefined>();
	let syncing = $state<Record<string, boolean>>({});
	let noProviders = $state(false);
	let noModels = $state(false);
	let toggling = $state<Record<string, boolean>>({});

	const providerOptions = $derived(data.providers.map((p) => ({ value: p.id, label: p.name })));

	// The same statuses the API filters by: enabled models are the ones jobs and defaults can pick
	const statusOptions = [
		{ value: 'enabled', label: 'Enabled' },
		{ value: 'disabled', label: 'Disabled' },
		{ value: 'unlisted', label: 'Not listed' }
	];

	// The kind and base URL share the name's cell, which keeps the row to the columns people compare
	const providerColumns: ColumnDef<Provider>[] = [
		{
			accessorKey: 'name',
			header: 'Name',
			meta: { sortKey: 'name', cellClass: 'w-full max-w-0' },
			cell: ({ row }) => renderSnippet(providerCell, row.original)
		},
		{
			accessorKey: 'hasApiKey',
			header: 'API key',
			meta: { hideBelow: 'md' },
			cell: ({ row }) => renderSnippet(keyCell, row.original)
		},
		{
			accessorKey: 'modelCount',
			header: 'Models',
			meta: { hideBelow: 'sm' },
			cell: ({ row }) => renderSnippet(modelsCell, row.original)
		},
		actionsColumn<Provider>((provider) => renderSnippet(providerActionsCell, provider))
	];

	// Input and output prices share one column with the cache prices in its tooltip, and the capabilities read as one short line
	const modelColumns: ColumnDef<Model>[] = [
		{
			accessorKey: 'model',
			header: 'Model',
			meta: { sortKey: 'model', cellClass: 'w-full max-w-0' },
			cell: ({ row }) => renderSnippet(modelCell, row.original)
		},
		{
			accessorKey: 'providerName',
			header: 'Provider',
			meta: { sortKey: 'provider', hideBelow: 'xl', cellClass: 'whitespace-nowrap' }
		},
		{
			id: 'priceIn',
			header: 'Price per 1M',
			meta: { sortKey: 'priceIn', align: 'right', hideBelow: 'sm', cellClass: 'numeric' },
			cell: ({ row }) => renderSnippet(priceCell, row.original)
		},
		{
			accessorKey: 'contextWindow',
			header: 'Context',
			meta: { sortKey: 'contextWindow', align: 'right', hideBelow: 'xl', cellClass: 'numeric' },
			cell: ({ row }) => formatTokens(row.original.contextWindow)
		},
		{
			id: 'caps',
			header: 'Capabilities',
			meta: { hideBelow: 'xl' },
			cell: ({ row }) => renderSnippet(capsCell, row.original)
		},
		{
			accessorKey: 'enabled',
			header: 'Enabled',
			meta: { sortKey: 'enabled', headerClass: 'w-0', cellClass: 'w-0' },
			cell: ({ row }) => renderSnippet(enabledCell, row.original)
		},
		actionsColumn<Model>((model) => renderSnippet(modelActionsCell, model))
	];

	// An empty table shows its create button in the empty panel instead of the toolbar, like on the jobs list
	async function fetchProviders(query: TableQuery) {
		const result = await providerService.list(query);
		noProviders = result.total === 0 && !query.search;
		return result;
	}

	async function fetchModels(query: TableQuery) {
		const result = await providerService.listModels(query);
		noModels = result.total === 0 && !query.search && !query.provider && !query.status;
		return result;
	}

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
			message: `Its ${provider.modelCount} ${provider.modelCount === 1 ? 'model is' : 'models are'} removed too. Jobs that use them fall back to the workspace defaults, and a default that uses them is unset until you pick another.`,
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
			toast.error(`Failed to read the models of "${provider.name}"`, { description: error });
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

	function modelName(model: Model) {
		return model.label || model.model;
	}

	// The capabilities a model declares, in the order the model form lists them
	function capabilities(model: Model) {
		return Object.entries(capabilityLabels)
			.filter(([key]) => model.caps[key as Capability])
			.map(([, text]) => text);
	}

	// Testing is the main thing to do with a provider, the rest sits in the menu
	// Enabling or disabling every model is offered only while it would change something
	function providerActions(provider: Provider): {
		inline: RowAction;
		items: (RowAction | false)[];
	} {
		return {
			inline: { label: 'Test', icon: PlugZapIcon, onSelect: () => (testingProvider = provider) },
			items: [
				{ label: 'Edit', icon: PencilIcon, onSelect: () => openEditProvider(provider) },
				{ label: 'Add model', icon: PlusIcon, onSelect: () => openAddModel(provider.id) },
				provider.modelSource !== 'manual' && {
					label: 'Sync models',
					icon: RefreshCwIcon,
					disabled: syncing[provider.id],
					onSelect: () => syncModels(provider)
				},
				provider.enabledModelCount < provider.modelCount && {
					label: 'Enable all models',
					icon: ToggleRightIcon,
					onSelect: () => setProviderModelsEnabled(provider, true)
				},
				provider.enabledModelCount > 0 && {
					label: 'Disable all models',
					icon: ToggleLeftIcon,
					onSelect: () => setProviderModelsEnabled(provider, false)
				},
				{
					label: 'Delete',
					icon: Trash2Icon,
					variant: 'destructive',
					onSelect: () => confirmDeleteProvider(provider)
				}
			]
		};
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
</script>

{#snippet providerCell(provider: Provider)}
	<span class="flex min-w-0 flex-col">
		<span class="truncate font-medium" title={provider.name}>{provider.name}</span>
		<span class="text-muted-foreground flex min-w-0 items-center gap-1.5 text-sm">
			<span class="shrink-0">{providerKindLabel(provider.kind)}</span>
			<span aria-hidden="true">·</span>
			{#if provider.baseUrl}
				<span class="truncate font-mono text-xs" title={provider.baseUrl}>{provider.baseUrl}</span>
			{:else}
				<span class="truncate">Official API</span>
			{/if}
		</span>
	</span>
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
			<span class="text-muted-foreground text-sm whitespace-nowrap">
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
	{#if canManage}
		{@const actions = providerActions(provider)}
		<RowActions name={provider.name} inline={actions.inline} items={actions.items} />
	{/if}
{/snippet}

{#snippet modelCell(model: Model)}
	<span class="flex min-w-0 flex-col">
		<span class="flex min-w-0 items-center gap-2">
			<span class="truncate font-medium" title={modelName(model)}>{modelName(model)}</span>
			{#if model.unlistedAt !== null}
				<Badge variant="warning" title="The provider stopped listing this model">Not listed</Badge>
			{/if}
		</span>
		{#if model.label}
			<span class="text-muted-foreground truncate font-mono text-xs" title={model.model}
				>{model.model}</span
			>
		{/if}
	</span>
{/snippet}

<!-- Input and output side by side, the cache prices that only some runs pay in the tooltip -->
{#snippet priceCell(model: Model)}
	<Tooltip.Root>
		<Tooltip.Trigger>
			{#snippet child({ props })}
				<span {...props} class="cursor-default whitespace-nowrap">
					{formatPricePerMillion(model.price.in)}
					<span class="text-muted-foreground">/</span>
					{formatPricePerMillion(model.price.out)}
				</span>
			{/snippet}
		</Tooltip.Trigger>
		<Tooltip.Content>
			<dl class="numeric grid grid-cols-pairs gap-x-4 gap-y-0.5">
				<dt class="text-muted-foreground">Input</dt>
				<dd class="text-right">{formatPricePerMillion(model.price.in)}</dd>
				<dt class="text-muted-foreground">Output</dt>
				<dd class="text-right">{formatPricePerMillion(model.price.out)}</dd>
				<dt class="text-muted-foreground">Cache read</dt>
				<dd class="text-right">{formatPricePerMillion(model.price.cacheRead)}</dd>
				<dt class="text-muted-foreground">Cache write</dt>
				<dd class="text-right">{formatPricePerMillion(model.price.cacheWrite)}</dd>
			</dl>
		</Tooltip.Content>
	</Tooltip.Root>
{/snippet}

<!-- Members can't switch models on or off, so they read the state instead of a switch they can't use -->
{#snippet enabledCell(model: Model)}
	{#if canManage}
		<Switch
			checked={model.enabled}
			disabled={toggling[model.id]}
			aria-label="{model.enabled ? 'Disable' : 'Enable'} {modelName(model)}"
			onCheckedChange={(checked) => setModelEnabled(model, checked)}
		/>
	{:else}
		<span class={model.enabled ? '' : 'text-muted-foreground'}>
			{model.enabled ? 'Enabled' : 'Disabled'}
		</span>
	{/if}
{/snippet}

<!-- The first capability and a count, with the full list in the tooltip -->
{#snippet capsCell(model: Model)}
	{@const caps = capabilities(model)}
	{#if caps.length === 0}
		<span class="text-muted-foreground">—</span>
	{:else}
		<Tooltip.Root>
			<Tooltip.Trigger>
				{#snippet child({ props })}
					<span {...props} class="text-muted-foreground cursor-default whitespace-nowrap">
						{caps[0]}{#if caps.length > 1}<span class="numeric">&nbsp;+{caps.length - 1}</span>{/if}
					</span>
				{/snippet}
			</Tooltip.Trigger>
			<Tooltip.Content>
				<ul class="flex flex-col gap-0.5">
					{#each caps as cap (cap)}
						<li>{cap}</li>
					{/each}
				</ul>
			</Tooltip.Content>
		</Tooltip.Root>
	{/if}
{/snippet}

<!-- The same menu as the providers table above, so both tables on the page act alike -->
{#snippet modelActionsCell(model: Model)}
	{#if canManage}
		<!-- A listed model would come back with the next sync, so it can only be turned off -->
		<RowActions
			name={modelName(model)}
			items={[
				{ label: 'Edit', icon: PencilIcon, onSelect: () => openEditModel(model) },
				(!model.synced || model.unlistedAt !== null) && {
					label: 'Delete',
					icon: Trash2Icon,
					variant: 'destructive',
					onSelect: () => confirmDeleteModel(model)
				}
			]}
		/>
	{/if}
{/snippet}

{#snippet addProviderButton()}
	{#if canManage}
		<Button onclick={openAddProvider}>
			<PlusIcon data-icon="inline-start" />
			Add provider
		</Button>
	{/if}
{/snippet}

<!-- Outlined, since adding a provider is the page's primary action -->
{#snippet addModelButton()}
	{#if canManage && data.providers.length > 0}
		<Button variant="outline" onclick={() => openAddModel()}>
			<PlusIcon data-icon="inline-start" />
			Add model
		</Button>
	{:else if canManage}
		<!-- A disabled button gets no pointer events or focus, so this one is only marked disabled and keeps the tooltip that says why -->
		<Tooltip.Root>
			<Tooltip.Trigger>
				{#snippet child({ props })}
					<Button {...props} variant="outline" softDisabled>
						<PlusIcon data-icon="inline-start" />
						Add model
					</Button>
				{/snippet}
			</Tooltip.Trigger>
			<Tooltip.Content>Add a provider first</Tooltip.Content>
		</Tooltip.Root>
	{/if}
{/snippet}

<!-- The notice keeps the general tab's spacing, the two sections keep more room between them -->
{#if !canManage}
	<div class="mb-6">
		<AdminOnlyNotice
			>Only admins of the workspace can change its providers and models.</AdminOnlyNotice
		>
	</div>
{/if}
<div class="flex flex-col gap-10">
	<section class="flex flex-col gap-3">
		<div class="flex flex-col gap-1">
			<h2 class="text-lg font-semibold">Providers</h2>
			<p class="text-muted-foreground text-sm leading-snug">The LLM APIs Umpteenth can call.</p>
		</div>
		<DataTable
			bind:this={providersTable}
			label="Providers"
			columns={providerColumns}
			fetchPage={fetchProviders}
			getRowId={(provider) => provider.id}
			defaultSort="name"
			urlPrefix="providers"
			searchPlaceholder="Search providers"
			actions={noProviders ? undefined : addProviderButton}
		>
			{#snippet empty()}
				<Empty.Root size="sm">
					<Empty.Header>
						<Empty.Media variant="icon">
							<CloudIcon />
						</Empty.Media>
						<Empty.Title>No providers</Empty.Title>
						<Empty.Description
							>Add Anthropic or an OpenAI-compatible API to run jobs.</Empty.Description
						>
					</Empty.Header>
					{#if canManage}
						<Empty.Content>
							{@render addProviderButton()}
						</Empty.Content>
					{/if}
				</Empty.Root>
			{/snippet}
		</DataTable>
	</section>

	<section class="flex flex-col gap-3">
		<div class="flex flex-col gap-1">
			<h2 class="text-lg font-semibold">Models</h2>
			<p class="text-muted-foreground text-sm leading-snug">
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
			actions={noModels ? undefined : addModelButton}
		>
			{#snippet empty()}
				<Empty.Root size="sm">
					<Empty.Header>
						<Empty.Media variant="icon">
							<BotIcon />
						</Empty.Media>
						<Empty.Title>No models</Empty.Title>
						<Empty.Description>
							{data.providers.length > 0
								? "Sync a provider's models, or add one by hand."
								: 'Add a provider first, then sync its models or add one by hand.'}
						</Empty.Description>
					</Empty.Header>
					<!-- Without a provider there is nothing to add a model to, and the providers panel above offers to add one -->
					{#if canManage && data.providers.length > 0}
						<Empty.Content>
							{@render addModelButton()}
						</Empty.Content>
					{/if}
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
