# Style guide

Every sentence in the docs follows this guide, including captions, table cells, asides and frontmatter descriptions.

## Contents

- Core rules
- Quick checks
- Scoring
- Voice
- Humor
- Formatting
- What not to document

## Core rules

1. **Cut filler phrases.** Remove throat-clearing openers, emphasis crutches and adverbs. See [phrases.md](phrases.md).
2. **Break formulaic structures.** Avoid binary contrasts, negative listings, dramatic fragmentation, rhetorical setups and false agency. See [structures.md](structures.md).
3. **Use active voice.** Every sentence needs a subject doing something. Replace "the file is stored" with "Umpteenth stores the file" and "a token gets created" with "you create a token". No inanimate thing performs a human action ("the log tells you", "the job decides"). Software may be the subject of mechanical verbs (starts, stores, sends, retries, refuses) and never of human ones (decides, wants, knows, thinks, remembers). The product's own metaphor, jobs that learn, is fine where it names the feature.
4. **Be specific.** No vague declaratives ("this matters", "the reasons are structural"). Name the value, the label, the command. Use "every", "always", "never" and "all" only when the code guarantees them.
5. **Put the reader in the room.** Write "you", never "users", "people" or "operators". Specifics beat abstractions.
6. **Vary rhythm.** Mix sentence lengths. Two items beat three. End paragraphs differently. No em dashes, and no en dashes used as dashes: use a period, a comma, a colon or parentheses.
7. **Trust readers.** State facts directly. Skip softening ("might", "you may want to", "consider"), justification and hand-holding.
8. **Cut quotables.** A sentence that sounds like a pull quote or a tagline gets rewritten plainly.

## Quick checks

Before you finish a page:

- Any adverbs? Cut them, unless the adverb is a technical fact ("only the listed domains").
- Any passive voice? Find the actor and make it the subject.
- An inanimate thing doing a human verb? Name the person, or give the program a mechanical verb.
- A sentence or heading starting with What, When, Where, Which, Who, Why or How? Restructure it: "When a run fails, ..." becomes "If a run fails, ..." or "A failed run ...", and "How a run works" becomes "A run, start to finish".
- "Here's what", "here's how", "let's"? Cut to the point.
- "Not X, it's Y"? State Y.
- Three sentences of the same length in a row? Break one.
- A paragraph ending on a punchy one-liner? Vary it.
- A vague declarative? Name the specific consequence.
- Narrator from a distance ("nobody designed this")? Put the reader in the scene.
- Meta-joiners ("the rest of this page", "below, we'll", "as mentioned above")? Delete them.
- "Sits at the intersection of"? Say how the parts connect.
- Three parallel items with identical grammar? Break one or cut to two.
- A tidy closing clause ("each building on the last")? Cut it, or say what connects to what.
- A labelled parallel enumeration ("X (step 1), Y (step 2), Z (step 3)")? Vary it or fold it into prose.
- "The norm, not the exception"? Say it plainly: "this is common".
- Triple parallel subordinate clauses ("how X, what Y and why Z")? Split them into sentences.

## Scoring

Rate every page you write or edit from 1 to 10 on each dimension, and revise anything under 40 of 50:

| Dimension | Question |
|---|---|
| Directness | Does it make statements, or announce them? |
| Rhythm | Is it varied, or metronomic? |
| Trust | Does it respect the reader's intelligence? |
| Authenticity | Does it sound like a person? |
| Density | Is anything cuttable? |

## Voice

- Write as the senior engineer who built Umpteenth, explaining it to a colleague who self-hosts software and knows Docker: friendly, concrete, a bit dry.
- Second person, present tense, American spelling ("behavior", "color").
- Lead with what the reader wants to do, then give the facts they need for it. Background comes after, if at all.
- A real example beats an abstract rule. A short sentence after a long one keeps the reader awake.
- A page starts with its first paragraph, which says in one or two sentences what the page helps you do. No "Introduction" or "Overview" heading.

## Humor

- At most one joke per page, and many pages need none. Reference pages have none. Across the site, about ten.
- Dry, true and about the work: recurring chores, cron syntax, 3 a.m. pages, the Docker socket, the job you've done by hand for the umpteenth time.
- Never at the reader's expense, never in a heading, never in a warning or a security section, and never a pun.
- Good: "Cron expressions are easier to write than to read, so the preset menu covers the common ones."
- Bad: "Say goodbye to boring tasks forever!" (a tagline), "Cron? More like cron-fusing!" (a pun).

## Formatting

- Headings in sentence case. Page titles of one to four words.
- Markdown source holds one sentence per line and never wraps a sentence. A blank line separates paragraphs.
- UI labels in **bold**, spelled as the frontend spells them. Navigation paths as **Settings → General**.
- Code, paths, environment variables, flags, commands, status values and file names in `backticks`.
- Tables for reference data readers compare row by row (environment variables, settings, statuses). Guides prefer prose, steps and examples.
- Every code block names its language (`bash`, `json`, `yaml`, `dockerfile`, `txt` where Expressive Code has no grammar). One command per `bash` block when readers run them one at a time. File snippets carry a title, such as `title="docker-compose.yml"`.
- Asides use Starlight's syntax (`:::note`, `:::tip`, `:::caution`, `:::danger`, optionally `:::caution[Podman]`), two or three per page at most.
- `.mdx` pages may import Starlight's components from `@astrojs/starlight/components`: `Steps` around an ordered list of setup steps, `Tabs` with `TabItem` and a `syncKey` for real alternatives (Docker or Podman, Caddy, nginx or Traefik), `LinkCard` and `CardGrid` on the Start here pages only.
- Links between pages are relative with a trailing slash: `../triggers/` within a section, `../../deployment/security/` across sections, plus `#anchor` where it helps. Starlight builds anchors from headings in lowercase, with spaces turned into hyphens and punctuation dropped.
- Frontmatter holds a `title` and a one-sentence `description` of 70 to 160 characters, which search results and link previews show, so it names the task and the terms a reader would search for (Docker Compose, cron, Slack).
- A page whose short title misses the words people search for adds a `seoTitle` of at most 48 characters, such as `Reverse proxy with Caddy, nginx or Traefik`. It replaces the title in the browser tab, search results and link previews, while the heading on the page stays short. Quote it when it contains a colon.
- Examples use `https://umpteenth.example.com` for the instance, `acme` for the organization and plausible names, never real personal data.

## What not to document

- Internals a self-hoster never sees or acts on: the actor framework, task pools, `LISTEN/NOTIFY`, sqlc, goose, table names, Go packages, the agent's internal tool names, RFC numbers.
- Comparisons with other products.
- Contributor tooling (`backend/scripts/`, `make test`, the `e2etest` build tag, `APP_ENV=test`, `/api/test/*`).
- Features that don't exist yet, even when PLAN.md plans them.
- Benchmarks from "our tests".
- The same fact in full on two pages.
