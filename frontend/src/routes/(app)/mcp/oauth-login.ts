import type { McpServer } from '$lib/api/types';
import McpService from '$lib/services/mcp-service';
import { apiErrorToast } from '$lib/utils/error-util';
import { tryCatch } from '$lib/utils/try-catch-util';

// Sends the browser to the server's authorization server, which returns it to the MCP servers page with the outcome
export async function startOAuthLogin(server: Pick<McpServer, 'id' | 'name'>) {
	const result = await tryCatch(new McpService().login(server.id));
	if (result.error) {
		apiErrorToast(result.error, `Failed to start the login for ${server.name}`);
		return false;
	}
	window.location.assign(result.data.authorizationUrl);
	return true;
}
