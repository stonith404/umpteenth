import McpService from '$lib/services/mcp-service';
import { tryCatch } from '$lib/utils/try-catch-util';
import type { PageLoad } from './$types';

// The configured MCP servers are matched against the services the compiled spec needs
// A failure here only hides the matching, so the page still loads
export const load: PageLoad = async ({ fetch }) => {
	const result = await tryCatch(new McpService(fetch).listAll());
	return { mcpServers: result.data ?? [] };
};
