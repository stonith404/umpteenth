---
title: Backups and migration
description: Back up and restore an Umpteenth instance, and move jobs, playbooks and settings to another instance with export and import.
---

A full backup of Umpteenth has three parts: the database, the stored files and the encryption key in `config.yml`.
To carry jobs, playbooks, MCP servers and settings to another instance without their run history, use [export and import](#export-and-import).

## Parts of a backup

| Part | Location |
|---|---|
| Database | `data/umpteenth.db` on a single node, or your Postgres database |
| Stored files | Artifacts, long tool outputs and image build logs: `data/blobs` with the default `filesystem` backend, the bucket with `s3`, the database itself with `database` |
| Encryption key | `app.encryption_key` in `config.yml`, or wherever you set `APP_ENCRYPTION_KEY` |

Without the encryption key, Umpteenth can't decrypt the secrets, model API keys and MCP logins it stored, and everyone has to sign in again, so keep the key with every backup.

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
A run in flight doesn't survive the stop, so pick a moment when nothing runs.

:::tip[Back up without stopping]
With `sqlite3` installed on the host, `sudo sqlite3 data/umpteenth.db ".backup umpteenth-backup.db"` copies the live database, and a copy of `config.yml` and `data/blobs` completes the backup.
To restore that file, copy it to `data/umpteenth.db` and delete `data/umpteenth.db-wal` and `data/umpteenth.db-shm`, or SQLite replays the old write-ahead log into it.
:::

## Back up Postgres

Dump the database with `pg_dump`, here against the Postgres service of the repository's `docker-compose.ha.yml`:

```bash
docker compose -f docker-compose.ha.yml exec -T postgres pg_dump -U umpteenth -Fc umpteenth > umpteenth.dump
```

Copy the stored files as well, unless `file_storage.backend` is `database`, which keeps them in the dump.

## Restore

1. Stop every Umpteenth container.
2. Put back `config.yml` with the encryption key that was in use when you took the backup.
3. Put back the database.
   For a single node, replace `data/` with the one from the backup.
   For Postgres, load the dump with `pg_restore`:

   ```bash
   docker compose -f docker-compose.ha.yml exec -T postgres pg_restore -U umpteenth -d umpteenth --clean --if-exists < umpteenth.dump
   ```

4. Put back the stored files: `data/blobs`, or the bucket.
5. Start Umpteenth.

To restore onto a new host, put `docker-compose.yml`, `config.yml` and `data/` into one directory there and run `docker compose up -d`.
Compose pulls the app image, and Umpteenth pulls the sandbox image when it starts.
At the first run of a job with its own Dockerfile, Umpteenth builds the job's image again, unless your registry holds it.

## Export and import

`umpteenth export` writes a workspace's jobs, playbooks, MCP servers and settings to a JSON file, and `umpteenth import` recreates them in a workspace on the same or another instance.
Use them to copy jobs to a second instance, or to move from SQLite to Postgres, since Umpteenth has no database converter.
The run history stays behind.

Both commands need an API token from the workspace you export from or import into.
Create one under **Settings → API tokens** with **Create API token**, and delete it once you're done.
Import creates providers and changes settings, so its token has to belong to an admin or the owner of the workspace.

### Export

Run the export inside the container, and let your shell write the file on the host:

```bash
docker exec -e UMPTEENTH_API_TOKEN=ump_… umpteenth /app/umpteenth export --url http://localhost:8080 --history > umpteenth-export.json
```

`--history` includes every playbook version of each job, and without it the export holds only the current one.

The file holds:

- Jobs with their instruction, settings, schedule, secret mappings, MCP servers and playbook.
- Providers with their models and prices, without API keys.
- MCP servers, without their OAuth logins and without values that look like credentials.
- Secret names, without values.
- The workspace settings, without the notification webhook URL.

Runs, job state, webhook tokens, API tokens and users stay behind.

:::caution[Read the file before you share it]
Umpteenth recognizes credentials by their names and shapes, so a token you pasted into an instruction or a playbook lands in the file as written.
:::

### Import

Start with a dry run, which prints what the import would do and changes nothing:

```bash
docker exec -i -e UMPTEENTH_API_TOKEN=ump_… umpteenth /app/umpteenth import --url http://localhost:8080 --dry-run /dev/stdin < umpteenth-export.json
```

Run the same command without `--dry-run` to import.
The report lists what the import created and skipped, and under `Left to do` the steps you finish by hand, such as setting a provider's API key, creating a secret or logging in to an MCP server.

The import matches everything by name and creates only what the workspace lacks.
An existing provider gets the models it's missing, and an existing job the secret mappings and MCP servers it's missing.
Everything else that exists stays as it is, apart from the workspace settings, which take the export's values.
A second import creates nothing twice, so run it again once you've created the secrets from `Left to do`, and it attaches them to their jobs.

To create secrets with their values in the same pass, write them to a dotenv file in `./data` on the host, which the container sees under `/app/data`:

```ini title="data/secrets.env"
GITHUB_TOKEN=ghp_...
SLACK_BOT_TOKEN=xoxb-...
```

Add `--secrets /app/data/secrets.env` to the import, and delete the file once it has finished.

Check two things about the imported jobs:

- They keep their schedule, so a scheduled job starts running on the target at its next slot.
  For a move to a new instance, [switch off its schedule](../../guides/jobs/#stopping-automatic-runs) on one side, or both instances run it.
- They get new IDs, so their webhook URLs change, and they have no webhook token until you generate one ([Triggers and schedules](../../guides/triggers/#webhooks)).
