# Screenshot kit

These scripts seed a throwaway Umpteenth with two weeks of believable history and capture every screenshot the docs use, in light and dark.
They drive the API and the scripted fake model of an `e2etest` build, so they break when those change: fix the script that fails, not the data by hand.

## Run it

From the repository root:

1. Build the sandbox image from this checkout under a tag of its own, so the shared `ghcr.io/stonith404/umpteenth-sandbox:latest` stays as it is:
   ```bash
   docker buildx build --load -t umpteenth-docs-sandbox:test docker/sandbox
   ```
   Then start the end-to-end stack on its own port and volume, so it can't touch an instance on 8080:
   ```bash
   docker compose -p umpteenth-docs -f tests/setup/docker-compose.yml -f .claude/skills/update-docs/scripts/screenshots/override.yml up -d --build
   ```
2. Seed the workspace, the Hacker News digest that graduates to a script, the other jobs and a manual playbook edit, then plan the move into the past two weeks:
   ```bash
   cd .claude/skills/update-docs/scripts/screenshots && python3 setup.py && python3 hn.py && python3 others.py && python3 manual.py && python3 plan.py
   ```
   After `hn.py`, the same stack also records the landing page's live demo, the digest's runs #1 and #6, into `frontend/src/demo/fixtures.ts`:
   ```bash
   python3 demo.py
   ```
3. Move the history back two weeks while the stack is stopped, so the dashboard and the graduation chart spread over days:
   ```bash
   docker compose -p umpteenth-docs stop
   ```
   ```bash
   docker run --rm -v umpteenth-docs-shots-data:/data -v "$PWD":/plan python:3.13-slim python /plan/shift.py
   ```
   ```bash
   docker compose -p umpteenth-docs start
   ```
4. Capture everything, or only the names you pass:
   ```bash
   node shots.mjs
   ```
   ```bash
   node shot-new-job.mjs
   ```
5. Look at every PNG in `docs/src/assets/screens/` in both themes, then remove the stack:
   ```bash
   docker compose -p umpteenth-docs down -v
   ```

## Files

| File | Does |
|---|---|
| `override.yml` | Moves the stack to port 18086 and its own volume, with the sandbox image from step 1 |
| `lib.py` | API client, run helpers and the scripted model turns shared by the seed scripts |
| `setup.py` | Resets the stack, renames the fake model to Claude models with catalog prices, adds secrets, MCP servers and jobs |
| `hn.py` | Takes the Hacker News digest from Explore through Assisted to Scripted, with reflection and a change held for review |
| `others.py` | Gives the stale PR digest, dependency report, release notes and uptime check their runs |
| `manual.py` | Adds a hand-made playbook version to the digest |
| `demo.py` | Records the digest's first run and its first scripted run for the landing page's live demo, with fictional stories, call IDs and user |
| `plan.py`, `shift.py` | Plan and apply the move of every run into the last two weeks, directly in the SQLite database |
| `pb/` | The toolkit and main scripts the seeded playbooks carry |
| `cap.mjs` | Playwright helpers: sign-in, the user name swap, settling animations |
| `shots.mjs` | One entry per screenshot: URL, what to wait for, and the crop |
| `shot-new-job.mjs` | The New job questions screen for a flaky test report and the review screen for the Hacker News digest, with the compile call answered by the script, since the shift leaves the scripted model without a usable key |

## Rules the captures follow

- No visible "test", "fake" or "E2E": `cap.mjs` swaps the signed-in user for Ada Park, the local sandbox image for `ghcr.io/stonith404/umpteenth-sandbox:latest` and the local origin for `umpteenth.example.com`.
- No real third-party content: the digest fetches the live Hacker News front page, so `shots.mjs` replaces its headlines with fixed fictional stories before capturing. Do the same for any new screenshot that shows fetched content.
- 1440 by 900 viewport, device scale factor 2 (1.5 for tall pages), each PNG under about 600 KB.
