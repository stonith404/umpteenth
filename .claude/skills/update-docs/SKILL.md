---
name: update-docs
description: Write, update or review pages of the Umpteenth documentation site in docs/ (Astro Starlight) in the house style, with facts checked against the code. Use when a product change needs documenting, when asked to write, edit, extend, fix, restructure or proofread the docs, to add a docs page, diagram or screenshot, or to check the docs for accuracy, broken links or style.
---

# Updating the Umpteenth docs

The docs live in `docs/src/content/docs/` and build with Astro Starlight (`pnpm docs` serves them on :4321, `pnpm docs:build` builds them).
Every page follows one style guide and states only facts the code confirms.
Work through the four steps below in order, even for a one-line fix.

## 1. Find the page and the facts

1. Look up the page that owns the topic in [references/site-map.md](references/site-map.md).
   Explain a topic in full on its owning page only, and link there from the others.
2. Read the whole page first, so your change fits its flow and doesn't repeat it.
3. Confirm every fact you write in the code:
   - UI labels, exactly as spelled: `frontend/src/routes/**` and `frontend/src/lib/**`
   - behavior, defaults, limits and error strings: `backend/internal/<module>/`
   - configuration options, their YAML keys, environment variable names and defaults: the schema in `backend/internal/config/config.go` and `config.example.yml`
   - server commands and flags: `backend/cmd/umpteenth/` and `backend/internal/cmds/`, the sandbox CLI: `backend/cmd/ump/`
   - install and deployment: `docker/`, `docker-compose.yml`, `config.example.yml`
4. Copy identifiers from the current code: the binary `/app/umpteenth`, the images `ghcr.io/stonith404/umpteenth` and `-sandbox`, `$UMPTEENTH_API_TOKEN` in API examples, `X-Umpteenth-*` headers, `ump_` API tokens, `umh_` webhook tokens.
   The sandbox CLI is `ump`, with `UMP_*` variables and `/ump/...` paths.
   The product was called Agent Gig and the sandbox CLI `gig` before, and the docs never mention either old name.

## 2. Write

Read [references/style.md](references/style.md) before writing, and keep [references/phrases.md](references/phrases.md) and [references/structures.md](references/structures.md) open while you edit.
The rules that catch most drafts:

- Address the reader as "you", in active voice, with a real subject doing something. Software does mechanical things (starts, stores, refuses) and never human ones (decides, knows, wants).
- No em dashes, no filler adverbs, no "not X, it's Y", no sentence or heading that starts with What, When, Where, Which, Who, Why or How.
- Lead with what the reader wants to do. State facts directly, with the specific value, label or command.
- Vary sentence length, prefer two items over three, and end paragraphs in different ways.
- One sentence per source line, never wrapped. UI labels in **bold**, navigation as **Settings → General**, identifiers in `backticks`.
- At most one dry joke per page, none on reference pages, never in a warning.

Add a diagram or a screenshot where it shows a mechanism or a screen faster than prose, following [references/visuals.md](references/visuals.md).

## 3. Check

Run all of these before you call the change done:

1. The prose linter on the pages you touched, and fix every error and each warning that is a real problem:
   ```bash
   python3 .claude/skills/update-docs/scripts/lint.py docs/src/content/docs/guides/triggers.mdx
   ```
   Without arguments it lints every page.
2. Score each page you touched from 1 to 10 on directness, rhythm, trust, authenticity and density, as [references/style.md](references/style.md) defines them. Revise anything under 40 of 50.
3. Reread the page as a fact checker who assumes every claim is wrong: find the code for each default, label, flag, path, status and error string, and fix or cut what the code doesn't confirm.
4. Build from a clean cache and fix every error and every warning your change causes (the `i18n` collection warning and the `MODULE_LEVEL_DIRECTIVE` warning are known and harmless):
   ```bash
   rm -rf docs/.astro docs/node_modules/.astro && pnpm docs:build
   ```
5. Check the links in the built site:
   ```bash
   python3 .claude/skills/update-docs/scripts/check_links.py
   ```
6. Look at every page you changed in the browser at http://localhost:4321, in light and dark, when it has a diagram, a screenshot, a table or a component.

## 4. Keep the site whole

- A new page goes into the sidebar in `docs/astro.config.mjs`, which lists every page explicitly, and into [references/site-map.md](references/site-map.md). Use `.mdx` only when the page imports a component, `.md` otherwise.
- A moved or deleted page gets an entry in `redirects` in `docs/astro.config.mjs`, and the links pointing at it change with it.
- A product change usually touches more than its own guide: new configuration options go into **Configuration**, new job fields into **Job settings**, new error messages into **Troubleshooting**, new routes into **REST API**, new server commands into **Server CLI**. Search the docs for the old term to find every page that mentions it:
  ```bash
  grep -rn "the old label" docs/src/content/docs docs/src/components docs/src/pages
  ```
- The landing page (`docs/src/pages/index.astro` and `docs/src/components/landing/`) makes product claims too, so check it when a feature changes.
- A new page needs no search engine work beyond its frontmatter: Starlight lists it in the sitemap, `docs/src/sharedHead.ts` gives it the social card, and `/llms.txt` picks it up for AI assistants.
  `docs/src/routeData.ts` applies `seoTitle` and gives the generated API reference its titles and descriptions.
  The social card `docs/public/og.png` shows the landing page's headline, so it changes with it.
