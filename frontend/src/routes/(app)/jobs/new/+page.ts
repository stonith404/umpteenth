import McpService from '#lib/services/mcp-service.js';
import SecretService from '#lib/services/secret-service.js';
import SettingsService from '#lib/services/settings-service.js';
import SkillService from '#lib/services/skill-service.js';
import SystemService from '#lib/services/system-service.js';
import { tryCatch } from '#lib/utils/try-catch-util.js';
import type { PageLoad } from './$types';

// The configured MCP servers are matched against the services the compiled spec needs, and the default image seeds the Dockerfile template
// The workspace's skills can be attached right away, preselected when the compile step suggests them, and its secrets are matched to the credentials the job needs
// A failure to list the servers, skills or secrets or to read the sandbox backend only hides what depends on them, so the page still loads
export const load: PageLoad = async ({ fetch }) => {
	const [servers, skills, secrets, settings, system] = await Promise.all([
		tryCatch(new McpService(fetch).listAll()),
		tryCatch(new SkillService(fetch).listAll()),
		tryCatch(new SecretService(fetch).listAll()),
		new SettingsService(fetch).get(),
		tryCatch(new SystemService(fetch).info())
	]);
	return {
		mcpServers: servers.data ?? [],
		skills: skills.data ?? [],
		secrets: secrets.data ?? [],
		defaultImage: settings.defaultImage,
		// Compiling uses the utility model and falls back to the agent model, so without either it can only fail
		canCompile: !!(settings.utilityModelId || settings.agentModelId),
		// A backend that can't be read is assumed to build images, so the page doesn't warn about something it doesn't know
		imageBuilds: system.data?.sandbox?.imageBuilds ?? true
	};
};
