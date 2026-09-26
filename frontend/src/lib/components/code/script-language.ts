// Language names of the code editor, kept apart from the CodeMirror extensions in code-languages.ts so pages that only name a language don't load the editor
export type CodeLanguage = 'plain' | 'markdown' | 'python' | 'json' | 'shell' | 'dockerfile';

// Maps a playbook script's `lang` (e.g. `bash` or `python`) to an editor language
export function languageForScript(lang: string | null | undefined): CodeLanguage {
	const normalized = (lang ?? '').toLowerCase();
	if (normalized.startsWith('py')) return 'python';
	if (['bash', 'sh', 'shell', 'zsh'].includes(normalized)) return 'shell';
	if (normalized === 'json') return 'json';
	if (normalized === 'dockerfile') return 'dockerfile';
	if (normalized === 'markdown' || normalized === 'md') return 'markdown';
	return 'plain';
}
