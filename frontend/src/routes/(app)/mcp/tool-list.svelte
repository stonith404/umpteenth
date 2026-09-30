<script lang="ts">
	import type { McpToolInfo } from '$lib/api/types';
	import CodeEditor from '$lib/components/code/code-editor.svelte';
	import { Badge } from '$lib/components/ui/badge';
	import * as Collapsible from '$lib/components/ui/collapsible';
	import ChevronRightIcon from '@lucide/svelte/icons/chevron-right';
	import WrenchIcon from '@lucide/svelte/icons/wrench';
	import { SvelteSet } from 'svelte/reactivity';

	let { tools }: { tools: McpToolInfo[] } = $props();

	// A server can list hundreds of tools, so a schema's editor is only created once its row is first opened
	const opened = new SvelteSet<string>();

	// Only schemas with parameters are worth expanding
	function hasParameters(schema: unknown) {
		if (!schema || typeof schema !== 'object') return false;
		const properties = (schema as { properties?: Record<string, unknown> }).properties;
		return !!properties && Object.keys(properties).length > 0;
	}
</script>

{#if tools.length > 0}
	<ul class="flex flex-col gap-2" aria-label="Tools">
		{#each tools as tool (tool.name)}
			<li class="rounded-lg border">
				<Collapsible.Root onOpenChange={(open) => open && opened.add(tool.name)}>
					<Collapsible.Trigger disabled={!hasParameters(tool.inputSchema)}>
						{#snippet child({ props })}
							<button {...props} class="group flex w-full items-start gap-3 px-3 py-2.5 text-left">
								{#if hasParameters(tool.inputSchema)}
									<ChevronRightIcon
										class="text-muted-foreground mt-0.5 size-4 shrink-0 transition-transform group-data-[state=open]:rotate-90"
									/>
								{:else}
									<WrenchIcon class="text-muted-foreground mt-0.5 size-4 shrink-0" />
								{/if}
								<div class="flex min-w-0 flex-1 flex-col gap-1">
									<span class="flex flex-wrap items-center gap-2">
										<span class="font-mono text-sm font-medium break-all">{tool.name}</span>
										{#if tool.readOnly}<Badge variant="secondary">Read-only</Badge>{/if}
										{#if tool.destructive}<Badge variant="destructive">Destructive</Badge>{/if}
									</span>
									{#if tool.description}
										<span class="text-muted-foreground text-sm whitespace-pre-line"
											>{tool.description}</span
										>
									{/if}
								</div>
							</button>
						{/snippet}
					</Collapsible.Trigger>
					<Collapsible.Content>
						{#if opened.has(tool.name)}
							<div class="px-3 pb-3">
								<CodeEditor
									value={JSON.stringify(tool.inputSchema, null, 2)}
									language="json"
									readonly
									label="Input schema of {tool.name}"
									class="h-auto max-h-72"
								/>
							</div>
						{/if}
					</Collapsible.Content>
				</Collapsible.Root>
			</li>
		{/each}
	</ul>
{:else}
	<p class="text-muted-foreground text-sm">The server offers no tools.</p>
{/if}
