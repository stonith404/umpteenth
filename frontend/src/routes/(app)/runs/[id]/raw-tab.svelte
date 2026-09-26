<script lang="ts">
	import type { RunDetail, RunEvent } from '$lib/api/types';
	import { Button } from '$lib/components/ui/button';
	import CopyIcon from '@lucide/svelte/icons/copy';
	import DownloadIcon from '@lucide/svelte/icons/download';
	import { toast } from 'svelte-sonner';
	import JsonView from './json-view.svelte';

	let { run, events }: { run: RunDetail; events: RunEvent[] } = $props();

	const document = $derived({ run, events });

	function download() {
		const blob = new Blob([JSON.stringify(document, null, 2)], { type: 'application/json' });
		const url = URL.createObjectURL(blob);
		const link = Object.assign(window.document.createElement('a'), {
			href: url,
			download: `run-${run.jobName.replace(/[^\w-]+/g, '-').toLowerCase()}-${run.number}.json`
		});
		link.click();
		URL.revokeObjectURL(url);
	}

	async function copy() {
		try {
			await navigator.clipboard.writeText(JSON.stringify(document, null, 2));
			toast.success('Copied the run as JSON');
		} catch {
			toast.error('Copying failed, use the download instead');
		}
	}
</script>

<div class="flex flex-col gap-3">
	<div class="flex flex-wrap items-center gap-2">
		<p class="text-muted-foreground text-sm">
			The run and its {events.length.toLocaleString()} events as returned by the API.
		</p>
		<div class="ml-auto flex gap-2">
			<Button variant="outline" size="sm" onclick={copy}>
				<CopyIcon data-icon="inline-start" />
				Copy
			</Button>
			<Button variant="outline" size="sm" onclick={download}>
				<DownloadIcon data-icon="inline-start" />
				Download JSON
			</Button>
		</div>
	</div>
	<JsonView value={document} class="max-h-[70vh]" />
</div>
