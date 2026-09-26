#!/usr/bin/env python3
# Checks docs pages against the mechanical rules of the house style in references/style.md
# Errors break a rule outright and make the run fail, warnings point at lines a person should reread
# Usage: lint.py [file ...], which lints every docs page, diagram and landing component without arguments

import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[4]
DOCS = ROOT / 'docs' / 'src'
DEFAULT_GLOBS = ['content/docs/**/*.md', 'content/docs/**/*.mdx', 'components/diagrams/*.astro', 'components/landing/*.astro', 'pages/*.astro']

# Words that are filler in every context the docs use them in
FILLER = re.compile(
	r"\b(just|simply|really|very|quite|basically|essentially|actually|literally|obviously|clearly|of course|in order to|please|seamless(?:ly)?|effortless(?:ly)?|easily)\b",
	re.I,
)

# Openers that announce instead of saying, anchored at the start of a sentence
THROAT = re.compile(
	r"^(Here's|Here is|Let's|Let us|In this (?:guide|page|section)|This (?:page|guide|section) (?:covers|explains|shows|describes|walks)|As (?:mentioned|noted|described|we saw)|Below,|The rest of this|Now that|It's worth|It is worth|Keep in mind)\b"
)

WH_START = re.compile(r"^(What|When|Where|Which|Who|Why|How)\b")
OLD_NAME = re.compile(r"agent-gig|Agent Gig|AGENT_GIG|X-Agent-Gig|agent_gig|\bgig\b|\bGIG_|/gig/")

# Adverbs in -ly that the docs use as plain words, not as filler
LY_OK = set(
	'only early reply apply supply family daily weekly monthly hourly yearly nightly quarterly rely fly july italy anomaly assembly '
	'friendly likely unlikely costly timely orderly multiply comply imply ally rally tally holy ugly silly lonely lovely poly readonly'.split()
)
LY = re.compile(r"\b([A-Za-z]+ly)\b")

# A form of "be" or "get" followed by a participle, the usual shape of the passive voice
PASSIVE = re.compile(
	r"\b(?:is|are|was|were|be|been|being|gets|get|got|getting)\s+(?:\w+ly\s+)?(\w+ed|built|sent|kept|shown|made|held|done|found|lost|left|known|given|taken|written|seen|run|set|read|put)\b",
	re.I,
)

CONTRAST = re.compile(r"\bnot\b[^.]*,\s*but\b|n't\b[^.]*[,;:]\s*it's\b|\bnot (?:just|only)\b", re.I)
SENTENCE_BREAK = re.compile(r"(?<!e\.g)(?<!i\.e)(?<!etc)(?<!vs)[a-z0-9)`*][.!?]\s+[A-Z]")


def strip_inline(text):
	# Inline code, link targets, HTML tags and bold markers aren't prose, so the prose rules skip them
	text = re.sub(r"`[^`]*`", "CODE", text)
	text = re.sub(r"\]\([^)]*\)", "]", text)
	text = re.sub(r"<[^>]+>", "", text)
	return text


def sentence_start(text):
	# A line's first words after list markers, table pipes, bold and link brackets
	return re.sub(r"^(?:[-*+]\s+|\d+\.\s+|>\s*|\|\s*)*", "", text).lstrip('[(')


def lint_markdown(path, lines, report):
	in_front = False
	in_code = False
	in_tag = False
	front_keys = set()
	prose = []

	for i, raw in enumerate(lines, 1):
		line = raw.rstrip('\n')
		stripped = line.strip()

		# Frontmatter holds the title and description, which get the prose rules too
		if i == 1 and stripped == '---':
			in_front = True
			continue
		if in_front:
			if stripped == '---':
				in_front = False
				continue
			key = stripped.split(':', 1)[0]
			front_keys.add(key)
			if key == 'title':
				prose.append((i, '# ' + stripped.split(':', 1)[1].strip().strip('\'"'), False))
			elif key == 'description':
				prose.append((i, stripped.split(':', 1)[1].strip().strip('\'"'), False))
			continue

		# Code blocks only get the old-name check, since their text is copied from the code
		if stripped.startswith('```') or stripped.startswith('~~~'):
			in_code = not in_code
			continue
		if OLD_NAME.search(line):
			report(i, 'error', 'old-name', 'an old name: the product is Umpteenth and its sandbox CLI is ump, so copy the current identifier from the code', line)
		if in_code:
			continue

		if path.suffix == '.md' and (stripped.startswith('import ') or re.match(r"^<[A-Z]", stripped)):
			report(i, 'error', 'md-component', 'components only work in .mdx pages', line)

		# A component whose attributes span several lines isn't prose until its tag closes
		if in_tag:
			in_tag = not stripped.endswith('>')
			prose.append((i, '', False))
			continue
		if stripped.startswith('<') and not stripped.endswith('>'):
			in_tag = True
		if stripped.startswith(('import ', '<', ':::', '{/*', '<!--')) or not stripped:
			prose.append((i, '', False))
			continue
		prose.append((i, line, stripped.startswith('|')))

	for key in ('title', 'description'):
		if key not in front_keys:
			report(1, 'error', 'frontmatter', f'the frontmatter needs a {key}', '')

	for n, (i, line, is_table) in enumerate(prose):
		if not line:
			continue
		text = strip_inline(line)
		is_heading = line.lstrip().startswith('#')
		start = sentence_start(text.lstrip('# ').strip())

		if '—' in text:
			report(i, 'error', 'em-dash', 'no em dashes: use a period, a comma, a colon or parentheses', line)
		if ' – ' in text:
			report(i, 'error', 'en-dash', 'no en dashes as dashes', line)
		for m in FILLER.finditer(text):
			report(i, 'warning' if is_heading else 'error', 'filler', f'cut "{m.group(0)}"' + (', unless the heading quotes a message from the app' if is_heading else ''), line)
		if THROAT.match(start):
			report(i, 'error', 'throat-clearing', 'start with the point', line)

		# Sentences, and headings where it reads naturally, don't open with a Wh- word
		starts = [start] if is_table else [start] + [s for s in re.split(r"[.!?]\s+", start)[1:]]
		if is_table:
			starts = [sentence_start(c.strip()) for c in text.strip('|').split('|')]
		for s in starts:
			if WH_START.match(s):
				report(i, 'warning' if is_heading or is_table else 'error', 'wh-start', 'restructure so the sentence doesn\'t open with a Wh- word', line)

		for m in LY.finditer(text):
			if m.group(1).lower() not in LY_OK and not FILLER.fullmatch(m.group(1)):
				report(i, 'warning', 'adverb', f'"{m.group(1)}": cut it unless it states a technical fact', line)
		for m in PASSIVE.finditer(text):
			report(i, 'warning', 'passive', f'"{m.group(0)}": name who does it, unless it is an adjective', line)
		if CONTRAST.search(text):
			report(i, 'warning', 'contrast', 'state the positive fact instead of a "not X, but Y" contrast', line)

		if is_table or is_heading:
			continue
		# Quoted messages from the app may hold several sentences of their own
		if SENTENCE_BREAK.search(re.sub(r'"[^"]*"', 'QUOTE', start)):
			report(i, 'warning', 'one-sentence-per-line', 'put each sentence on its own line', line)

		# A prose line without closing punctuation, followed by a line that continues it in lowercase, is a wrapped sentence
		nxt = prose[n + 1] if n + 1 < len(prose) else None
		if nxt and nxt[1] and not nxt[2]:
			cont = nxt[1].strip()
			if cont[:1].islower() and not re.search(r"[.!?:;)\]*`]$", line.rstrip()):
				report(i, 'error', 'wrapped', 'a sentence continues on the next line: keep it on one line', line)


def lint_astro(lines, report):
	# Components get the checks that apply to any visible text: dashes and the old name
	for i, line in enumerate(lines, 1):
		if OLD_NAME.search(line):
			report(i, 'error', 'old-name', 'an old name: the product is Umpteenth and its sandbox CLI is ump, so copy the current identifier from the code', line)
		if '—' in line:
			report(i, 'error', 'em-dash', 'no em dashes: use a period, a comma, a colon or parentheses', line)


def main(argv):
	files = [Path(a).resolve() for a in argv] or sorted(p for g in DEFAULT_GLOBS for p in DOCS.glob(g))
	errors = warnings = 0

	for path in files:
		lines = path.read_text(encoding='utf-8').splitlines()
		shown = path.relative_to(ROOT) if path.is_relative_to(ROOT) else path

		def report(line_no, level, rule, message, excerpt):
			nonlocal errors, warnings
			if level == 'error':
				errors += 1
			else:
				warnings += 1
			excerpt = excerpt.strip()
			if len(excerpt) > 100:
				excerpt = excerpt[:97] + '...'
			print(f"{shown}:{line_no}: {level} [{rule}] {message}" + (f"\n    {excerpt}" if excerpt else ''))

		if path.suffix == '.astro':
			lint_astro(lines, report)
		else:
			lint_markdown(path, lines, report)

	print(f"\n{len(files)} files, {errors} errors, {warnings} warnings")
	return 1 if errors else 0


if __name__ == '__main__':
	sys.exit(main(sys.argv[1:]))
