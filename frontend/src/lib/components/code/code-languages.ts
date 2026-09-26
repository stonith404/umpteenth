import { json } from '@codemirror/lang-json';
import { markdown } from '@codemirror/lang-markdown';
import { python } from '@codemirror/lang-python';
import { StreamLanguage } from '@codemirror/language';
import { dockerFile } from '@codemirror/legacy-modes/mode/dockerfile';
import { shell } from '@codemirror/legacy-modes/mode/shell';
import type { Extension } from '@codemirror/state';

export type CodeLanguage = 'plain' | 'markdown' | 'python' | 'json' | 'shell' | 'dockerfile';

// Builds the CodeMirror language support for a language name
export function languageExtension(language: CodeLanguage): Extension {
	switch (language) {
		case 'markdown':
			return markdown();
		case 'python':
			return python();
		case 'json':
			return json();
		case 'shell':
			return StreamLanguage.define(shell);
		case 'dockerfile':
			return StreamLanguage.define(dockerFile);
		default:
			return [];
	}
}

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
