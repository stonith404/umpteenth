import McpService from '$lib/services/mcp-service';
import SettingsService from '$lib/services/settings-service';
import SystemService from '$lib/services/system-service';
import { tryCatch } from '$lib/utils/try-catch-util';
import type { PageLoad } from './$types';

// The configured MCP servers are matched against the services the compiled spec needs, and the default image seeds the Dockerfile template
// A failure to list the servers or read the sandbox backend only hides what depends on them, so the page still loads
export const load: PageLoad = async ({ fetch }) => {
	const [servers, settings, system] = await Promise.all([
		tryCatch(new McpService(fetch).listAll()),
		new SettingsService(fetch).get(),
		tryCatch(new SystemService(fetch).info())
	]);
	return {
		mcpServers: servers.data ?? [],
		defaultImage: settings.defaultImage,
		// Compiling uses the utility model and falls back to the agent model, so without either it can only fail
		canCompile: !!(settings.utilityModelId || settings.agentModelId),
		// A backend that can't be read is assumed to build images, so the page doesn't warn about something it doesn't know
		imageBuilds: system.data?.sandbox?.imageBuilds ?? true
	};
};
