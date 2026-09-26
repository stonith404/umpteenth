import JobService from '$lib/services/job-service';
import McpService from '$lib/services/mcp-service';
import ProviderService from '$lib/services/provider-service';
import SecretService from '$lib/services/secret-service';
import SettingsService from '$lib/services/settings-service';
import type { PageLoad } from './$types';

// The pickers need every model, MCP server and secret of the workspace, and the workspace defaults explain empty fields
export const load: PageLoad = async ({ params, fetch, depends }) => {
	depends('app:job-settings');
	const jobService = new JobService(fetch);
	const [jobServers, jobSecrets, servers, secrets, models, settings] = await Promise.all([
		jobService.getMcpServers(params.id),
		jobService.getSecrets(params.id),
		new McpService(fetch).listAll(),
		new SecretService(fetch).listAll(),
		new ProviderService(fetch).listAllModels(),
		new SettingsService(fetch).get()
	]);
	return { jobServers, jobSecrets, servers, secrets, models, settings };
};
