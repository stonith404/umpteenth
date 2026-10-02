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
export type LoginProvider = Schemas['LoginProviderDto'];
export type Passkey = Schemas['PasskeyDto'];
export type SignInLink = Schemas['SignInLinkDto'];
export type ApiToken = Schemas['ApiTokenDto'];
export type ApiTokenCreate = RequestBodyOf<'create-api-token'>;
export type ApiTokenCreated = ResponseOf<'create-api-token'>;
export type WorkspaceSettings = Schemas['WorkspaceSettings'];
export type WorkspaceSettingsUpdate = RequestBodyOf<'update-settings'>;
// Whether the UI shows usage as a price in US dollars or as input and output tokens, a workspace setting that also comes with the session's workspace
export type UsageUnit = WorkspaceSettings['usageUnit'];
export type Limits = Schemas['Limits'];

export type Run = Schemas['RunDto'];
export type RunDetail = Schemas['RunDetailDto'];
export type RunEvent = Schemas['EventDto'];
export type RunDelta = Schemas['DeltaDto'];
export type RunArtifact = Schemas['ArtifactDto'];
export type RunListQuery = QueryOf<'list-runs'>;
export type WorkspaceEvent = Schemas['WorkspaceEventDto'];
export type StatsOverview = ResponseOf<'get-stats-overview'>;
export type StatsRange = NonNullable<QueryOf<'get-stats-overview'>['range']>;
export type StatsDayBucket = Schemas['DayBucket'];
// The width of the overview's chart buckets: 'hour' for the 24h range, a UTC 'day' otherwise
export type StatsBucket = StatsOverview['bucket'];
export type StatsJobCost = Schemas['JobCost'];
// The KPI totals of one period, with a token count next to every cost
export type StatsTotals = Schemas['Totals'];

// Jobs, playbooks and job images
export type Job = Schemas['JobDto'];
export type JobListItem = Schemas['JobListDto'];
// One run of a job's history strip on the jobs list, and the shape of a job's lastRun
export type JobRecentRun = Schemas['LastRun'];
export type JobListQuery = QueryOf<'list-jobs'>;
export type JobFields = Schemas['JobFields'];
export type JobPatch = RequestBodyOf<'update-job'>;
export type JobSpec = Schemas['Spec'];
export type JobIOField = Schemas['IOField'];
export type JobMcpNeed = Schemas['MCPNeed'];
// What the compile step answers: the spec, and what the user has to answer before it can be trusted
export type JobCompileResult = Schemas['CompileOutputBody'];
export type JobQuestion = Schemas['Question'];
export type JobStatsRange = NonNullable<QueryOf<'get-job-stats'>['range']>;
export type JobRunPoint = Schemas['JobRunPoint'];
export type JobVersionMarker = Schemas['VersionMarker'];
export type JobRunNow = RequestBodyOf<'run-job'>;
export type JobStateEntry = Schemas['StateEntryDto'];
export type JobServer = Schemas['JobServer'];
export type JobSkill = Schemas['JobSkill'];
export type JobSecret = Schemas['JobSecret'];
// A credential the compile step found the job's commands need, with the configured secret that holds it
export type JobSecretNeed = Schemas['SecretNeed'];
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

// Agent skills
export type Skill = Schemas['SkillDto'];
export type SkillFile = Schemas['SkillFile'];
export type SkillChoice = Schemas['SkillChoice'];

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
export type CatalogModel = Schemas['CatalogEntryDto'];
export type Secret = Schemas['SecretDto'];

// Workspaces, their members and invites, and instance administration
export type Workspace = Schemas['WorkspaceDto'];
export type WorkspaceRole = Workspace['role'];
export type WorkspaceMember = Schemas['MemberDto'];
export type WorkspaceInvite = Schemas['InviteDto'];
export type WorkspaceInviteCreate = RequestBodyOf<'create-workspace-invite'>;
export type InvitePreview = ResponseOf<'lookup-invite'>;
export type AdminUser = Schemas['AdminUserDto'];
export type AdminUserCreate = RequestBodyOf<'create-user'>;
export type AdminUserCreated = ResponseOf<'create-user'>;
export type AdminWorkspace = Schemas['AdminWorkspaceDto'];
