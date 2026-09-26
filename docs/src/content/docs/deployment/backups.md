---
title: Backups and migration
description: Back up an Umpteenth instance, restore it, and move jobs, playbooks and settings to another instance with export and import.
---

A full backup of Umpteenth has three parts: the database, the stored files and the encryption key in `config.yml`.
To carry jobs, playbooks, MCP servers and settings to another instance without their run history, use [export and import](#export-and-import).

## Parts of a backup

| Part | Location |
|---|---|
| Database | `data/umpteenth.db` on a single node, or your Postgres database |
| Stored files | Artifacts, long tool outputs and image build logs: `data/blobs` with the default `filesystem` backend, the bucket with `s3`, the database itself with `database` |
| Encryption key | `app.encryption_key` in `config.yml`, or wherever you set `APP_ENCRYPTION_KEY` |

Umpteenth encrypts secret values, model API keys and MCP logins in the database with the encryption key, and signs sessions with it.
Restore a database under a different key and your jobs and runs come back, but Umpteenth can't decrypt the secrets, keys and logins it stored, and everyone has to sign in again.
Umpteenth can't re-encrypt stored values under a new key, so keep the key with every backup.

## Back up a single node

With the default setup, `config.yml` and the `data/` directory next to `docker-compose.yml` hold everything.
Stop the container, copy both, and start it again:

```bash
docker compose stop umpteenth
```

```bash
sudo tar -czf umpteenth-backup.tar.gz config.yml data
```

```bash
docker compose start umpteenth
```

The container writes `data/` as root, hence `sudo`.
A run in flight doesn't survive the stop, so pick a moment when nothing runs ([Upgrades and maintenance](../upgrading/#after-a-restart) lists what survives a restart).

To back up without stopping, let SQLite copy the live database with its `.backup` command.
The Umpteenth image doesn't contain the `sqlite3` tool, so install it on the host and run:

```bash
sudo sqlite3 data/umpteenth.db ".backup umpteenth-backup.db"
```

Then copy `config.yml` and `data/blobs` next to `umpteenth-backup.db`.

## Back up Postgres

Dump the database with `pg_dump`, here against the Postgres service of the repository's `docker-compose.ha.yml`:

```bash
docker compose -f docker-compose.ha.yml exec -T postgres pg_dump -U umpteenth -Fc umpteenth > umpteenth.dump
```

The backup of the stored files depends on `file_storage.backend`.
The default `filesystem` backend applies on Postgres too, so copy the directory in `file_storage.path`, which is `data/blobs` unless you changed it.
With `s3`, copy the bucket with your storage provider's tools, and with `database` the dump contains them.

## Restore

1. Stop every Umpteenth container.
2. Put back `config.yml` with the encryption key that was in use when you took the backup.
3. Put back the database.
   For a single node, replace `data/` with the one from the backup.
   To restore a `.backup` file, copy it to `data/umpteenth.db` and delete `data/umpteenth.db-wal` and `data/umpteenth.db-shm`, since SQLite otherwise replays the old write-ahead log into the restored file.
   For Postgres, load the dump with `pg_restore`:

   ```bash
   docker compose -f docker-compose.ha.yml exec -T postgres pg_restore -U umpteenth -d umpteenth --clean --if-exists < umpteenth.dump
   ```

4. Put back the stored files: `data/blobs`, or the bucket.
5. Start Umpteenth.

Runs that were in flight when you took an online backup come back as running.
Umpteenth fails them within two minutes of starting, with an error that begins with `interrupted:`.

To restore onto a new host, build the two images there first, as in [Installation](../../getting-started/installation/).

## Export and import

`umpteenth export` reads an instance's configuration through the REST API and writes it as JSON, and `umpteenth import` recreates it on the same or another instance.
Use them to copy jobs to a second instance or to seed a fresh install.
Moving from SQLite to Postgres works the same way, since Umpteenth has no database converter.
In each case the run history stays behind.

Both commands need an API token of the instance they talk to.
Create one under **Settings → API tokens** with **Create token**, and delete it once you're done, since every token has full access.

### Export

Run the export inside the container:

```bash
docker exec -e UMPTEENTH_API_TOKEN=<token> umpteenth /app/umpteenth export --history > umpteenth-export.json
```

Without `--history`, the export holds only the current playbook version of each job.
Inside the container, both commands talk to `http://localhost:8080`, unless the container's environment sets `UMPTEENTH_URL` or `APP_URL`.

From another machine with the Umpteenth image, pass the instance's URL:

```bash
docker run --rm -e UMPTEENTH_API_TOKEN=<token> ghcr.io/stonith404/umpteenth:latest export --url https://umpteenth.example.com --history > umpteenth-export.json
```

[Server CLI](../../reference/server-cli/) lists every flag of both commands.

### Contents of an export

An export contains:

- Providers with their models, prices and capabilities, without API keys.
  A model you turned off comes out turned off on the target, even where the target had it on.
- Secret names, without values.
- MCP servers, without OAuth logins.
  Umpteenth leaves out environment variables and headers whose name or value looks like a credential, and replaces credential-like arguments and URL parts with `REDACTED`.
  Values built from `{{secret:NAME}}` references stay as they are, since the reference holds no credential.
- Jobs with their instruction, spec, settings, schedule, **Enabled** state, model, secret mappings, MCP servers with their tool allow-lists, and playbook.
- Workspace settings: the default models, sandbox defaults, daily spend limit, retention, notification events and the name of the signing secret, without the notification webhook URL.

Runs with their timelines and artifacts stay behind, and so do job state, webhook tokens, API tokens, users and image builds.

:::caution[Read the file before you share it]
Umpteenth detects credentials by their names and shapes.
A token pasted into an instruction or a playbook lands in the file as written, and so does an MCP value with an ordinary name and an unfamiliar format.
:::

### Import

Start with a dry run, which reports what the import would do and changes nothing:

```bash
docker exec -i -e UMPTEENTH_API_TOKEN=<token> umpteenth /app/umpteenth import --dry-run /dev/stdin < umpteenth-export.json
```

Run the same command without `--dry-run` to import.
The report sorts each item under `Created` (`Would create` in a dry run), `Skipped` or `Left to do`:

```txt
Created:
  provider OpenRouter
  MCP server linear
  job Weekly dependency report
  settings
Skipped:
  job Nightly triage (exists, only its secrets and MCP servers are filled in)
Left to do:
  Set the API key of provider OpenRouter in Settings → Providers & models
  Create secret GITHUB_TOKEN in Settings → Secrets, then import again to attach it to its jobs
  Log in to MCP server linear under MCP Servers, since exports leave out OAuth logins
```

The import matches items by name:

| Kind | Missing on the target | Already on the target |
|---|---|---|
| Provider | Created without an API key | Kept, and gets the models it lacks |
| Secret | Created if `--secrets` has a value, otherwise listed under `Left to do` | Kept with its value |
| MCP server | Created | Skipped |
| Job | Created with its playbook, or with every version if the export has them | Kept as it is, apart from secret mappings and MCP servers it lacks, which the import adds |
| Settings | Applied | Overwritten with the export's values |

A second import creates nothing twice and attaches the secrets you created after the first one.
It applies the exported settings again, so changes you made to them on the target in between go back to the export's values.

Two more effects show up after an import:

- Imported jobs keep their schedule and their **Enabled** state, so a scheduled job starts running on the target at its next slot.
  For a move to a new instance, [pause](../../guides/jobs/#pausing-a-job) the job on one side, or both instances run it.
- Imported jobs get new IDs.
  Their webhook URLs change, and they have no webhook token until you generate one ([Triggers and schedules](../../guides/triggers/#webhooks)).

### Secret values

`--secrets` fills in secret values from a dotenv file:

```ini title="secrets.env"
GITHUB_TOKEN=ghp_...
SLACK_BOT_TOKEN=xoxb-...
```

The command reads that path itself, and inside the `umpteenth` container it can't see your working directory.
Run it with `docker run` and the file mounted instead:

```bash
docker run --rm -i -v "$PWD/secrets.env:/secrets.env:ro" -e UMPTEENTH_API_TOKEN=<token> ghcr.io/stonith404/umpteenth:latest import --url https://umpteenth.example.com --secrets /secrets.env /dev/stdin < umpteenth-export.json
```

A value for a secret the target already has doesn't replace it.
Delete `secrets.env` once the import has finished.
