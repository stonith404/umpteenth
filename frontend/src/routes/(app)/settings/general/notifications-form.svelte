<script lang="ts">
	import type { Secret, WorkspaceSettings, WorkspaceSettingsUpdate } from '#lib/api/types.js';
	import FormCard from '#lib/components/form/form-card.svelte';
	import FormInput from '#lib/components/form/form-input.svelte';
	import { Button } from '#lib/components/ui/button/index.js';
	import { Checkbox } from '#lib/components/ui/checkbox/index.js';
	import * as Field from '#lib/components/ui/field/index.js';
	import * as Select from '#lib/components/ui/select/index.js';
	import * as Tooltip from '#lib/components/ui/tooltip/index.js';
	import SettingsService from '#lib/services/settings-service.js';
	import { apiErrorToast } from '#lib/utils/error-util.js';
	import { createForm } from '#lib/utils/form-util.js';
	import { tryCatch } from '#lib/utils/try-catch-util.js';
	import SendIcon from '@lucide/svelte/icons/send';
	import { toast } from 'svelte-sonner';
	import { z } from 'zod/v4';
	import ReadOnlyValues from './read-only-values.svelte';

	let {
		settings,
		secrets,
		readOnly = false,
		onSave
	}: {
		settings: WorkspaceSettings;
		// Null when the secrets couldn't be loaded, which leaves the signing secret as it is
		secrets: Secret[] | null;
		// Shows the saved values as text, for members who may look but not change them
		readOnly?: boolean;
		onSave: (update: WorkspaceSettingsUpdate) => Promise<void>;
	} = $props();

	const EVENTS = [
		{ value: 'run.failed', label: 'A run fails or times out' },
		{
			value: 'run.fell_back',
			label: "A scripted run's main script fails and the agent takes over"
		},
		{ value: 'job.demoted', label: 'A job is demoted to Assisted after two fallbacks in a row' }
	] as const;
	type NotifyEvent = (typeof EVENTS)[number]['value'];

	const formSchema = z.object({
		notifyWebhookUrl: z.union([z.literal(''), z.url('Must be a URL')]),
		notifyOn: z.array(z.enum(['run.failed', 'run.fell_back', 'job.demoted'])),
		notifySecret: z.string()
	});

	const form = createForm(formSchema, {
		notifyWebhookUrl: settings.notifyWebhookUrl ?? '',
		notifyOn: settings.notifyOn ?? [],
		notifySecret: settings.notifySecret ?? ''
	});
	const inputs = form.inputs;

	// The test sends to the saved URL, which the page's settings only learn about after a reload
	let savedUrl = $state(settings.notifyWebhookUrl ?? '');
	async function save(data: z.infer<typeof formSchema>) {
		await onSave(data);
		savedUrl = data.notifyWebhookUrl;
	}

	// The list keeps the order of EVENTS, so ticking an event off and on again leaves the form unchanged
	function toggle(event: NotifyEvent, on: boolean) {
		const current = $inputs.notifyOn.value;
		const next = on ? [...current, event] : current.filter((e) => e !== event);
		$inputs.notifyOn.value = EVENTS.map((e) => e.value).filter((v) => next.includes(v));
	}

	const settingsService = new SettingsService();
	let testing = $state(false);

	// The test posts to the saved webhook, so an edited URL only takes part once its card is saved
	async function sendTest() {
		testing = true;
		const result = await tryCatch(settingsService.testNotification());
		testing = false;
		if (result.error) {
			apiErrorToast(result.error, 'The test notification was not delivered');
			return;
		}
		toast.success('Sent a test notification');
	}
</script>

<FormCard
	title="Notifications"
	description="Posts a JSON message to a webhook. Slack and Discord webhook URLs work as they are."
	{readOnly}
	dirty={form.isDirty()}
	saving={form.saving}
	onsubmit={() => form.submit(save)}
>
	{#snippet actions()}
		{#if savedUrl}
			<Button variant="outline" size="sm" disabled={testing} onclick={sendTest}>
				<SendIcon data-icon="inline-start" />
				Send test
			</Button>
		{:else}
			<!-- A disabled button gets no pointer events or focus, so this one is only marked disabled and keeps the tooltip that says why -->
			<Tooltip.Root>
				<Tooltip.Trigger>
					{#snippet child({ props })}
						<Button {...props} variant="outline" size="sm" softDisabled>
							<SendIcon data-icon="inline-start" />
							Send test
						</Button>
					{/snippet}
				</Tooltip.Trigger>
				<Tooltip.Content>Save a webhook URL first</Tooltip.Content>
			</Tooltip.Root>
		{/if}
	{/snippet}

	{#if readOnly}
		<ReadOnlyValues
			items={[
				{
					label: 'Webhook URL',
					value: settings.notifyWebhookUrl,
					empty: 'Not set, notifications are off',
					mono: true
				},
				{ label: 'Signing secret', value: settings.notifySecret, empty: 'Unsigned', mono: true },
				{
					label: 'Notify when',
					value: EVENTS.filter((e) => settings.notifyOn?.includes(e.value))
						.map((e) => e.label)
						.join('\n'),
					empty: 'Nothing'
				}
			]}
			class="[&_dd]:whitespace-pre-line"
		/>
	{:else}
		<Field.Group>
			<div class="grid grid-cols-1 gap-x-6 gap-y-7 md:grid-cols-2">
				<FormInput
					label="Webhook URL"
					placeholder="https://hooks.slack.com/services/…"
					description="Leave empty to turn notifications off."
					bind:input={$inputs.notifyWebhookUrl}
				/>
				<FormInput
					label="Signing secret"
					labelFor="notify-secret"
					description={secrets
						? 'Signs every message with an X-Umpteenth-Signature HMAC-SHA256 header, so the receiver can check where it came from.'
						: "The secrets couldn't be loaded, so the signing secret can't be changed right now."}
					input={$inputs.notifySecret}
				>
					<Select.Root type="single" bind:value={$inputs.notifySecret.value} disabled={!secrets}>
						<Select.Trigger id="notify-secret" class="w-full">
							{$inputs.notifySecret.value || 'Unsigned'}
						</Select.Trigger>
						<Select.Content>
							<Select.Item value="" label="Unsigned" />
							{#each secrets ?? [] as secret (secret.id)}
								<Select.Item value={secret.name} label={secret.name} />
							{/each}
						</Select.Content>
					</Select.Root>
				</FormInput>
			</div>
			<Field.Set>
				<Field.Legend variant="label">Notify when</Field.Legend>
				<Field.Group data-slot="checkbox-group">
					{#each EVENTS as event (event.value)}
						<Field.Field orientation="horizontal">
							<Checkbox
								id="notify-{event.value}"
								checked={$inputs.notifyOn.value.includes(event.value)}
								onCheckedChange={(checked) => toggle(event.value, checked === true)}
							/>
							<Field.Label for="notify-{event.value}" variant="choice">{event.label}</Field.Label>
						</Field.Field>
					{/each}
				</Field.Group>
			</Field.Set>
		</Field.Group>
	{/if}
</FormCard>
