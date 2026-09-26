<div align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="branding/lockup/lockup-on-dark.svg">
    <img src="branding/lockup/lockup-on-light.svg" alt="Umpteenth" width="280">
  </picture>
</div>

<br>

Umpteenth is a self-hosted app for agentic jobs you describe in plain language.
Each run gets its own disposable sandbox, where an LLM agent works with shell tools and the MCP servers you connect.
Jobs run on a schedule, from a webhook or on demand, and every run is recorded with a live timeline, its cost and its outputs.

What makes Umpteenth different is that jobs get better at their work.
After every run, reflection turns what the agent learned into playbook changes and tested scripts, and once a job has done the same steps three runs in a row, it graduates to a script that runs without the agent and costs close to nothing.
If the script fails, the agent takes over in the same sandbox and repairs it.

## Setup

Umpteenth runs as a Docker container next to Docker Engine, and needs an OpenID Connect provider or GitHub for sign-in and an LLM provider such as Anthropic or any OpenAI-compatible endpoint.

Visit the [documentation](https://umpteenth.dev/getting-started/installation/) for the setup guide and more information.

## Contribute

You're very welcome to contribute to Umpteenth! Please follow the [contribution guide](CONTRIBUTING.md) to get started.
