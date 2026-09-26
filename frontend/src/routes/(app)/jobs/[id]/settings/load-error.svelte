<script lang="ts" module>
	// The settings tab's own requests, reloaded by 'Try again' without reloading the job
	export const JOB_SETTINGS_DEPENDENCY = 'app:job-settings';
</script>

<script lang="ts">
	import { invalidate } from '$app/navigation';
	import * as Alert from '$lib/components/ui/alert';
	import { Button } from '$lib/components/ui/button';
	import { getErrorMessage } from '$lib/utils/error-util';
	import CircleAlertIcon from '@lucide/svelte/icons/circle-alert';
	import RotateCwIcon from '@lucide/svelte/icons/rotate-cw';

	let { title, error }: { title: string; error: unknown } = $props();

	let retrying = $state(false);

	// API messages rarely end with a period, and the alert reads as a sentence
	const message = $derived(getErrorMessage(error).replace(/([^.!?])$/, '$1.'));

	async function retry() {
		retrying = true;
		await invalidate(JOB_SETTINGS_DEPENDENCY);
		retrying = false;
	}
</script>

<!-- Shown inside the card whose data failed to load, so the rest of the settings stay usable -->
<Alert.Root variant="destructive">
	<CircleAlertIcon />
	<Alert.Title>{title}</Alert.Title>
	<Alert.Description>
		<div class="flex flex-col items-start gap-2">
			<span>{message}</span>
			<Button variant="outline" size="xs" isLoading={retrying} onclick={retry}>
				<RotateCwIcon data-icon="inline-start" />
				Try again
			</Button>
		</div>
	</Alert.Description>
</Alert.Root>
