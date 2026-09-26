<script lang="ts">
	import { invalidateAll } from '$app/navigation';
	import { page } from '$app/state';
	import type { WorkspaceInvite, WorkspaceMember, WorkspaceRole } from '$lib/api/types';
	import { openConfirmDialog } from '$lib/components/confirm-dialog';
	import {
		DataTable,
		RowActions,
		actionsColumn,
		renderComponent,
		renderSnippet
	} from '$lib/components/data-table';
	import RelativeTime from '$lib/components/relative-time.svelte';
	import * as Avatar from '$lib/components/ui/avatar';
	import { Badge } from '$lib/components/ui/badge';
	import { Button } from '$lib/components/ui/button';
	import * as Empty from '$lib/components/ui/empty';
	import * as Select from '$lib/components/ui/select';
	import * as Table from '$lib/components/ui/table';
	import WorkspaceService from '$lib/services/workspace-service';
	import { apiErrorToast } from '$lib/utils/error-util';
	import { tryCatch } from '$lib/utils/try-catch-util';
	import { hasRole, roleLabels } from '$lib/utils/workspace-util';
	import CrownIcon from '@lucide/svelte/icons/crown';
	import LinkIcon from '@lucide/svelte/icons/link';
	import MailIcon from '@lucide/svelte/icons/mail';
	import Trash2Icon from '@lucide/svelte/icons/trash-2';
	import UserMinusIcon from '@lucide/svelte/icons/user-minus';
	import UserPlusIcon from '@lucide/svelte/icons/user-plus';
	import UsersIcon from '@lucide/svelte/icons/users';
	import type { ColumnDef } from '@tanstack/table-core';
	import { onMount } from 'svelte';
	import { toast } from 'svelte-sonner';
	import Expiry from '../expiry.svelte';
	import InviteDialog from './invite-dialog.svelte';
	import InviteLinkDialog from './invite-link-dialog.svelte';

	const workspaceService = new WorkspaceService();

	const user = $derived(page.data.user!);
	const canManage = $derived(hasRole(user, 'admin'));
	const isOwner = $derived(user.workspace.role === 'owner');

	let dataTable: ReturnType<typeof DataTable<WorkspaceMember>> | undefined = $state();
	let invites = $state<WorkspaceInvite[]>([]);
	let inviteOpen = $state(false);
	let inviteUrl = $state<string | null>(null);

	// The same order as the admin users table, with the more telling last sign-in first
	const columns: ColumnDef<WorkspaceMember>[] = [
		{
			id: 'name',
			header: 'Member',
			meta: { sortKey: 'name', cellClass: 'w-full max-w-0' },
			cell: ({ row }) => renderSnippet(memberCell, row.original)
		},
		{
			accessorKey: 'role',
			header: 'Role',
			meta: { sortKey: 'role' },
			cell: ({ row }) => renderSnippet(roleCell, row.original)
		},
		{
			accessorKey: 'lastLoginAt',
			header: 'Last sign-in',
			meta: { sortKey: 'lastLoginAt', hideBelow: 'sm', cellClass: 'whitespace-nowrap' },
			cell: ({ row }) => renderSnippet(lastLoginCell, row.original)
		},
		{
			accessorKey: 'joinedAt',
			header: 'Joined',
			meta: { sortKey: 'joinedAt', hideBelow: 'md', cellClass: 'whitespace-nowrap' },
			cell: ({ row }) => renderComponent(RelativeTime, { value: row.original.joinedAt })
		},
		actionsColumn<WorkspaceMember>((member) => renderSnippet(actionsCell, member))
	];

	onMount(() => {
		void loadInvites();
	});

	function displayName(member: WorkspaceMember) {
		return member.name || member.email || 'Unknown user';
	}

	function initials(member: WorkspaceMember) {
		return displayName(member)
			.split(/[\s@.]+/)
			.filter(Boolean)
			.slice(0, 2)
			.map((part) => part[0]?.toUpperCase())
			.join('');
	}

	// Pending invites only exist when people can have several workspaces, and only admins see them
	async function loadInvites() {
		if (!canManage || !user.workspacesEnabled) {
			invites = [];
			return;
		}
		const result = await tryCatch(workspaceService.listInvites());
		if (result.error) {
			apiErrorToast(result.error, 'Failed to load the invites');
			return;
		}
		invites = result.data ?? [];
	}

	function onInvited(url: string | null) {
		inviteUrl = url;
		void loadInvites();
		void dataTable?.refresh();
	}

	// The role changes in place and goes back if the server refuses
	async function changeRole(member: WorkspaceMember, role: Exclude<WorkspaceRole, 'owner'>) {
		if (role === member.role) return;
		const previous = member.role;
		dataTable?.updateRow(member.userId, { role });
		const result = await tryCatch(workspaceService.updateMember(member.userId, role));
		if (result.error) {
			dataTable?.updateRow(member.userId, { role: previous });
			apiErrorToast(result.error, 'Failed to change the role');
			return;
		}
		toast.success(`${displayName(member)} is now ${roleLabels[role].toLowerCase()}`);

		// Changing your own role changes what this page lets you do
		if (member.userId === user.id) await invalidateAll();
	}

	function confirmTransfer(member: WorkspaceMember) {
		openConfirmDialog({
			title: `Make ${displayName(member)} the owner`,
			message:
				'The owner is the only one who can delete the workspace or hand it over. You stay in the workspace as an admin.',
			confirm: {
				label: 'Hand over ownership',
				destructive: true,
				action: async () => {
					const result = await tryCatch(workspaceService.transfer(member.userId));
					if (result.error) {
						apiErrorToast(result.error, 'Failed to hand over ownership');
						return;
					}
					toast.success(`${displayName(member)} now owns the workspace`);
					await invalidateAll();
					await dataTable?.refresh();
				}
			}
		});
	}

	function confirmRemove(member: WorkspaceMember) {
		openConfirmDialog({
			title: `Remove ${displayName(member)}`,
			message:
				'They lose access to the workspace right away, and API tokens they created stop working.',
			confirm: {
				label: 'Remove',
				destructive: true,
				action: async () => {
					const result = await tryCatch(workspaceService.removeMember(member.userId));
					if (result.error) {
						apiErrorToast(result.error, 'Failed to remove the member');
						return;
					}
					toast.success(`Removed ${displayName(member)}`);
					await dataTable?.refresh();
				}
			}
		});
	}

	function confirmRevoke(invite: WorkspaceInvite) {
		openConfirmDialog({
			title: 'Revoke invite',
			message: invite.email
				? `${invite.email} won't join when they next sign in.`
				: 'The link stops working right away.',
			confirm: {
				label: 'Revoke',
				destructive: true,
				action: async () => {
					const result = await tryCatch(workspaceService.deleteInvite(invite.id));
					if (result.error) {
						apiErrorToast(result.error, 'Failed to revoke the invite');
						return;
					}
					toast.success('Revoked the invite');
					await loadInvites();
				}
			}
		});
	}
</script>

<!-- Phones leave out the avatar, so the name and the role picker beside it both fit -->
{#snippet memberCell(member: WorkspaceMember)}
	<div class="flex min-w-0 items-center gap-3">
		<Avatar.Root class="hidden size-8 shrink-0 sm:flex">
			{#if member.picture}
				<Avatar.Image src={member.picture} alt="" referrerpolicy="no-referrer" />
			{/if}
			<Avatar.Fallback>{initials(member)}</Avatar.Fallback>
		</Avatar.Root>
		<div class="flex min-w-0 flex-col">
			<!-- A long name truncates on its own, so '(you)' always stays visible -->
			<span class="flex min-w-0 gap-1">
				<span class="truncate font-medium" title={displayName(member)}>{displayName(member)}</span>
				{#if member.userId === user.id}
					<span class="text-muted-foreground shrink-0">(you)</span>
				{/if}
			</span>
			{#if member.email && member.email !== displayName(member)}
				<span class="text-muted-foreground truncate text-sm" title={member.email}
					>{member.email}</span
				>
			{/if}
		</div>
		{#if member.deactivated}
			<Badge variant="secondary">Deactivated</Badge>
		{/if}
	</div>
{/snippet}

{#snippet roleCell(member: WorkspaceMember)}
	{#if member.role === 'owner' || !canManage}
		<Badge variant="secondary">{roleLabels[member.role]}</Badge>
	{:else}
		<Select.Root
			type="single"
			value={member.role}
			onValueChange={(value) => changeRole(member, value as Exclude<WorkspaceRole, 'owner'>)}
		>
			<Select.Trigger size="sm" class="w-24 sm:w-28" aria-label="Role of {displayName(member)}">
				{roleLabels[member.role]}
			</Select.Trigger>
			<Select.Content>
				<Select.Item value="member" label="Member" />
				<Select.Item value="admin" label="Admin" />
			</Select.Content>
		</Select.Root>
	{/if}
{/snippet}

{#snippet lastLoginCell(member: WorkspaceMember)}
	{#if member.lastLoginAt}
		<RelativeTime value={member.lastLoginAt} />
	{:else}
		<span class="text-muted-foreground">Never</span>
	{/if}
{/snippet}

{#snippet actionsCell(member: WorkspaceMember)}
	<!-- The owner changes by handing over, and people rejoin at sign-in while workspaces are off, so neither gets a remove action -->
	<RowActions
		name={displayName(member)}
		items={[
			isOwner &&
				member.role !== 'owner' && {
					label: 'Make owner',
					icon: CrownIcon,
					onSelect: () => confirmTransfer(member)
				},
			canManage &&
				user.workspacesEnabled &&
				member.role !== 'owner' &&
				member.userId !== user.id && {
					label: 'Remove from workspace',
					icon: UserMinusIcon,
					variant: 'destructive',
					onSelect: () => confirmRemove(member)
				}
		]}
	/>
{/snippet}

{#snippet inviteActions(invite: WorkspaceInvite)}
	<RowActions
		name={invite.email ?? 'invite link'}
		items={[
			{
				label: 'Revoke',
				icon: Trash2Icon,
				variant: 'destructive',
				onSelect: () => confirmRevoke(invite)
			}
		]}
	/>
{/snippet}

<div class="flex flex-col gap-4">
	<p class="text-muted-foreground text-sm leading-snug">
		{#if user.workspacesEnabled}
			Everyone in {user.workspace.name}. Admins manage members, providers and settings, and the
			owner can also delete the workspace.
		{:else}
			Everyone who signs in joins this workspace as a member. Admins manage members, providers and
			settings.
		{/if}
	</p>

	<DataTable
		bind:this={dataTable}
		label="Members"
		{columns}
		fetchPage={(query) => workspaceService.listMembers(query)}
		getRowId={(member) => member.userId}
		defaultSort="role"
		searchPlaceholder="Search members"
	>
		{#snippet actions()}
			{#if canManage && user.workspacesEnabled}
				<Button onclick={() => (inviteOpen = true)}>
					<UserPlusIcon data-icon="inline-start" />
					Invite member
				</Button>
			{/if}
		{/snippet}
		{#snippet empty()}
			<Empty.Root size="sm">
				<Empty.Header>
					<Empty.Media variant="icon">
						<UsersIcon />
					</Empty.Media>
					<Empty.Title>No members found</Empty.Title>
				</Empty.Header>
			</Empty.Root>
		{/snippet}
	</DataTable>
</div>

<!-- Invites are an admin's business, so they go away as soon as the viewer stops being one -->
{#if canManage && invites.length > 0}
	<!-- A section like the tables on the providers tab, framed the same as the members table above -->
	<section class="mt-10 flex flex-col gap-3">
		<div class="flex flex-col gap-1">
			<h2 class="text-lg font-semibold">Pending invites</h2>
			<p class="text-muted-foreground text-sm leading-snug">
				Email invites wait for a sign-in with a verified address, and links work once.
			</p>
		</div>
		<div class="bg-card ring-border overflow-hidden rounded-lg shadow-xs ring-1">
			<Table.Root aria-label="Pending invites">
				<Table.Header>
					<Table.Row>
						<Table.Head>Invite</Table.Head>
						<Table.Head>Role</Table.Head>
						<Table.Head class="hidden md:table-cell">Invited by</Table.Head>
						<Table.Head class="hidden sm:table-cell">Expires</Table.Head>
						<Table.Head class="w-0"><span class="sr-only">Actions</span></Table.Head>
					</Table.Row>
				</Table.Header>
				<Table.Body>
					{#each invites as invite (invite.id)}
						<Table.Row>
							<Table.Cell class="w-full max-w-0">
								<span class="flex min-w-0 items-center gap-2">
									{#if invite.email}
										<MailIcon class="text-muted-foreground size-4 shrink-0" />
										<span class="truncate" title={invite.email}>{invite.email}</span>
									{:else}
										<LinkIcon class="text-muted-foreground size-4 shrink-0" />
										<span class="truncate">Invite link</span>
									{/if}
								</span>
							</Table.Cell>
							<Table.Cell>
								<Badge variant="secondary">{roleLabels[invite.role]}</Badge>
							</Table.Cell>
							<Table.Cell class="hidden whitespace-nowrap md:table-cell">
								{invite.invitedBy ?? '—'}
							</Table.Cell>
							<Table.Cell class="hidden whitespace-nowrap sm:table-cell">
								<Expiry value={invite.expiresAt} />
							</Table.Cell>
							<Table.Cell class="w-0 text-right">{@render inviteActions(invite)}</Table.Cell>
						</Table.Row>
					{/each}
				</Table.Body>
			</Table.Root>
		</div>
	</section>
{/if}

<InviteDialog bind:open={inviteOpen} {onInvited} />
<InviteLinkDialog bind:url={inviteUrl} />
