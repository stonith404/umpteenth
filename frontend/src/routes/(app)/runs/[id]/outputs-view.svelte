<script lang="ts">
	import JsonView from './json-view.svelte';

	// Structured outputs of a run, usually a flat object of named values
	let { outputs }: { outputs: unknown } = $props();

	const entries = $derived(
		outputs && typeof outputs === 'object' && !Array.isArray(outputs)
			? Object.entries(outputs as Record<string, unknown>)
			: null
	);

	function isScalar(value: unknown) {
		return value === null || ['string', 'number', 'boolean'].includes(typeof value);
	}
</script>

{#if entries}
	<dl class="divide-y overflow-hidden rounded-lg border text-sm" data-slot="outputs">
		{#each entries as [key, value] (key)}
			<div class="grid gap-1 px-3 py-2 sm:grid-cols-outputs sm:gap-4">
				<dt class="text-muted-foreground font-mono text-xs leading-5 break-all">{key}</dt>
				<dd class="min-w-0">
					{#if isScalar(value)}
						<span class="font-mono text-xs leading-5 break-words whitespace-pre-wrap">
							{typeof value === 'string' ? value : JSON.stringify(value)}
						</span>
					{:else}
						<!-- The list's own border frames the value already, so it isn't boxed a second time -->
						<JsonView {value} class="max-h-60 rounded-none border-0 bg-transparent p-0" />
					{/if}
				</dd>
			</div>
		{/each}
	</dl>
{:else}
	<JsonView value={outputs} class="max-h-80" />
{/if}
