import JobService from '#lib/services/job-service.js';
import McpService from '#lib/services/mcp-service.js';
import ProviderService from '#lib/services/provider-service.js';
import SecretService from '#lib/services/secret-service.js';
import SettingsService from '#lib/services/settings-service.js';
import SkillService from '#lib/services/skill-service.js';
import SystemService from '#lib/services/system-service.js';
import { tryCatch } from '#lib/utils/try-catch-util.js';
import type { PageLoad } from './$types';
import { JOB_SETTINGS_DEPENDENCY } from './load-error.svelte';

// The pickers need every model, MCP server, skill and secret of the workspace, and the workspace defaults explain empty fields
// Everything but the defaults only feeds one card, so a failed request shows an error in that card instead of taking down the whole tab
export const load: PageLoad = async ({ params, fetch, depends }) => {
	depends(JOB_SETTINGS_DEPENDENCY);
	const jobService = new JobService(fetch);
	const [jobServers, jobSkills, jobSecrets, servers, skills, secrets, models, settings, system] =
		await Promise.all([
			tryCatch(jobService.getMcpServers(params.id)),
			tryCatch(jobService.getSkills(params.id)),
			tryCatch(jobService.getSecrets(params.id)),
			tryCatch(new McpService(fetch).listAll()),
			tryCatch(new SkillService(fetch).listAll()),
			tryCatch(new SecretService(fetch).listAll()),
			tryCatch(new ProviderService(fetch).listAllModels()),
			new SettingsService(fetch).get(),
			tryCatch(new SystemService(fetch).info())
		]);

	// The network picker offers the unrestricted network only where the sandbox backend does
	const networks = system.data?.sandbox?.capabilities.networks ?? [];
	return {
		jobServers,
		jobSkills,
		jobSecrets,
		servers,
		skills,
		secrets,
		models,
		settings,
		networks
	};
};
