<script lang="ts">
	import type { User } from '$lib/api/types';
	import { openConfirmDialog } from '$lib/components/confirm-dialog';
	import DangerZone from '$lib/components/danger-zone.svelte';
	import WorkspaceService from '$lib/services/workspace-service';
	import { apiErrorToast } from '$lib/utils/error-util';
	import { tryCatch } from '$lib/utils/try-catch-util';
	import { enterWorkspace } from '$lib/utils/workspace-util';
	import LogOutIcon from '@lucide/svelte/icons/log-out';
	import Trash2Icon from '@lucide/svelte/icons/trash-2';
	import { toast } from 'svelte-sonner';

	let { user }: { user: User } = $props();

	const workspaceService = new WorkspaceService();

	const isOwner = $derived(user.workspace.role === 'owner');

	// Both actions move the session into another workspace, which is a new personal one when none is left
	function confirmLeave() {
		openConfirmDialog({
			title: `Leave ${user.workspace.name}`,
			message:
				"You lose access to its jobs, runs and secrets, and API tokens you created in it stop working. You'll need a new invite to come back.",
			confirm: {
				label: 'Leave workspace',
				destructive: true,
				action: async () => {
					const result = await tryCatch(workspaceService.leave());
					if (result.error) {
						apiErrorToast(result.error, 'Failed to leave the workspace');
						return;
					}
					toast.success(`Left ${user.workspace.name}`);
					await enterWorkspace();
				}
			}
		});
	}

	function confirmDelete() {
		openConfirmDialog({
			title: `Delete ${user.workspace.name}`,
			message:
				"Every job, run, secret, provider and MCP server in the workspace is deleted, and its members lose access. This can't be undone.",
			// Deleting a whole workspace asks for its name, so it can't be confirmed by reflex
			confirmText: user.workspace.name,
			confirm: {
				label: 'Delete workspace',
				destructive: true,
				action: async () => {
					const result = await tryCatch(workspaceService.delete());
					if (result.error) {
						apiErrorToast(result.error, 'Failed to delete the workspace');
						return;
					}
					toast.success(`Deleted ${user.workspace.name}`);
					await enterWorkspace();
				}
			}
		});
	}
</script>

<!-- The owner can't leave, since a workspace always has one, so they only get to delete it -->
{#snippet deleteDescription()}
	Removes every job, run, secret, provider and MCP server for all members. To leave instead, make
	someone else the owner on the <a href="/settings/members" class="underline underline-offset-3"
		>Members</a
	> tab.
{/snippet}

<DangerZone
	actions={isOwner
		? [
				{
					title: 'Delete this workspace',
					description: deleteDescription,
					label: 'Delete workspace',
					icon: Trash2Icon,
					onclick: confirmDelete
				}
			]
		: [
				{
					title: 'Leave this workspace',
					description:
						"Takes away your access to its jobs, runs and secrets. You'll need a new invite to come back.",
					label: 'Leave workspace',
					icon: LogOutIcon,
					onclick: confirmLeave
				}
			]}
/>
