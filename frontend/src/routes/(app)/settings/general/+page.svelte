<script lang="ts">
	import { invalidate, invalidateAll } from '$app/navigation';
	import type { WorkspaceSettingsUpdate } from '$lib/api/types';
	import AdminOnlyNotice from '$lib/components/workspaces/admin-only-notice.svelte';
	import SettingsService from '$lib/services/settings-service';
	import WorkspaceService from '$lib/services/workspace-service';
	import { hasRole } from '$lib/utils/workspace-util';
	import BudgetForm from './budget-form.svelte';
	import DangerZone from './danger-zone.svelte';
	import ModelsForm from './models-form.svelte';
	import NotificationsForm from './notifications-form.svelte';
	import SandboxDefaultsForm from './sandbox-defaults-form.svelte';
	import UsageForm from './usage-form.svelte';
	import WorkspaceForm from './workspace-form.svelte';

	let { data } = $props();

	const settingsService = new SettingsService();
	const workspaceService = new WorkspaceService();

	const canManage = $derived(hasRole(data.user, 'admin'));

	// Each card saves only its own fields, the PATCH endpoint leaves everything else untouched
	async function save(update: WorkspaceSettingsUpdate) {
		await settingsService.update(update);
	}

	// Every page reads the unit from the session's workspace, so reloading the session switches the whole app at once
	async function saveUsage(update: WorkspaceSettingsUpdate) {
		await settingsService.update(update);
		await invalidate('app:user');
	}

	// The name also shows in the switcher, which reads it from the session's user
	async function rename(name: string) {
		await workspaceService.rename(name);
		await invalidateAll();
	}
</script>

<div class="flex flex-col gap-6">
	{#if !canManage}
		<AdminOnlyNotice>Only admins of the workspace can change its settings.</AdminOnlyNotice>
	{/if}
	<!-- Members may look, so they get the saved values as text instead of controls they can't use -->
	{#if data.user.workspacesEnabled}
		{#key data.user.workspace.id}
			<WorkspaceForm name={data.user.workspace.name} readOnly={!canManage} onSave={rename} />
		{/key}
	{/if}
	<SandboxDefaultsForm settings={data.settings} readOnly={!canManage} onSave={save} />
	<ModelsForm settings={data.settings} models={data.models} readOnly={!canManage} onSave={save} />
	<BudgetForm settings={data.settings} readOnly={!canManage} onSave={save} />
	<UsageForm settings={data.settings} readOnly={!canManage} onSave={saveUsage} />
	<NotificationsForm
		settings={data.settings}
		secrets={data.secrets}
		readOnly={!canManage}
		onSave={save}
	/>
	{#if data.user.workspacesEnabled}
		<DangerZone user={data.user} />
	{/if}
</div>
