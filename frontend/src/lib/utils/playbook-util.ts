import type { PlaybookAppliedOp, PlaybookContent, PlaybookLearning } from '$lib/api/types';
import type { CodeLanguage } from '$lib/components/code/code-languages';
import { languageForScript } from '$lib/components/code/code-languages';

export const authorLabels: Record<string, string> = {
	user: 'Manual edit',
	reflection: 'Reflection',
	rollback: 'Rollback',
	compile: 'Compile'
};

export function authorLabel(author: string) {
	return authorLabels[author] ?? author;
}

// One comparable part of a playbook, e.g. the Dockerfile or one toolkit script
export type PlaybookSection = {
	key: string;
	label: string;
	language: CodeLanguage;
	before: string;
	after: string;
};

// A learning as one readable line, so a changed hit count or status shows up as a changed line in diffs
function learningLine(l: PlaybookLearning) {
	const meta = [l.kind, l.status, `hits ${l.hits}`].join(', ');
	return `[${l.id}] (${meta}) ${l.text}${l.when ? ` — when ${l.when}` : ''}`;
}

function verifyText(verify: unknown) {
	return verify === undefined || verify === null ? '' : JSON.stringify(verify, null, 2);
}

// Splits two playbook versions into comparable sections, keeping only the ones that differ
export function changedSections(
	before: PlaybookContent | undefined,
	after: PlaybookContent
): PlaybookSection[] {
	const sections: PlaybookSection[] = [];
	const add = (key: string, label: string, language: CodeLanguage, a: string, b: string) => {
		if (a !== b) sections.push({ key, label, language, before: a, after: b });
	};

	add(
		'learnings',
		'Learnings',
		'plain',
		(before?.learnings ?? []).map(learningLine).join('\n'),
		(after.learnings ?? []).map(learningLine).join('\n')
	);

	// Scripts are compared by name, so an added or removed script diffs against nothing
	const beforeScripts = new Map((before?.toolkit ?? []).map((s) => [s.name, s]));
	const afterScripts = new Map((after.toolkit ?? []).map((s) => [s.name, s]));
	const names = [...new Set([...beforeScripts.keys(), ...afterScripts.keys()])].sort();
	for (const name of names) {
		const a = beforeScripts.get(name);
		const b = afterScripts.get(name);
		const header = (s: typeof a) =>
			s ? `# ${s.description}${s.sideEffects ? ' (side effects)' : ''}\n${s.content}` : '';
		add(
			`script:${name}`,
			`Script ${name}`,
			languageForScript(b?.lang ?? a?.lang),
			header(a),
			header(b)
		);
	}

	add('dockerfile', 'Dockerfile', 'dockerfile', before?.dockerfile ?? '', after.dockerfile ?? '');
	add('setup', 'Setup script', 'shell', before?.setup ?? '', after.setup ?? '');
	add('main', 'Main script', 'shell', before?.main ?? '', after.main ?? '');
	add('verify', 'Verify', 'json', verifyText(before?.verify), verifyText(after.verify));
	return sections;
}

// Formats a playbook for the JSON editor, with a stable key order
export function playbookToJson(content: PlaybookContent): string {
	const ordered: PlaybookContent = {
		learnings: content.learnings ?? [],
		toolkit: content.toolkit ?? [],
		dockerfile: content.dockerfile ?? null,
		setup: content.setup ?? null,
		main: content.main ?? null
	};
	if (content.verify !== undefined && content.verify !== null) ordered.verify = content.verify;
	return JSON.stringify(ordered, null, 2);
}

// A readable sentence for one reflection operation, e.g. "Add learning L3"
export function opLabel(op: PlaybookAppliedOp): string {
	const target = op.target ?? op.id ?? op.name ?? '';
	switch (op.op) {
		case 'add_learning':
			return target ? `Add learning ${target}` : 'Add a learning';
		case 'update_learning':
			return `Update learning ${target}`;
		case 'retire_learning':
			return `Retire learning ${target}`;
		case 'upsert_script':
			return target ? `Save script ${target}` : 'Save a script';
		case 'delete_script':
			return `Delete script ${target}`;
		case 'set_setup':
			return op.content ? 'Set the setup script' : 'Remove the setup script';
		case 'set_dockerfile':
			return op.content ? 'Set the Dockerfile' : 'Remove the Dockerfile';
		case 'propose_main':
			return 'Graduate to a main script';
		case 'update_main':
			return 'Update the main script';
		case 'set_verify':
			return op.content ? 'Set the verify checks' : 'Remove the verify checks';
	}
	return op.op;
}

export const opStatusLabels: Record<PlaybookAppliedOp['status'], string> = {
	applied: 'Applied',
	held: 'Held for review',
	rejected: 'Rejected'
};

export function opStatusLabel(status: PlaybookAppliedOp['status']) {
	return opStatusLabels[status] ?? status;
}

// The editor language of an operation's content
export function opLanguage(op: PlaybookAppliedOp): CodeLanguage {
	if (op.op === 'set_dockerfile') return 'dockerfile';
	if (op.op === 'set_setup') return 'shell';
	if (op.op === 'set_verify') return 'json';
	const shebang = op.content?.split('\n', 1)[0] ?? '';
	return shebang.includes('python') ? 'python' : 'shell';
}
