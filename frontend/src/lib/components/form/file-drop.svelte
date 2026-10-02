<script lang="ts">
	import { Button } from '$lib/components/ui/button';
	import { formatBytes } from '$lib/utils/format-util';
	import { cn } from '$lib/utils/style';
	import FileArchiveIcon from '@lucide/svelte/icons/file-archive';
	import UploadIcon from '@lucide/svelte/icons/upload';
	import XIcon from '@lucide/svelte/icons/x';

	let {
		file = $bindable(null),
		id,
		accept,
		hint,
		invalid = false,
		onchange
	}: {
		// The picked file, null until one is picked
		file?: File | null;
		// Ties the hidden input to the field's label
		id: string;
		accept?: string;
		// Names what to drop, e.g. "Drop a zip here"
		hint: string;
		invalid?: boolean;
		onchange?: (file: File | null) => void;
	} = $props();

	let input: HTMLInputElement;
	let dragging = $state(false);

	function pick(next: File | null) {
		file = next;
		onchange?.(next);
	}

	// The input is cleared after each pick, so picking the same file again after removing it still fires a change
	function onInputChange() {
		pick(input.files?.[0] ?? null);
		input.value = '';
	}

	function onDrop(event: DragEvent) {
		event.preventDefault();
		dragging = false;
		const dropped = event.dataTransfer?.files?.[0];
		if (dropped) pick(dropped);
	}
</script>

<!-- The native input stays in the page for the label and for tests, while the button and the drop zone stand in for its own unlabelled controls -->
<input
	bind:this={input}
	{id}
	type="file"
	{accept}
	class="sr-only"
	tabindex={-1}
	aria-invalid={invalid}
	onchange={onInputChange}
/>

{#if file}
	<div
		data-slot="file-drop"
		class={cn(
			'bg-card flex items-center gap-3 rounded-lg border py-2 pr-2 pl-3',
			invalid && 'border-destructive'
		)}
	>
		<FileArchiveIcon class="text-muted-foreground size-4 shrink-0" />
		<span class="min-w-0 flex-1 truncate text-sm" title={file.name}>{file.name}</span>
		<span class="text-muted-foreground numeric shrink-0 text-xs">{formatBytes(file.size)}</span>
		<Button
			variant="ghost"
			size="icon-sm"
			aria-label="Remove {file.name}"
			onclick={() => pick(null)}
		>
			<XIcon />
		</Button>
	</div>
{:else}
	<!-- Dropping is a shortcut, so the zone itself takes no focus and the button stays the way to pick by keyboard -->
	<!-- svelte-ignore a11y_no_static_element_interactions -->
	<div
		data-slot="file-drop"
		class={cn(
			'flex flex-col items-center gap-2 rounded-lg border border-dashed px-4 py-5 text-center transition-colors',
			dragging && 'border-foreground/50 bg-muted/50',
			invalid && 'border-destructive'
		)}
		ondragenter={(e) => {
			e.preventDefault();
			dragging = true;
		}}
		ondragover={(e) => e.preventDefault()}
		ondragleave={(e) => {
			if (!e.currentTarget.contains(e.relatedTarget as Node | null)) dragging = false;
		}}
		ondrop={onDrop}
	>
		<UploadIcon class="text-muted-foreground size-5" />
		<p class="text-muted-foreground text-sm">{hint}, or</p>
		<Button variant="outline" size="sm" onclick={() => input.click()}>Choose file</Button>
	</div>
{/if}
