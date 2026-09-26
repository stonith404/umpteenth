<script lang="ts">
	import type { Secret, WorkspaceSettings, WorkspaceSettingsUpdate } from '$lib/api/types';
	import FormInput from '$lib/components/form/form-input.svelte';
	import { Button } from '$lib/components/ui/button';
	import * as Card from '$lib/components/ui/card';
	import { Checkbox } from '$lib/components/ui/checkbox';
	import * as Field from '$lib/components/ui/field';
	import * as Select from '$lib/components/ui/select';
	import SettingsService from '$lib/services/settings-service';
	import { apiErrorToast } from '$lib/utils/error-util';
	import { createForm } from '$lib/utils/form-util';
	import { tryCatch } from '$lib/utils/try-catch-util';
	import { trackFormChanges } from '$lib/utils/unsaved-changes-util.svelte';
	import SendIcon from '@lucide/svelte/icons/send';
	import { toast } from 'svelte-sonner';
	import { z } from 'zod/v4';

	let {
		settings,
		secrets,
		onSave
	}: {
		settings: WorkspaceSettings;
		secrets: Secret[];
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
		notifyOn: (settings.notifyOn ?? []) as NotifyEvent[],
		notifySecret: settings.notifySecret ?? ''
	});
	const inputs = form.inputs;

	// The test sends to the saved URL, which the page's settings only learn about after a reload
	let savedUrl = $state(settings.notifyWebhookUrl ?? '');
	trackFormChanges(
		() => form,
		async (data) => {
			await onSave(data);
			savedUrl = data.notifyWebhookUrl;
		}
	);

	// The list keeps the order of EVENTS, so ticking an event off and on again leaves the form unchanged
	function toggle(event: NotifyEvent, on: boolean) {
		const current = $inputs.notifyOn.value;
		const next = on ? [...current, event] : current.filter((e) => e !== event);
		$inputs.notifyOn.value = EVENTS.map((e) => e.value).filter((v) => next.includes(v));
	}

	const settingsService = new SettingsService();
	let testing = $state(false);

	// The test uses the saved settings, so unsaved edits have to be saved first
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

<Card.Root>
	<Card.Header>
		<Card.Title>Notifications</Card.Title>
		<Card.Description>
			Posts a JSON message to a webhook. Slack and Discord webhook URLs work as they are.
		</Card.Description>
		<Card.Action>
			<Button variant="outline" size="sm" disabled={testing || !savedUrl} onclick={sendTest}>
				<SendIcon data-icon="inline-start" />
				Send test
			</Button>
		</Card.Action>
	</Card.Header>
	<Card.Content>
		<Field.Group>
			<FormInput
				label="Webhook URL"
				placeholder="https://hooks.slack.com/services/…"
				description="Leave empty to turn notifications off."
				bind:input={$inputs.notifyWebhookUrl}
			/>
			<Field.Set>
				<Field.Legend variant="label">Notify when</Field.Legend>
				<Field.Group data-slot="checkbox-group" class="gap-3">
					{#each EVENTS as event (event.value)}
						<Field.Field orientation="horizontal">
							<Checkbox
								id="notify-{event.value}"
								checked={$inputs.notifyOn.value.includes(event.value)}
								onCheckedChange={(checked) => toggle(event.value, checked === true)}
							/>
							<Field.Label for="notify-{event.value}" class="font-normal">{event.label}</Field.Label
							>
						</Field.Field>
					{/each}
				</Field.Group>
			</Field.Set>
			<FormInput
				label="Signing secret"
				labelFor="notify-secret"
				description="Signs every message with an X-Umpteenth-Signature HMAC-SHA256 header, so the receiver can check where it came from."
				input={{ ...$inputs.notifySecret, required: false }}
			>
				<Select.Root type="single" bind:value={$inputs.notifySecret.value}>
					<Select.Trigger id="notify-secret" class="w-full md:w-80">
						{$inputs.notifySecret.value || 'Unsigned'}
					</Select.Trigger>
					<Select.Content>
						<Select.Item value="" label="Unsigned" />
						{#each secrets as secret (secret.id)}
							<Select.Item value={secret.name} label={secret.name} />
						{/each}
					</Select.Content>
				</Select.Root>
			</FormInput>
		</Field.Group>
	</Card.Content>
</Card.Root>
