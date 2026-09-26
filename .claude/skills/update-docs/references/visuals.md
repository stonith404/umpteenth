# Diagrams and screenshots

Add a picture where it shows a mechanism or a screen faster than prose: where data flows, which state follows which, what a sandbox can reach, what a screen looks like.
If a sentence says it as fast, write the sentence.
Pictures only work in `.mdx` pages.

## Contents

- Using an existing diagram
- Drawing a new diagram
- Checking a diagram
- Screenshots
- Capturing screenshots

## Using an existing diagram

Import the component from `docs/src/components/diagrams/` and place it right after the paragraph that introduces its idea:

```mdx
import NetworkModes from '../../../components/diagrams/NetworkModes.astro';

<NetworkModes />
```

Each diagram has a default caption. Pass `caption="..."` when the page needs a different emphasis, and don't repeat the caption in the prose.

## Drawing a new diagram

Copy the structure of `docs/src/components/diagrams/WhereThingsRun.astro`:

- Inline SVG inside `<Figure>` from `docs/src/components/Figure.astro`, with `role="img"`, an `aria-label` and a `caption` prop that state the one claim of the picture.
- A `viewBox` 680 units wide, the width of the content column, so text renders at 1:1: titles 13px, subtitles 11.5px, edge labels 11px, through the class sets below. Wider content scrolls sideways on phones instead of shrinking.
- Only the Tailwind class sets from `docs/src/components/diagrams/classes.ts`, which use the `dg-*` colors from `docs/src/styles/global.css`, so the drawing works in light and dark: `dg.zone` (`dg.zoneDashed` for things that get thrown away), `dg.zoneLabel`, `dg.box` (`dg.boxLime`, `dg.boxMuted`, `dg.boxDanger`, or `dgBox(tone)`), `dg.title` and `dg.sub` (`dgTitle(lime)` and `dgSub(lime)` on a lime box), `dg.code`, `dg.edge` (`dg.edgeDashed`, `dg.edgeLime`, `dg.edgeLimeDashed`, `dg.edgeDanger`), `dg.arrowhead` (`dg.arrowheadLime`, `dg.arrowheadDanger`), `dg.edgeLabel` (`dg.edgeLabelLime`, `dg.edgeLabelDanger`), `dg.dot`, `dg.bar`.
- Each variant is a complete set, so use one set per element instead of adding a second color utility on top: two utilities for the same property are settled by stylesheet order, not class order.
- Lime marks the one element the drawing is about. Red marks something blocked.
- The brand's dither (`dither.ts`) for modes: loose dots while a job explores, the solid lime bar once it runs as a script.
- Label every arrow with what travels along it. Put edge labels in the gaps between columns, never across a box or a zone edge.
- Prefix marker and pattern ids with a short component prefix, so two diagrams on one page don't clash.
- A visual the shared sets don't cover gets its own class set in the component's frontmatter, built from the `dg-*` colors, like `cc` in `CostCurve.astro`.
- Check every fact the drawing shows in the code: status names, conditions, counts, ports, network rules.

## Checking a diagram

1. Add a temporary page, for example `docs/src/content/docs/dg-test.mdx`, that renders the diagram, and open http://localhost:4321/dg-test/ in light and dark.
2. Run this in the page's console, and fix everything it prints:
   ```js
   const out = [];
   document.querySelectorAll('figure svg[role="img"]').forEach((svg, i) => {
     const rects = [...svg.querySelectorAll('rect[rx]')].map(r => { const b = r.getBBox(); return [b.x, b.x + b.width, b.y, b.y + b.height]; });
     const labels = svg.querySelectorAll('text[class*="stroke-dg-card"]');
     labels.forEach(t => { const b = t.getBBox(); rects.forEach(([l, r, top, bot]) => { const vert = b.y < bot && b.y + b.height > top; if (vert && ((b.x < l && b.x + b.width > l) || (b.x < r && b.x + b.width > r))) out.push(`fig ${i}: label "${t.textContent.trim()}" crosses an edge`); }); });
     svg.querySelectorAll('text[class*="text-[13px]"], text[class*="text-[11.5px]"]').forEach(t => { const b = t.getBBox(); const box = t.parentNode.querySelector('rect'); if (box) { const bb = box.getBBox(); if (b.x + b.width > bb.x + bb.width - 8) out.push(`fig ${i}: text "${t.textContent.trim()}" is tight`); } });
   });
   out;
   ```
3. Delete the temporary page.

## Screenshots

`docs/src/components/Screenshot.astro` shows `docs/src/assets/screens/<name>-light.png` or `<name>-dark.png`, whichever matches the reader's theme, and fails the build if either file is missing:

```mdx
import Screenshot from '../../../components/Screenshot.astro';

<Screenshot name="run-timeline" alt="A finished run's timeline with a shell command and its output" caption="The Timeline tab of a finished run." />
```

Retake a screenshot when the screen it shows changes. Astro converts the PNGs to WebP at build time.

## Capturing screenshots

Capture from a throwaway instance with fake data, never from a real one.
[scripts/screenshots/](../scripts/screenshots/README.md) seeds that instance and retakes every screenshot the docs use, so start there, and add an entry to its `shots.mjs` for a new screenshot.
The steps it automates, for a screenshot it doesn't cover:

1. Start the end-to-end stack on its own port and volume, so it can't collide with a running instance on 8080. An override file next to the compose file does it:
   ```yaml
   services:
     umpteenth:
       image: umpteenth-docs:test
       ports: !override
         - '18086:8080'
       environment:
         APP_URL: http://localhost:18086
   volumes:
     umpteenth-test-data:
       name: umpteenth-docs-shots-data
   ```
   ```bash
   docker compose -p umpteenth-docs -f tests/setup/docker-compose.yml -f override.yml up -d --build
   ```
2. Seed it through the test routes: `POST /api/test/reset` wipes the data and makes a scripted fake model the default, `POST /api/test/session` signs you in, and `POST /api/test/llm-script` queues the fake model's answers (see `tests/utils/run.util.ts` and `tests/specs/*.spec.ts`). Runs use real sandboxes, so shell commands really run.
3. Make the data look like a real instance: plausible job names and costs, the fake provider and model renamed through the API, and no visible "test", "fake" or "E2E" strings. Replace the signed-in user's name in the DOM before capturing. Replace real third-party content a run fetched, such as news headlines, with neutral fictional text.
4. Capture with Playwright from `tests/node_modules`, at a 1440 by 900 viewport, device scale factor 2, once with `colorScheme: 'light'` and once with `'dark'`. Crop to what the page discusses, finish animations first (`document.getAnimations().forEach(a => a.finish())`), and keep each PNG under about 600 KB.
5. Look at both PNGs before you use them, then stop the stack with `docker compose -p umpteenth-docs down -v`.
