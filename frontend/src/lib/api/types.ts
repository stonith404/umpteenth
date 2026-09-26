import type { components, operations } from './schema';

// Short names for the generated types, so components never spell out `components['schemas'][...]`
// These are aliases only: every API type comes from the generated `schema.d.ts`, never from hand-written interfaces
export type Schemas = components['schemas'];

// The JSON request body of an operation
// Request and response types are looked up by operation ID, because Huma names inline body schemas generically (e.g. `CreateInputBody1`) and those names shift as modules are added
export type RequestBodyOf<O extends keyof operations> = operations[O] extends {
	requestBody?: { content: { 'application/json': infer T } };
}
	? T
	: never;

// The JSON body of an operation's 200 response
export type ResponseOf<O extends keyof operations> = operations[O]['responses'] extends {
	200: { content: { 'application/json': infer T } };
}
	? T
	: never;

// The query parameters of an operation, e.g. the paging, sorting and filter parameters of a list endpoint
export type QueryOf<O extends keyof operations> = NonNullable<operations[O]['parameters']['query']>;

export type User = Schemas['UserDto'];
export type ApiToken = Schemas['ApiTokenDto'];
export type ApiTokenCreate = RequestBodyOf<'create-api-token'>;
export type ApiTokenCreated = ResponseOf<'create-api-token'>;
export type WorkspaceSettings = Schemas['WorkspaceSettings'];
export type SystemInfo = ResponseOf<'get-system-info'>;

// The sandbox adapter's report inside the system info, which the API passes through untyped
export type SandboxInfo = {
	adapter: string;
	version: string;
	arch: string;
	isolation: string;
	egressFilter?: 'active' | 'unavailable' | 'off';
	egressFilterError?: string;
	runtimeError?: string;
};
export type WorkspaceSettingsUpdate = RequestBodyOf<'update-settings'>;
export type Limits = Schemas['Limits'];

export type Run = Schemas['RunDto'];
export type RunDetail = Schemas['RunDetailDto'];
export type RunEvent = Schemas['EventDto'];
export type RunDelta = Schemas['DeltaDto'];
export type RunArtifact = Schemas['ArtifactDto'];
export type RunListQuery = QueryOf<'list-runs'>;
export type RunTriggerResult = ResponseOf<'retry-run'>;
export type WorkspaceEvent = Schemas['WorkspaceEventDto'];
export type StatsOverview = ResponseOf<'get-stats-overview'>;
export type StatsRange = NonNullable<QueryOf<'get-stats-overview'>['range']>;
export type StatsTotals = Schemas['Totals'];
export type StatsDayBucket = Schemas['DayBucket'];
export type StatsJobCost = Schemas['JobCost'];
export type StatsCheaperJob = Schemas['CheaperJob'];
export type RunRef = Schemas['RunRef'];

// Jobs, playbooks and job images
export type Job = Schemas['JobDto'];
export type JobListItem = Schemas['JobListDto'];
export type JobListQuery = QueryOf<'list-jobs'>;
export type JobFields = Schemas['JobFields'];
export type JobPatch = RequestBodyOf<'update-job'>;
export type JobSpec = Schemas['Spec'];
export type JobSchedule = Schemas['Schedule'];
export type JobIOField = Schemas['IOField'];
export type JobMcpNeed = Schemas['MCPNeed'];
export type JobLimitOverrides = Schemas['LimitOverrides'];
export type JobStats = ResponseOf<'get-job-stats'>;
export type JobStatsRange = NonNullable<QueryOf<'get-job-stats'>['range']>;
export type JobRunPoint = Schemas['JobRunPoint'];
export type JobVersionMarker = Schemas['VersionMarker'];
export type JobRunNow = RequestBodyOf<'run-job'>;
export type JobTriggerResult = ResponseOf<'run-job'>;
export type JobStateEntry = Schemas['StateEntryDto'];
export type JobWebhookToken = ResponseOf<'rotate-webhook-token'>;
export type JobServer = Schemas['JobServer'];
export type JobSecret = Schemas['JobSecret'];
export type PlaybookVersion = Schemas['VersionDto'];
export type PlaybookVersionListItem = Schemas['VersionListDto'];
export type PlaybookContent = Schemas['Content'];
export type PlaybookLearning = Schemas['Learning'];
export type PlaybookScript = Schemas['Script'];
export type PlaybookUpdate = RequestBodyOf<'update-playbook'>;
export type PlaybookAppliedOp = Schemas['AppliedOp'];
export type JobImage = Schemas['ImageDto'];
export type JobImageDetail = ResponseOf<'get-image'>;

// MCP servers
export type McpServer = Schemas['ServerDto'];
export type McpServerAuth = Schemas['AuthDto'];
export type McpServerBody = Schemas['ServerBody'];
export type McpToolInfo = Schemas['ToolInfo'];
export type McpTestResult = ResponseOf<'test-mcp-server'>;

// Providers, models and secrets
export type Provider = Schemas['ProviderDto'];
export type ProviderCreate = RequestBodyOf<'create-provider'>;
export type ProviderUpdate = RequestBodyOf<'update-provider'>;
export type ProviderKind = ProviderCreate['kind'];
export type ProviderTestResult = ResponseOf<'test-provider'>;
export type Model = Schemas['ModelDto'];
export type ModelCreate = RequestBodyOf<'create-model'>;
export type ModelUpdate = RequestBodyOf<'update-model'>;
export type ModelCaps = Schemas['Caps'];
export type ModelPrice = Schemas['Price'];
export type CatalogModel = Schemas['CatalogEntryDto'];
export type Secret = Schemas['SecretDto'];
