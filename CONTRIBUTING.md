# Contributing

Thanks for the interest to contribute to Umpteenth. We welcome clear bug reports, focused feature proposals, documentation improvements, and well-tested code changes.

## Before you start

- Search existing issues and pull requests before opening a duplicate.
- Discuss changes in an issue before investing time in an implementation. This excludes bug fixes and minor documentation updates.
- Never disclose a suspected vulnerability in a public issue, discussion, or pull request. Follow the repository's [security policy](SECURITY.md).

## AI Usage Policy

We have nothing against using AI tools to assist your development. However, AI must not replace the human contribution behind a new feature. **Simply copying a feature request into an AI tool, generating code, and submitting the result as a pull request is not accepted.** Maintainers can already do that themselves, so this workflow provides no meaningful advantage to the project.

This restriction applies to new features, not bug fixes. For feature contributions, we expect meaningful human involvement such as thoughtfully prompting and iterating, making implementation decisions and reviewing and reflecting the output.

### Guidelines for Using AI Tools

To keep contributions reviewable and high-quality, please follow these guidelines when using AI tools:

1. **Do not submit prompt-only feature implementations.** A new feature must include meaningful human input beyond passing the feature request to an AI tool and submitting its output. This restriction does not apply to bug fixes.
2. **Understand every line.** You must be able to explain what your code does and why, in your own words. "The AI wrote it" is not an acceptable answer to a reviewer's question.
3. **Test before submitting.** Review and test all code manually, as a human, before opening a PR. Don't trust the AI's claim that it works.
4. **Write your own words.** Don't paste AI-generated text into issues, comments, or PR descriptions. Walls of generated text make discussions harder, not easier.

Feature PRs that appear to be low-effort AI output may be closed without a detailed review.

## Getting started

### Submit a Pull Request

Before you submit the pull request for review please ensure that

- The pull request title follows the [Conventional Commits specification](https://www.conventionalcommits.org), since it decides the next version and becomes the changelog entry:

  `<type>[optional scope]: <description>`

  example:

  ```
  fix: keep the run timeline scrolled to the latest step
  ```

  Where `type` can be:
  - **feat** - a new feature
  - **fix** - a bug fix
  - **perf** - a performance improvement
  - **docs** - documentation only changes
  - **refactor** - a code change that neither fixes a bug nor adds a feature
  - **chore** - maintenance such as dependency updates or CI changes

- Your pull request has a detailed description
- You run `pnpm format` and `pnpm lint` and fix all errors
- You run `make lint` in the `backend` folder if you changed the backend

### Development Environment

Umpteenth consists of a frontend and backend. In production the frontend gets embedded into the backend binary, but in development they run as separate processes to enable hot reloading.

#### 1. Install required tools

- [Node.js](https://nodejs.org/en/download/) >= 24
- [pnpm](https://pnpm.io/installation) >= 10
- [Go](https://golang.org/doc/install) >= 1.27
- [Docker](https://docs.docker.com/get-docker/), which the backend uses to create the sandboxes
- [Git](https://git-scm.com/downloads)

#### 2. Setup

##### Backend

The backend is built with [Huma](https://huma.rocks) and [sqlc](https://sqlc.dev) and written in Go. To set it up, follow these steps:

1. Copy `config.example.yml` from the project root to `backend/config.yml`
2. Edit `backend/config.yml`:
   - Set `app.env` to `development` and `app.url` to `http://localhost:3000`
   - Set `app.encryption_key` to the output of `openssl rand -base64 32`
   - Add a sign-in provider under `auth.providers` and register `http://localhost:3000/api/auth/callback/<id>` as its redirect URI, where `<id>` is the provider's key
3. Open the `backend` folder
4. Start the backend with `go run -tags exclude_frontend,exclude_ump ./cmd/umpteenth`

The `exclude_frontend` tag skips the embedded UI, and `exclude_ump` compiles the `ump` CLI for the sandboxes at runtime instead of embedding it. Environment variables in `backend/.env` are read too and win over the config file.

##### Frontend

The frontend is built with [SvelteKit](https://svelte.dev/docs/kit) and written in TypeScript. To set it up, follow these steps:

1. Open the project root folder
2. Install the dependencies with `pnpm install`
3. Start the frontend with `pnpm dev`

You're all set! The application is now listening on `localhost:3000`. The backend gets proxied through the frontend in development mode.

After changing the API, regenerate the OpenAPI spec and the TypeScript types with `pnpm gen:api`.

##### Documentation

The documentation at [umpteenth.dev](https://umpteenth.dev) is built with [Astro Starlight](https://starlight.astro.build) and lives in `docs/`. Start it with `pnpm docs`.

### Testing

If you are contributing to a new feature please ensure that you add tests for it.

#### End-to-end tests

We are using [Playwright](https://playwright.dev) for end-to-end testing.

The tests are located in the `tests` folder at the root of the project. They run against a stack built with the `e2etest` build tag, which adds a fake LLM provider and test-only routes, so no real model is needed.

The tests can be run like this:

1. Install the dependencies from the root of the project `pnpm install`
2. Start the test environment by running `docker compose -f tests/setup/docker-compose.yml up -d --build`
3. Run the tests with `pnpm test`

If you make any changes to the application, you have to rebuild the test environment by running `docker compose -f tests/setup/docker-compose.yml up -d --build` again.

#### Unit tests

In the backend we are using unit tests with the built-in Go testing framework. The tests are located in the same folder as the code they are testing and have the `_test.go` suffix.

To run the tests, simply run `make test` from the root of the `backend` folder.
