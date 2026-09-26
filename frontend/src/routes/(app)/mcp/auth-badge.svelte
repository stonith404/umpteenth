<script lang="ts">
	import type { McpServerAuth } from '$lib/api/types';
	import { Badge } from '$lib/components/ui/badge';
	import { authBadge } from '$lib/utils/mcp-util';

	let { auth }: { auth: McpServerAuth } = $props();

	const badge = $derived(authBadge(auth));
</script>

<Badge variant={badge.variant}>{badge.label}</Badge>
