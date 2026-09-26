<script lang="ts">
	import type { SandboxInfo, SystemInfo } from '$lib/api/types';
	import * as Alert from '$lib/components/ui/alert';
	import { Badge } from '$lib/components/ui/badge';
	import * as Card from '$lib/components/ui/card';
	import TriangleAlertIcon from '@lucide/svelte/icons/triangle-alert';

	let { system }: { system: SystemInfo } = $props();

	const sandbox = $derived(system.sandbox as SandboxInfo | null | undefined);
	const egressLabels: Record<string, string> = {
		active: 'Private networks blocked',
		unavailable: 'Not enforced',
		off: 'Turned off'
	};
</script>

<Card.Root>
	<Card.Header>
		<Card.Title>Sandbox backend</Card.Title>
		<Card.Description
			>Where runs execute on this instance. Set by the server's environment.</Card.Description
		>
	</Card.Header>
	<Card.Content class="flex flex-col gap-4">
		{#if sandbox}
			<dl class="grid grid-cols-2 gap-x-6 gap-y-3 text-sm sm:grid-cols-4">
				<div class="flex flex-col gap-1">
					<dt class="text-muted-foreground text-xs">Adapter</dt>
					<dd class="font-mono text-xs">{sandbox.adapter} {sandbox.version}</dd>
				</div>
				<div class="flex flex-col gap-1">
					<dt class="text-muted-foreground text-xs">Isolation</dt>
					<dd><Badge variant="outline" class="rounded-md">{sandbox.isolation}</Badge></dd>
				</div>
				<div class="flex flex-col gap-1">
					<dt class="text-muted-foreground text-xs">Architecture</dt>
					<dd class="font-mono text-xs">{sandbox.arch}</dd>
				</div>
				<div class="flex flex-col gap-1">
					<dt class="text-muted-foreground text-xs">Egress firewall</dt>
					<dd>
						<Badge
							variant={sandbox.egressFilter === 'active' ? 'secondary' : 'outline'}
							class="rounded-md"
						>
							{egressLabels[sandbox.egressFilter ?? ''] ?? 'Unknown'}
						</Badge>
					</dd>
				</div>
			</dl>
			{#if sandbox.runtimeError}
				<Alert.Root variant="destructive">
					<TriangleAlertIcon />
					<Alert.Title>Sandboxes can't start under the configured runtime</Alert.Title>
					<Alert.Description>
						A test sandbox failed when the server started: {sandbox.runtimeError}. Runs fail until
						the runtime is fixed and the server restarted.
					</Alert.Description>
				</Alert.Root>
			{/if}
			{#if sandbox.egressFilter === 'unavailable'}
				<Alert.Root variant="destructive">
					<TriangleAlertIcon />
					<Alert.Title>Internet sandboxes can reach private networks</Alert.Title>
					<Alert.Description>
						The firewall rules that keep them off the LAN, the host and cloud metadata could not be
						installed{sandbox.egressFilterError ? `: ${sandbox.egressFilterError}` : '.'} Set sandbox.egress_filter
						to required to refuse internet runs until they can be.
					</Alert.Description>
				</Alert.Root>
			{/if}
		{:else}
			<p class="text-muted-foreground text-sm">No sandbox backend is configured.</p>
		{/if}
	</Card.Content>
</Card.Root>
