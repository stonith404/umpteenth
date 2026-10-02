<script lang="ts">
	import type { McpServerAuth } from '#lib/api/types.js';
	import { Badge } from '#lib/components/ui/badge/index.js';
	import { authBadge } from '#lib/utils/mcp-util.js';

	let { auth }: { auth: McpServerAuth } = $props();

	const badge = $derived(authBadge(auth));
</script>

<Badge variant={badge.variant}>{badge.label}</Badge>
