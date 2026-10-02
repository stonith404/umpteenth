<script lang="ts">
	import { invalidate } from '$app/navigation';
	import type { Job } from '#lib/api/types.js';
	import CopyButton from '#lib/components/copy-button.svelte';
	import { openConfirmDialog } from '#lib/components/confirm-dialog/index.js';
	import * as Alert from '#lib/components/ui/alert/index.js';
	import { Badge } from '#lib/components/ui/badge/index.js';
	import { Button } from '#lib/components/ui/button/index.js';
	import * as Card from '#lib/components/ui/card/index.js';
	import * as Dialog from '#lib/components/ui/dialog/index.js';
	import * as Field from '#lib/components/ui/field/index.js';
	import * as InputGroup from '#lib/components/ui/input-group/index.js';
	import JobService from '#lib/services/job-service.js';
	import { apiErrorToast } from '#lib/utils/error-util.js';
	import { tryCatch } from '#lib/utils/try-catch-util.js';
	import KeyRoundIcon from '@lucide/svelte/icons/key-round';
	import TriangleAlertIcon from '@lucide/svelte/icons/triangle-alert';

	let { job }: { job: Pick<Job, 'id' | 'hasWebhookToken'> } = $props();

	const jobService = new JobService();

	// The backend returns a path, the public URL is where the browser reached the app
	const url = $derived(`${window.location.origin}/hooks/${job.id}`);

	let token = $state<string | null>(null);
	let isLoading = $state(false);

	const curl = $derived(
		`curl -X POST ${url} \\\n  -H "Authorization: Bearer ${token ?? '<token>'}" \\\n  -H "Content-Type: application/json" \\\n  -d '{"example": "input"}'`
	);

	async function rotate() {
		isLoading = true;
		const result = await tryCatch(jobService.rotateWebhookToken(job.id));
		isLoading = false;
		if (result.error) {
			apiErrorToast(result.error, 'Failed to create a webhook token');
			return;
		}
		token = result.data.token;
		await invalidate('app:job');
	}

	function onRotate() {
		if (!job.hasWebhookToken) {
			void rotate();
			return;
		}
		openConfirmDialog({
			title: 'Rotate the webhook token',
			message: 'The current token stops working right away, so update every system that calls it.',
			confirm: { label: 'Rotate', destructive: true, action: rotate }
		});
	}
</script>

<Card.Root>
	<Card.Header>
		<Card.Title>Webhook</Card.Title>
		<Card.Description>
			Other systems start runs with a POST request. The request body becomes the run's
			<code class="font-mono text-xs">/ump/input.json</code>.
		</Card.Description>
		<Card.Action>
			{#if job.hasWebhookToken}
				<Badge variant="secondary">Token set</Badge>
			{:else}
				<Badge variant="outline">No token</Badge>
			{/if}
		</Card.Action>
	</Card.Header>
	<Card.Content>
		<div class="flex flex-col items-start gap-4">
			<Field.Field>
				<Field.Label for="webhook-url">URL</Field.Label>
				<InputGroup.Root>
					<InputGroup.Input
						id="webhook-url"
						value={url}
						readonly
						mono="xs"
						onfocus={(e) => e.currentTarget.select()}
					/>
					<InputGroup.Addon align="inline-end">
						<CopyButton value={url} label="Copy webhook URL" />
					</InputGroup.Addon>
				</InputGroup.Root>
				<Field.Description>
					Requests need the job's token as <code class="font-mono text-xs"
						>Authorization: Bearer &lt;token&gt;</code
					> and are rate-limited.
				</Field.Description>
			</Field.Field>
			<!-- The token action stays in the content panel with the URL it belongs to, like every other card's controls -->
			<Button variant="outline" size="sm" onclick={onRotate} {isLoading}>
				<KeyRoundIcon data-icon="inline-start" />
				{job.hasWebhookToken ? 'Rotate token' : 'Generate token'}
			</Button>
		</div>
	</Card.Content>
</Card.Root>

<Dialog.Root open={token !== null} onOpenChange={(open) => !open && (token = null)}>
	<Dialog.Content class="sm:max-w-xl" onOpenAutoFocus={(e) => e.preventDefault()}>
		<Dialog.Header>
			<Dialog.Title>Webhook token</Dialog.Title>
			<Dialog.Description>Send it as a bearer token when calling the webhook.</Dialog.Description>
		</Dialog.Header>
		{#if token}
			<Field.Field>
				<Field.Label for="webhook-token">Token</Field.Label>
				<InputGroup.Root>
					<InputGroup.Input
						id="webhook-token"
						value={token}
						readonly
						mono="xs"
						onfocus={(e) => e.currentTarget.select()}
					/>
					<InputGroup.Addon align="inline-end">
						<CopyButton value={token} label="Copy token" />
					</InputGroup.Addon>
				</InputGroup.Root>
			</Field.Field>
			<Field.Field>
				<Field.Label>Example</Field.Label>
				<div class="relative">
					<pre
						class="bg-muted/50 overflow-x-auto rounded-lg p-4 pr-12 font-mono text-xs leading-5">{curl}</pre>
					<div class="absolute top-2 right-2">
						<CopyButton value={curl} label="Copy example" />
					</div>
				</div>
			</Field.Field>
			<Alert.Root variant="warning">
				<TriangleAlertIcon />
				<Alert.Description>
					Copy the token now. For security reasons it won't be shown again.
				</Alert.Description>
			</Alert.Root>
		{/if}
		<Dialog.Footer>
			<Button onclick={() => (token = null)}>Done</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
