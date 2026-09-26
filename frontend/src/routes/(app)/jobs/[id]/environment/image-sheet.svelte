<script lang="ts">
	import type { JobImageDetail } from '$lib/api/types';
	import CodeEditor from '$lib/components/code/code-editor.svelte';
	import CopyButton from '$lib/components/copy-button.svelte';
	import ImageStatusBadge, {
		isImageBuilding
	} from '$lib/components/jobs/image-status-badge.svelte';
	import RelativeTime from '$lib/components/relative-time.svelte';
	import * as Alert from '$lib/components/ui/alert';
	import * as InputGroup from '$lib/components/ui/input-group';
	import * as Sheet from '$lib/components/ui/sheet';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import ImageService from '$lib/services/image-service';
	import { apiErrorToast } from '$lib/utils/error-util';
	import { formatBytes, formatDuration } from '$lib/utils/format-util';
	import { tryCatch } from '$lib/utils/try-catch-util';
	import CircleAlertIcon from '@lucide/svelte/icons/circle-alert';
	import { tick } from 'svelte';

	let {
		jobId,
		imageId = $bindable(null),
		onSettled
	}: {
		jobId: string;
		// The image to show, the sheet is open while it is set
		imageId: string | null;
		// Called when a build that was running finishes, so the page can refresh its table
		onSettled?: () => void;
	} = $props();

	const POLL_MS = 2000;
	// How close to the bottom counts as following the log, so it keeps scrolling with new lines
	const FOLLOW_THRESHOLD_PX = 40;

	const imageService = new ImageService();

	let image = $state<JobImageDetail | null>(null);
	let logElement = $state<HTMLPreElement>();

	// Loads the image and polls every 2 seconds while it builds, so the log reads like a live stream
	$effect(() => {
		const id = imageId;
		if (!id) return;
		image = null;
		let timer: ReturnType<typeof setTimeout> | undefined;
		let cancelled = false;

		const load = async () => {
			const result = await tryCatch(imageService.get(jobId, id));
			if (cancelled) return;
			if (result.error) {
				apiErrorToast(result.error, 'Failed to load the image');
				imageId = null;
				return;
			}
			const wasBuilding = image !== null && isImageBuilding(image.status);
			await setImage(result.data);
			if (isImageBuilding(result.data.status)) {
				timer = setTimeout(load, POLL_MS);
			} else if (wasBuilding) {
				onSettled?.();
			}
		};
		void load();

		return () => {
			cancelled = true;
			clearTimeout(timer);
		};
	});

	async function setImage(next: JobImageDetail) {
		const el = logElement;
		const following = !el || el.scrollHeight - el.scrollTop - el.clientHeight < FOLLOW_THRESHOLD_PX;
		image = next;
		await tick();
		if (following && logElement) logElement.scrollTop = logElement.scrollHeight;
	}

	function onOpenChange(open: boolean) {
		if (!open) imageId = null;
	}

	const buildTime = $derived(
		image?.startedAt ? (image.finishedAt ?? Date.now()) - image.startedAt : null
	);
</script>

<Sheet.Root open={imageId !== null} {onOpenChange}>
	<Sheet.Content class="w-full data-[side=right]:sm:max-w-3xl">
		<Sheet.Header>
			<Sheet.Title class="flex items-center gap-2">
				Image build
				{#if image}<ImageStatusBadge status={image.status} />{/if}
			</Sheet.Title>
			<Sheet.Description>
				{#if image}
					Queued <RelativeTime value={image.createdAt} />
					{#if buildTime !== null}
						· {image.finishedAt ? 'took' : 'running for'} {formatDuration(buildTime)}
					{/if}
					{#if image.sizeBytes}· {formatBytes(image.sizeBytes)}{/if}
				{:else}
					Loading…
				{/if}
			</Sheet.Description>
		</Sheet.Header>
		<div class="flex min-h-0 flex-1 flex-col gap-5 overflow-y-auto px-4 pb-4">
			{#if !image}
				<Skeleton class="h-24 w-full" />
				<Skeleton class="h-64 w-full" />
			{:else}
				{#if image.error}
					<Alert.Root variant="destructive">
						<CircleAlertIcon />
						<Alert.Title>The build failed</Alert.Title>
						<Alert.Description>{image.error}</Alert.Description>
					</Alert.Root>
				{/if}
				{#if image.digest || image.ref}
					<dl class="grid gap-3 text-sm">
						{#each [{ label: 'Reference', value: image.ref }, { label: 'Digest', value: image.digest }, { label: 'Base image digest', value: image.baseDigest }] as item (item.label)}
							{#if item.value}
								<div class="flex flex-col gap-1">
									<dt class="text-muted-foreground text-xs">{item.label}</dt>
									<dd>
										<InputGroup.Root>
											<InputGroup.Input
												readonly
												value={item.value}
												class="font-mono text-xs"
												aria-label={item.label}
											/>
											<InputGroup.Addon align="inline-end">
												<CopyButton value={item.value} label="Copy {item.label.toLowerCase()}" />
											</InputGroup.Addon>
										</InputGroup.Root>
									</dd>
								</div>
							{/if}
						{/each}
					</dl>
				{/if}
				<section class="flex min-h-0 flex-col gap-2">
					<h3 class="text-sm font-medium">Build log</h3>
					<pre
						bind:this={logElement}
						aria-live={isImageBuilding(image.status) ? 'polite' : 'off'}
						class="bg-muted/50 max-h-[28rem] min-h-40 overflow-auto rounded-2xl p-4 font-mono text-xs leading-5 whitespace-pre-wrap break-all">{image.log ||
							(isImageBuilding(image.status) ? 'Waiting for output…' : 'No output.')}</pre>
				</section>
				<section class="flex flex-col gap-2">
					<h3 class="text-sm font-medium">Dockerfile</h3>
					<CodeEditor
						value={image.dockerfile}
						language="dockerfile"
						readonly
						label="Dockerfile of this build"
						class="h-auto max-h-72"
					/>
				</section>
			{/if}
		</div>
	</Sheet.Content>
</Sheet.Root>
