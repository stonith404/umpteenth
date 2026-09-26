import { json } from '@codemirror/lang-json';
import { markdown } from '@codemirror/lang-markdown';
import { python } from '@codemirror/lang-python';
import { StreamLanguage } from '@codemirror/language';
import { dockerFile } from '@codemirror/legacy-modes/mode/dockerfile';
import { shell } from '@codemirror/legacy-modes/mode/shell';
import type { Extension } from '@codemirror/state';
import type { CodeLanguage } from './script-language';

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
