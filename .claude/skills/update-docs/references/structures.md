# Structures to break

Each pattern below reads as template prose. Rewrite it into a direct statement.

## Binary contrasts

- "It's not a workflow builder, it's a job runner." → "Umpteenth runs jobs you describe in plain language."
- "Rather than X, Umpteenth does Y." → "Umpteenth does Y."
- "X isn't just A. It's also B." → "X does A and B."

State the positive fact. Mention the alternative only when readers expect it and need to hear it's missing.

## Negative listings and dramatic fragments

- "No database. No queue. No Redis." → "A single container with SQLite runs the whole thing."
- "One job. One sandbox. Zero leftovers." → "Each run gets its own sandbox, and Umpteenth deletes it afterwards."

## Rhetorical setups

- "The catch?", "The result?", "Here's the thing:", "The best part?"
- "Want to X? Y." → "To X, Y."
- "Ever wondered why...?" → State the reason.

## False agency

- "The playbook decides the mode." → "Umpteenth picks the mode from the playbook."
- "The error tells you what's wrong." → "The error names the missing variable."
- "The compile step understands your intent." → "The compile step extracts the schedule and outputs from your instruction."

## Passive voice

- "Secrets are encrypted at rest." → "Umpteenth encrypts secrets at rest."
- "The token is shown once." → "Umpteenth shows the token once."
- "Runs get cancelled when..." → "Umpteenth cancels runs when..."

## Wh- openers

- "When a run fails, reflection repairs the script." → "After a failed run, reflection repairs the script."
- "What you need: ..." → "You need: ..."
- "How graduation works" (heading) → "Graduation"
- "Where files go" (heading) → "Files and outputs"

## Triads and tidy closers

- "fast, cheap and reliable" → pick the two that matter, or give numbers.
- "..., with each run building on the last." → cut, or say what carries over: "Each run starts with the playbook the previous one updated."
- "how it starts, what it runs and where it stores results" → three sentences, or two facts.

## Meta-joiners and signposts

- "Now that you've installed Umpteenth, let's create a job." → "Create a job with **Create job** on the **Jobs** page."
- "The next section covers..." → Delete; the heading does it.
- "In summary", "To recap", "In short" → Delete the summary, or keep only the summary.

## Quotables

- "Describe it once, and it runs itself forever." → "After three matching runs, the job runs as a script without the agent."
- "Your jobs get smarter every day." → "Reflection adds what a run learned to the playbook after each run."

## Uniform rhythm

Three sentences of the same length in a row, or every paragraph ending on a short punchline, reads as machine rhythm. Combine two sentences, split a long one, or move the short one to the middle.
