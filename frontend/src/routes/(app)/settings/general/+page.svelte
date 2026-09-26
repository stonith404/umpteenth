<script lang="ts">
	import type { WorkspaceSettingsUpdate } from '$lib/api/types';
	import SettingsService from '$lib/services/settings-service';
	import BudgetForm from './budget-form.svelte';
	import ModelsForm from './models-form.svelte';
	import NotificationsForm from './notifications-form.svelte';
	import SandboxDefaultsForm from './sandbox-defaults-form.svelte';
	import SandboxStatus from './sandbox-status.svelte';

	let { data } = $props();

	const settingsService = new SettingsService();

	// Each card saves only its own fields, the PATCH endpoint leaves everything else untouched
	// Errors propagate to the unsaved-changes bar, which shows them and keeps the edits
	async function save(update: WorkspaceSettingsUpdate) {
		await settingsService.update(update);
	}
</script>

<div class="flex flex-col gap-6">
	<SandboxDefaultsForm settings={data.settings} onSave={save} />
	<ModelsForm settings={data.settings} models={data.models} onSave={save} />
	<BudgetForm settings={data.settings} onSave={save} />
	<NotificationsForm settings={data.settings} secrets={data.secrets} onSave={save} />
	<SandboxStatus system={data.system} />
</div>
