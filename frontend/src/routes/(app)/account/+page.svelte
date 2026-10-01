<script lang="ts">
	import { invalidate, invalidateAll } from '$app/navigation';
	import type { Passkey } from '$lib/api/types';
	import { openConfirmDialog } from '$lib/components/confirm-dialog';
	import { RowActions } from '$lib/components/data-table';
	import PageHeader from '$lib/components/page-header.svelte';
	import RelativeTime from '$lib/components/relative-time.svelte';
	import * as Alert from '$lib/components/ui/alert';
	import { Button } from '$lib/components/ui/button';
	import * as Table from '$lib/components/ui/table';
	import UserService from '$lib/services/user-service';
	import { apiErrorToast } from '$lib/utils/error-util';
	import { tryCatch } from '$lib/utils/try-catch-util';
	import KeyRoundIcon from '@lucide/svelte/icons/key-round';
	import PencilIcon from '@lucide/svelte/icons/pencil';
	import PlusIcon from '@lucide/svelte/icons/plus';
	import Trash2Icon from '@lucide/svelte/icons/trash-2';
	import TriangleAlertIcon from '@lucide/svelte/icons/triangle-alert';
	import { toast } from 'svelte-sonner';
	import ProfileForm from './profile-form.svelte';
	import RenamePasskeyDialog from './rename-passkey-dialog.svelte';

	let { data } = $props();

	const userService = new UserService();

	let adding = $state(false);
	let renaming = $state<Passkey | null>(null);

	// The menu and the members list read the name from the session's user
	async function saveProfile(name: string, email: string) {
		await userService.updateProfile({ name, email: email || undefined });
		await invalidate('app:user');
	}

	async function addPasskey() {
		adding = true;
		const result = await tryCatch(userService.addPasskey());
		adding = false;
		if (result.error) {
			apiErrorToast(result.error, 'Failed to add the passkey');
			return;
		}
		if (!result.data) return;
		toast.success(`Added "${result.data.name}"`);
		await invalidateAll();
	}

	function confirmDelete(passkey: Passkey) {
		openConfirmDialog({
			title: `Remove ${passkey.name}`,
			message:
				"You can no longer sign in with it. The device keeps it until you delete it there, but Umpteenth won't accept it.",
			confirm: {
				label: 'Remove',
				destructive: true,
				action: async () => {
					const result = await tryCatch(userService.deletePasskey(passkey.id));
					if (result.error) {
						apiErrorToast(result.error, 'Failed to remove the passkey');
						return;
					}
					toast.success(`Removed "${passkey.name}"`);
					await invalidateAll();
				}
			}
		});
	}
</script>

<svelte:head>
	<title>Account · Umpteenth</title>
</svelte:head>

<PageHeader title="Account" description="Your profile and how you sign in." />

{#if data.user.passkeyAccount}
	<div class="flex flex-col gap-10">
		{#key data.user.id}
			<ProfileForm name={data.user.name ?? ''} email={data.user.email ?? ''} onSave={saveProfile} />
		{/key}

		<!-- A section like the pending invites, framed the same as the tables elsewhere in settings -->
		<section class="flex flex-col gap-3">
			<div class="flex flex-wrap items-end justify-between gap-3">
				<div class="flex flex-col gap-1">
					<h2 class="text-lg font-semibold">Passkeys</h2>
					<p class="text-muted-foreground text-sm leading-snug">
						Your password manager, phone or security key keeps them, and unlocks them with your
						fingerprint, face or PIN.
					</p>
				</div>
				<Button variant="outline" isLoading={adding} onclick={addPasskey}>
					<PlusIcon data-icon="inline-start" />
					Add passkey
				</Button>
			</div>
			{#if data.passkeys.length === 0}
				<!-- Someone who came in through a sign-in link has no way back in without one -->
				<Alert.Root variant="warning">
					<TriangleAlertIcon />
					<Alert.Title>Add a passkey to sign in again</Alert.Title>
					<Alert.Description>
						Your account has no passkey yet. Without one, you need a new sign-in link from an
						instance admin once this session ends.
					</Alert.Description>
				</Alert.Root>
			{:else}
				<div class="bg-card ring-border overflow-hidden rounded-lg shadow-xs ring-1">
					<Table.Root aria-label="Passkeys">
						<Table.Header>
							<Table.Row>
								<Table.Head>Name</Table.Head>
								<Table.Head class="hidden md:table-cell">Added</Table.Head>
								<Table.Head class="hidden sm:table-cell">Last used</Table.Head>
								<Table.Head class="w-0"><span class="sr-only">Actions</span></Table.Head>
							</Table.Row>
						</Table.Header>
						<Table.Body>
							{#each data.passkeys as passkey (passkey.id)}
								<Table.Row>
									<Table.Cell class="w-full max-w-0">
										<span class="flex min-w-0 items-center gap-2">
											<KeyRoundIcon class="text-muted-foreground size-4 shrink-0" />
											<span class="truncate font-medium" title={passkey.name}>{passkey.name}</span>
										</span>
									</Table.Cell>
									<Table.Cell class="hidden whitespace-nowrap md:table-cell">
										<RelativeTime value={passkey.createdAt} />
									</Table.Cell>
									<Table.Cell class="hidden whitespace-nowrap sm:table-cell">
										{#if passkey.lastUsedAt}
											<RelativeTime value={passkey.lastUsedAt} />
										{:else}
											<span class="text-muted-foreground">Never</span>
										{/if}
									</Table.Cell>
									<!-- The last passkey stays, since without one only an admin's sign-in link gets the account back in -->
									<Table.Cell class="w-0 text-right">
										<RowActions
											name={passkey.name}
											items={[
												{ label: 'Rename', icon: PencilIcon, onSelect: () => (renaming = passkey) },
												data.passkeys.length > 1 && {
													label: 'Remove',
													icon: Trash2Icon,
													variant: 'destructive',
													onSelect: () => confirmDelete(passkey)
												}
											]}
										/>
									</Table.Cell>
								</Table.Row>
							{/each}
						</Table.Body>
					</Table.Root>
				</div>
			{/if}
		</section>
	</div>
{:else}
	<p class="text-muted-foreground text-sm leading-snug">
		Your sign-in provider manages your name, email address and how you sign in.
	</p>
{/if}

<RenamePasskeyDialog bind:passkey={renaming} onRenamed={() => invalidateAll()} />
