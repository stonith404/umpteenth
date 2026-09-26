import type { Provider } from '$lib/api/types';

// UI vocabulary of provider kinds, the API types the kind as a plain string
const providerKindLabels: Record<string, string> = {
	anthropic: 'Anthropic',
	openai: 'OpenAI-compatible',
	fake: 'Fake (tests)'
};

export function providerKindLabel(kind: string) {
	return providerKindLabels[kind] ?? kind;
}

// Base URLs of common OpenAI-compatible APIs, offered as one-click presets
export const openAiPresets = [
	{ name: 'OpenAI', baseUrl: 'https://api.openai.com/v1' },
	{ name: 'OpenRouter', baseUrl: 'https://openrouter.ai/api/v1' },
	{ name: 'Ollama', baseUrl: 'http://localhost:11434/v1' },
	{ name: 'LM Studio', baseUrl: 'http://localhost:1234/v1' }
];

// Whether a provider's models come from the catalog, mirroring the backend's rule so the form can say so before saving
export function usesCatalog(kind: string, baseUrl: string) {
	if (kind === 'anthropic') return true;
	if (kind !== 'openai') return false;
	const trimmed = baseUrl.trim();
	if (!trimmed) return true;
	try {
		return new URL(trimmed).hostname.toLowerCase() === 'api.openai.com';
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
