---
title: Skills
seoTitle: Agent skills with SKILL.md
description: Add agent skills with a SKILL.md from GitHub or as zips, attach them to jobs, and let the agent load their instructions and scripts when a task needs them.
---

A skill is a folder of instructions, scripts and reference files for one kind of task, such as filling PDF forms or writing release notes in your house style.
You add it once under **Skills**, attach it to the jobs that need it, and every run of those jobs finds it in `/ump/skills/<name>`.

Skills use the same format as Claude's agent skills, so a folder from [anthropics/skills](https://github.com/anthropics/skills) or one you built for Claude Code works as it is.

## A skill folder

A skill needs a `SKILL.md` whose frontmatter names the skill and says when to use it:

```markdown title="pdf-forms/SKILL.md"
---
name: pdf-forms
description: Fills PDF forms from structured data. Use when a job has to complete or read a PDF form.
---

# PDF forms

Run `scripts/fill.py <form.pdf> <data.json> <out.pdf>` to fill a form.
Field names and their quirks are in [reference.md](reference.md).
```

The `name` takes lowercase letters, digits and single hyphens, up to 64 characters, and must be unique in the workspace.
The `description` takes up to 1,024 characters without XML tags.
Write it for the agent: say what the skill does and the situations that call for it, since the agent sees only this line until it opens the skill.
Umpteenth ignores the other frontmatter keys, such as `license`.

Everything else in the folder is yours to arrange.
A script keeps its executable bit from the zip, and a file that starts with `#!` becomes executable even from a zip made on Windows.

## Add a skill

Open **Skills**, click **Add skill** and pick where the skill comes from.

Under **Link**, paste the GitHub address of the skill's folder, such as `https://github.com/anthropics/skills/tree/main/skills/pdf`, and click **Add skill**.
A link to the folder's `SKILL.md` or to a repository that holds a single skill works too, and so does a link to a `.zip` or `.skill` file on any server.
Umpteenth downloads public repositories only.

A repository such as `https://github.com/crowdin/skills` holds several skills, and a link to it or to the folder they share lists them in the dialog.
Tick the ones you want and click **Add 2 skills** (the button counts your picks), and each skill keeps the link to its own folder for later updates.
The list greys out the skills your workspace already has and those with a broken `SKILL.md`, with the reason under the name.

Under **Upload**, pick a zip of the folder, with `SKILL.md` at the root of the zip or inside one top-level folder.
A `.skill` file from Claude is such a zip.

Umpteenth checks the skill before storing it and shows what is wrong next to the field.
A skill may hold up to 500 files and 32 MiB, and a zip may be 8 MiB.
Umpteenth refuses symlinks, encrypted entries and paths outside the skill folder, and drops the `__MACOSX` folder and `.DS_Store` files that macOS adds.

Click a skill to see its files, read its `SKILL.md` and open any other file.

## Attach skills to a job

In the job's **Settings** tab, pick a skill from **Attach skill** in the **Skills** card and click **Save**.
A job takes up to 20 skills with 64 MiB of files in total.

The **New job** review screen has the same **Skills** card.
The compile step compares your description with each skill's `description` and attaches the ones that fit, marked **Suggested**.
**Attach skill** opens a list of your other skills that you can search by name or description.

## Skills in a run

Before the agent starts, Umpteenth copies the job's skills into the sandbox and notes **Added the skill pdf-forms** on the run's timeline.
The agent's instructions list each skill's name, description and the path of its `SKILL.md`.
The agent reads a `SKILL.md` once a task fits it and opens the other files it links to only when it needs them, so ten attached skills cost the model little more than ten lines.

The files belong to root, so the agent can read and run them but not change them, unless the job has **Run as root** on.
A graduated job's main script can call a skill's scripts by their path, such as `/ump/skills/pdf-forms/scripts/fill.py`, and Umpteenth tells reflection to call them there rather than copy them into the playbook.

:::caution[Review scripts before you upload them]
A skill's scripts run with the agent's user, network access and secrets.
Read a skill you got from elsewhere before you attach it, as you would any script you run on a server.
:::

## Replace or delete a skill

A skill added from a link keeps it, and **Update from link** in the skill's menu downloads the current version from there.
**Replace** takes a new version from a link or an upload, and an upload drops the skill's link.
Its `SKILL.md` must keep the same `name`, because jobs and their scripts find the skill by its folder; add a renamed skill as a new one.
Runs that start after the change get the new version.

**Delete** removes the skill and detaches it from every job, and the confirmation says how many jobs use it.
To delete several at once, tick their checkboxes on the **Skills** page and click **Delete** above the table.
**Download** gives you the stored zip back.
