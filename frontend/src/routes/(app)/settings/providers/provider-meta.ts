import type { Provider } from '#lib/api/types.js';

// UI vocabulary of provider kinds, the API types the kind as a plain string
const providerKindLabels: Record<string, string> = {
	anthropic: 'Anthropic',
	openai: 'OpenAI-compatible',
	fake: 'Fake (tests)'
};

export function providerKindLabel(kind: string) {
	return providerKindLabels[kind] ?? kind;
}

// Whether a provider talks to its kind's official API, which rejects every request without an API key
export function isOfficialApi(kind: string, baseUrl: string) {
	const trimmed = baseUrl.trim();
	if (!trimmed) return kind === 'anthropic' || kind === 'openai';
	try {
		const host = new URL(trimmed).hostname.toLowerCase();
		return (
			(kind === 'anthropic' && host === 'api.anthropic.com') ||
			(kind === 'openai' && host === 'api.openai.com')
		);
	} catch {
		return false;
	}
}

// Where a provider's model list comes from, in the words the providers page uses
export const modelSourceLabels: Record<Provider['modelSource'], string> = {
	catalog: 'Catalog',
	server: 'Server list',
	manual: 'Added by hand'
};

// Capabilities a model can declare, in the order the model form lists them
export const capabilityLabels = {
	tools: 'Tool use',
	parallelTools: 'Parallel tool calls',
	reasoning: 'Reasoning',
	jsonSchema: 'Structured output',
	promptCache: 'Prompt caching',
	vision: 'Vision'
} as const;

export type Capability = keyof typeof capabilityLabels;
