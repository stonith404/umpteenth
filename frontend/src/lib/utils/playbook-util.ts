import type { PlaybookAppliedOp, PlaybookContent, PlaybookLearning } from '#lib/api/types.js';
import { languageForScript, type CodeLanguage } from '#lib/components/code/script-language.js';
import { humanize } from '#lib/utils/format-util.js';

const authorLabels: Record<string, string> = {
	user: 'Manual edit',
	reflection: 'Reflection',
	rollback: 'Rollback',
	compile: 'Compile'
};

export function authorLabel(author: string) {
	return authorLabels[author] ?? author;
}

const scriptLanguageLabels: Record<string, string> = {
	python: 'Python',
	bash: 'Bash',
	sh: 'Shell'
};

// A script's language as people write it, e.g. `Python` for `python`
export function scriptLanguageLabel(lang: string) {
	return scriptLanguageLabels[lang.toLowerCase()] ?? lang;
}

// The reflection names a learning's kind with a short free-form word, and these are the ones it uses most
const learningKindLabels: Record<string, string> = {
	edge_case: 'Edge case',
	preference: 'Preference',
	fact: 'Fact',
	workaround: 'Workaround',
	environment: 'Environment'
};

// A learning's kind in sentence case, e.g. `Edge case` for `edge_case`
// Kinds are free-form, so a kind like `constructor` must not resolve to an inherited property
export function learningKindLabel(kind: string) {
	return Object.hasOwn(learningKindLabels, kind) ? learningKindLabels[kind] : humanize(kind);
}

// One comparable part of a playbook, e.g. the Dockerfile or one toolkit script
export type PlaybookSection = {
	key: string;
	label: string;
	language: CodeLanguage;
	before: string;
	after: string;
};

// A learning as one line of prose, e.g. `Ask HN posts have no URL — edge case, when listing stories`
// The hit count changes with every run that uses the learning, so it is left out to keep diffs to real edits
function learningLine(l: PlaybookLearning) {
	const meta = [learningKindLabel(l.kind).toLowerCase()];
	if (l.when) meta.push(`when ${l.when}`);
	if (l.status === 'retired') meta.push('retired');
	return `${l.text} — ${meta.join(', ')}`;
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
	// A script's description and side effects come from its own ump: header, so the content diff already shows them
	const beforeScripts = new Map((before?.toolkit ?? []).map((s) => [s.name, s]));
	const afterScripts = new Map((after.toolkit ?? []).map((s) => [s.name, s]));
	const names = [...new Set([...beforeScripts.keys(), ...afterScripts.keys()])].sort();
	for (const name of names) {
		const a = beforeScripts.get(name);
		const b = afterScripts.get(name);
		add(
			`script:${name}`,
			`Script ${name}`,
			languageForScript(b?.lang ?? a?.lang),
			a?.content ?? '',
			b?.content ?? ''
		);
	}

	add('dockerfile', 'Dockerfile', 'dockerfile', before?.dockerfile ?? '', after.dockerfile ?? '');
	add('setup', 'Setup script', 'shell', before?.setup ?? '', after.setup ?? '');
	add('main', 'Main script', 'shell', before?.main ?? '', after.main ?? '');
	add('verify', 'Verify', 'json', verifyText(before?.verify), verifyText(after.verify));
	return sections;
}

// The part of a playbook a change touches, which picks its icon
export type PlaybookChangeKind = 'learning' | 'script' | 'dockerfile' | 'setup' | 'main' | 'verify';

// One change in a version: the proposal behind it and the diff it made
// A hand edit has no proposal, and a held or rejected proposal has no diff
export type PlaybookChange = {
	key: string;
	label: string;
	kind: PlaybookChangeKind;
	op?: PlaybookAppliedOp;
	section?: PlaybookSection;
};

// The section key an operation edits, matching the keys of changedSections
// Learnings are keyed one by one so each proposal can show only its own learning
function opSectionKey(op: PlaybookAppliedOp): string {
	switch (op.op) {
		case 'add_learning':
		case 'update_learning':
		case 'retire_learning':
			return `learning:${op.target ?? op.id ?? ''}`;
		case 'upsert_script':
		case 'delete_script':
			return `script:${op.target ?? op.name ?? ''}`;
		case 'set_setup':
			return 'setup';
		case 'set_dockerfile':
			return 'dockerfile';
		case 'propose_main':
		case 'update_main':
			return 'main';
		case 'set_verify':
			return 'verify';
	}
	return op.op;
}

function sectionKind(key: string): PlaybookChangeKind {
	if (key.startsWith('learning')) return 'learning';
	if (key.startsWith('script:')) return 'script';
	return key as PlaybookChangeKind;
}

// Pairs a version's proposals with the diffs they made, so each change is shown once with its reason
export function versionChanges(
	before: PlaybookContent | undefined,
	after: PlaybookContent,
	ops: PlaybookAppliedOp[] = []
): PlaybookChange[] {
	// Learnings an applied proposal touched get a section of their own
	const learningIds = new Set<string>();
	for (const op of ops) {
		const key = opSectionKey(op);
		if (op.status === 'applied' && key.startsWith('learning:') && key !== 'learning:') {
			learningIds.add(key.slice('learning:'.length));
		}
	}

	// Every other difference comes from comparing the whole versions, without those learnings so they are not shown twice
	const withoutTouched = (c: PlaybookContent): PlaybookContent => ({
		...c,
		learnings: (c.learnings ?? []).filter((l) => !learningIds.has(l.id))
	});
	const sections = new Map(
		changedSections(before && withoutTouched(before), withoutTouched(after)).map((s) => [s.key, s])
	);
	for (const id of learningIds) {
		const a = before?.learnings?.find((l) => l.id === id);
		const b = after.learnings?.find((l) => l.id === id);
		const section: PlaybookSection = {
			key: `learning:${id}`,
			label: `Learning ${id}`,
			language: 'plain',
			before: a ? learningLine(a) : '',
			after: b ? learningLine(b) : ''
		};
		if (section.before !== section.after) sections.set(section.key, section);
	}

	// Only intermediate content could tell apart several applied proposals to one part, so their combined diff stays a change of its own
	const appliedPerKey = new Map<string, number>();
	for (const op of ops) {
		if (op.status !== 'applied') continue;
		const key = opSectionKey(op);
		appliedPerKey.set(key, (appliedPerKey.get(key) ?? 0) + 1);
	}

	// Each proposal becomes a change, and an applied one takes the diff of the part it edited when it is the only one editing it
	const changes: PlaybookChange[] = ops.map((op, i) => {
		const key = opSectionKey(op);
		const section =
			op.status === 'applied' && appliedPerKey.get(key) === 1 ? sections.get(key) : undefined;
		if (section) sections.delete(key);
		return { key: `op:${i}`, label: opLabel(op), kind: sectionKind(key), op, section };
	});

	// Whatever no proposal explains, such as a hand edit, is a change of its own
	for (const section of sections.values()) {
		changes.push({
			key: section.key,
			label: section.label,
			kind: sectionKind(section.key),
			section
		});
	}
	return changes;
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

const opStatusLabels: Record<PlaybookAppliedOp['status'], string> = {
	applied: 'Applied',
	held: 'Held for review',
	rejected: 'Rejected'
};

export function opStatusLabel(status: PlaybookAppliedOp['status']) {
	return opStatusLabels[status];
}

// The editor language of an operation's content
export function opLanguage(op: PlaybookAppliedOp): CodeLanguage {
	if (op.op === 'set_dockerfile') return 'dockerfile';
	if (op.op === 'set_setup') return 'shell';
	if (op.op === 'set_verify') return 'json';
	const shebang = op.content?.split('\n', 1)[0] ?? '';
	return shebang.includes('python') ? 'python' : 'shell';
}
