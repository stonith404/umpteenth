<script lang="ts">
	import type { AdminUser, AdminUserCreated, SignInLink } from '$lib/api/types';
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
	import AdminService from '$lib/services/admin-service';
	import { apiErrorToast } from '$lib/utils/error-util';
	import { tryCatch } from '$lib/utils/try-catch-util';
	import LinkIcon from '@lucide/svelte/icons/link';
	import ShieldIcon from '@lucide/svelte/icons/shield';
	import ShieldOffIcon from '@lucide/svelte/icons/shield-off';
	import UserCheckIcon from '@lucide/svelte/icons/user-check';
	import UserPlusIcon from '@lucide/svelte/icons/user-plus';
	import UserXIcon from '@lucide/svelte/icons/user-x';
	import UsersIcon from '@lucide/svelte/icons/users';
	import type { ColumnDef } from '@tanstack/table-core';
	import { toast } from 'svelte-sonner';
	import AddUserDialog from './add-user-dialog.svelte';
	import SignInLinkDialog from './sign-in-link-dialog.svelte';

	let { data } = $props();

	const adminService = new AdminService();

	let dataTable: ReturnType<typeof DataTable<AdminUser>> | undefined = $state();
	let addOpen = $state(false);
	// The link only shows once, together with the name of the account it signs in to
	let signInLink = $state<SignInLink | null>(null);
	let signInLinkName = $state('');

	const columns: ColumnDef<AdminUser>[] = [
		{
			id: 'name',
			header: 'User',
			meta: { sortKey: 'name', cellClass: 'w-full max-w-0' },
			cell: ({ row }) => renderSnippet(userCell, row.original)
		},
		{
			accessorKey: 'provider',
			header: 'Signs in with',
			meta: { hideBelow: 'lg', cellClass: 'whitespace-nowrap' },
			cell: ({ row }) => renderSnippet(providerCell, row.original)
		},
		{
			accessorKey: 'workspaceCount',
			header: 'Workspaces',
			meta: { hideBelow: 'xl', align: 'right', cellClass: 'numeric' }
		},
		{
			accessorKey: 'lastLoginAt',
			header: 'Last sign-in',
			meta: { sortKey: 'lastLoginAt', hideBelow: 'sm', cellClass: 'whitespace-nowrap' },
			cell: ({ row }) => renderSnippet(lastLoginCell, row.original)
		},
		{
			accessorKey: 'createdAt',
			header: 'Joined',
			meta: { sortKey: 'createdAt', hideBelow: 'xl', cellClass: 'whitespace-nowrap' },
			cell: ({ row }) => renderComponent(RelativeTime, { value: row.original.createdAt })
		},
		actionsColumn<AdminUser>((u) => renderSnippet(actionsCell, u))
	];

	function displayName(u: AdminUser) {
		return u.name || u.email || 'Unknown user';
	}

	function initials(u: AdminUser) {
		return displayName(u)
			.split(/[\s@.]+/)
			.filter(Boolean)
			.slice(0, 2)
			.map((part) => part[0]?.toUpperCase())
			.join('');
	}

	async function setDeactivated(u: AdminUser, deactivated: boolean) {
		const result = await tryCatch(adminService.setDeactivated(u.id, deactivated));
		if (result.error) {
			apiErrorToast(result.error, 'Failed to update the user');
			return;
		}
		toast.success(deactivated ? `Deactivated ${displayName(u)}` : `Reactivated ${displayName(u)}`);
		dataTable?.updateRow(u.id, { deactivated });
	}

	function onCreated(created: AdminUserCreated) {
		signInLinkName = displayName(created.user);
		signInLink = created.signInLink;
		void dataTable?.refresh();
	}

	// A new link replaces the one before, so an older link that went astray stops working
	async function createSignInLink(u: AdminUser) {
		const result = await tryCatch(adminService.createSignInLink(u.id));
		if (result.error) {
			apiErrorToast(result.error, 'Failed to create the sign-in link');
			return;
		}
		signInLinkName = displayName(u);
		signInLink = result.data;
	}

	async function setAdmin(u: AdminUser, isAdmin: boolean) {
		const result = await tryCatch(adminService.setAdmin(u.id, isAdmin));
		if (result.error) {
			apiErrorToast(result.error, 'Failed to update the user');
			return;
		}
		toast.success(
			isAdmin
				? `${displayName(u)} is now an instance admin`
				: `${displayName(u)} is no longer an instance admin`
		);
		dataTable?.updateRow(u.id, { isAdmin });
	}

	function confirmDeactivate(u: AdminUser) {
		openConfirmDialog({
			title: `Deactivate ${displayName(u)}`,
			message:
				'They are signed out everywhere, can no longer sign in, and their API tokens stop working. Their memberships are kept for when you reactivate them.',
			confirm: {
				label: 'Deactivate',
				destructive: true,
				action: () => setDeactivated(u, true)
			}
		});
	}
</script>

<!-- Like the members table, phones leave out the avatar to keep the room for the name -->
<!-- Roles and markers are neutral badges everywhere in settings and admin, since tinted badges read as statuses -->
{#snippet userCell(u: AdminUser)}
	<div class="flex min-w-0 items-center gap-3">
		<Avatar.Root class="hidden size-8 shrink-0 sm:flex">
			{#if u.picture}
				<Avatar.Image src={u.picture} alt="" referrerpolicy="no-referrer" />
			{/if}
			<Avatar.Fallback>{initials(u)}</Avatar.Fallback>
		</Avatar.Root>
		<div class="flex min-w-0 flex-col">
			<span class="truncate font-medium" title={displayName(u)}>{displayName(u)}</span>
			{#if u.email && u.email !== displayName(u)}
				<span class="text-muted-foreground truncate text-sm" title={u.email}>{u.email}</span>
			{/if}
		</div>
		{#if u.isAdmin}
			<Badge variant="secondary">Admin</Badge>
		{/if}
		{#if u.deactivated}
			<Badge variant="secondary">Deactivated</Badge>
		{/if}
	</div>
{/snippet}

<!-- The configured provider's name reads better than its issuer URL, which stays in the tooltip -->
{#snippet providerCell(u: AdminUser)}
	<span class="text-muted-foreground" title={u.issuer}>{u.provider || u.issuer}</span>
{/snippet}

{#snippet lastLoginCell(u: AdminUser)}
	{#if u.lastLoginAt}
		<RelativeTime value={u.lastLoginAt} />
	{:else}
		<span class="text-muted-foreground">Never</span>
	{/if}
{/snippet}

<!-- Nobody locks themselves out, so their own row has no actions -->
<!-- Sign-in links and admin rights only apply to passkey accounts, since sign-in providers manage their own -->
{#snippet actionsCell(u: AdminUser)}
	{#if u.id !== data.user.id}
		<RowActions
			name={displayName(u)}
			items={[
				u.passkeyAccount &&
					!u.deactivated && {
						label: 'Create sign-in link',
						icon: LinkIcon,
						onSelect: () => createSignInLink(u)
					},
				u.passkeyAccount &&
					(u.isAdmin
						? {
								label: 'Remove instance admin',
								icon: ShieldOffIcon,
								onSelect: () => setAdmin(u, false)
							}
						: {
								label: 'Make instance admin',
								icon: ShieldIcon,
								onSelect: () => setAdmin(u, true)
							}),
				u.deactivated
					? { label: 'Reactivate', icon: UserCheckIcon, onSelect: () => setDeactivated(u, false) }
					: {
							label: 'Deactivate',
							icon: UserXIcon,
							variant: 'destructive',
							onSelect: () => confirmDeactivate(u)
						}
			]}
		/>
	{/if}
{/snippet}

<div class="flex flex-col gap-4">
	<p class="text-muted-foreground text-sm leading-snug">
		The admin options of the sign-in providers decide on the instance admins among their accounts at
		every sign-in, and you decide on passkey accounts here. Deactivating a user locks them out
		without removing their memberships.
	</p>

	<DataTable
		bind:this={dataTable}
		label="Users"
		{columns}
		fetchPage={(query) => adminService.listUsers(query)}
		getRowId={(u) => u.id}
		defaultSort="name"
		searchPlaceholder="Search users"
	>
		{#snippet actions()}
			{#if data.passkeys}
				<Button onclick={() => (addOpen = true)}>
					<UserPlusIcon data-icon="inline-start" />
					Add user
				</Button>
			{/if}
		{/snippet}
		{#snippet empty()}
			<Empty.Root size="sm">
				<Empty.Header>
					<Empty.Media variant="icon">
						<UsersIcon />
					</Empty.Media>
					<Empty.Title>No users found</Empty.Title>
				</Empty.Header>
			</Empty.Root>
		{/snippet}
	</DataTable>
</div>

<AddUserDialog bind:open={addOpen} {onCreated} />
<SignInLinkDialog bind:link={signInLink} name={signInLinkName} />
