<script lang="ts">
	import { isApiError } from '$lib/api/api-error';
	import type { WorkspaceRole } from '$lib/api/types';
	import FormInput from '$lib/components/form/form-input.svelte';
	import { Button } from '$lib/components/ui/button';
	import * as Dialog from '$lib/components/ui/dialog';
	import * as Field from '$lib/components/ui/field';
	import * as Select from '$lib/components/ui/select';
	import * as Tabs from '$lib/components/ui/tabs';
	import WorkspaceService from '$lib/services/workspace-service';
	import { apiErrorToast } from '$lib/utils/error-util';
	import { preventDefault } from '$lib/utils/event-util';
	import { createForm } from '$lib/utils/form-util';
	import { tryCatch } from '$lib/utils/try-catch-util';
	import { roleDescriptions, roleLabels } from '$lib/utils/workspace-util';
	import { toast } from 'svelte-sonner';
	import { z } from 'zod/v4';

	let {
		open = $bindable(false),
		onInvited
	}: {
		open?: boolean;
		// A link invite hands over its URL, which is only shown once
		onInvited: (linkUrl: string | null) => void;
	} = $props();

	const workspaceService = new WorkspaceService();

	type InviteRole = Exclude<WorkspaceRole, 'owner'>;
	const roles: InviteRole[] = ['member', 'admin'];
	const expiryOptions = [
		{ days: 1, label: '1 day' },
		{ days: 7, label: '7 days' },
		{ days: 30, label: '30 days' }
	];

	const form = createForm(
		z.object({ email: z.union([z.literal(''), z.email('Must be an email address')]) }),
		{ email: '' }
	);
	const inputs = form.inputs;
	// An invite goes to one verified address, or is a link that works once for whoever opens it
	let mode = $state<'email' | 'link'>('email');
	let role = $state<InviteRole>('member');
	let expiresInDays = $state('7');
	let isLoading = $state(false);

	const byEmail = $derived(mode === 'email');

	async function onSubmit() {
		const data = form.validate();
		if (!data) return;
		if (byEmail && !data.email) {
			form.setErrors([{ field: 'email', message: 'Required' }]);
			return;
		}

		isLoading = true;
		const result = await tryCatch(
			workspaceService.createInvite({
				email: byEmail ? data.email : undefined,
				role,
				expiresInDays: Number(expiresInDays)
			})
		);
		isLoading = false;
		if (result.error) {
			if (isApiError(result.error, 'validation_failed')) form.setErrors(result.error.fields);
			apiErrorToast(result.error, 'Failed to invite');
			return;
		}

		// An email invite always waits for a sign-in, even from someone who signed in before
		open = false;
		if (result.data.url) {
			onInvited(result.data.url);
		} else {
			toast.success(`${data.email} joins the next time they sign in`);
			onInvited(null);
		}
	}

	// A link has no address, so an address typed before switching can't block the link with its validation
	function changeMode(value: string) {
		mode = value === 'link' ? 'link' : 'email';
		if (mode === 'link') form.reset();
	}

	function reset() {
		form.reset();
		mode = 'email';
		role = 'member';
		expiresInDays = '7';
	}
</script>

<!-- Closing empties the form, so the next open starts fresh whatever was typed before -->
<Dialog.Root bind:open onOpenChangeComplete={(isOpen) => !isOpen && reset()}>
	<Dialog.Content class="sm:max-w-md">
		<Dialog.Header>
			<Dialog.Title>Invite to workspace</Dialog.Title>
			<Dialog.Description>
				Invite someone by their email address, or create a link to share with them.
			</Dialog.Description>
		</Dialog.Header>
		<form novalidate id="invite-form" onsubmit={preventDefault(onSubmit)}>
			<Field.Group>
				<Tabs.Root value={mode} onValueChange={changeMode}>
					<Tabs.List aria-label="Invite by">
						<Tabs.Trigger value="email">By email</Tabs.Trigger>
						<Tabs.Trigger value="link">Invite link</Tabs.Trigger>
					</Tabs.List>
				</Tabs.Root>
				{#if byEmail}
					<FormInput
						label="Email"
						type="email"
						placeholder="name@example.com"
						description="They join the next time they sign in with this verified address."
						optional={false}
						bind:input={$inputs.email}
					/>
				{:else}
					<p class="text-muted-foreground text-sm leading-snug">
						You get a link to share. It works once, for whoever opens it first and signs in.
					</p>
				{/if}
				<Field.Field>
					<Field.Label for="invite-role">Role</Field.Label>
					<Select.Root type="single" bind:value={role}>
						<Select.Trigger id="invite-role" class="w-full">{roleLabels[role]}</Select.Trigger>
						<Select.Content>
							{#each roles as r (r)}
								<Select.Item value={r} label={roleLabels[r]}>
									<span class="flex flex-col">
										<span>{roleLabels[r]}</span>
										<span class="text-muted-foreground text-xs">{roleDescriptions[r]}</span>
									</span>
								</Select.Item>
							{/each}
						</Select.Content>
					</Select.Root>
				</Field.Field>
				<Field.Field>
					<Field.Label for="invite-expiry">Expires after</Field.Label>
					<Select.Root type="single" bind:value={expiresInDays}>
						<Select.Trigger id="invite-expiry" class="w-full">
							{expiryOptions.find((o) => String(o.days) === expiresInDays)?.label}
						</Select.Trigger>
						<Select.Content>
							{#each expiryOptions as option (option.days)}
								<Select.Item value={String(option.days)} label={option.label} />
							{/each}
						</Select.Content>
					</Select.Root>
				</Field.Field>
			</Field.Group>
		</form>
		<Dialog.Footer>
			<Button variant="outline" onclick={() => (open = false)}>Cancel</Button>
			<Button type="submit" form="invite-form" {isLoading}>
				{byEmail ? 'Invite' : 'Create link'}
			</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
