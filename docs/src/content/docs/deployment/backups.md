---
title: Backups and migration
description: Back up and restore an Umpteenth instance, its database, stored files and encryption key, and move it to a new host.
---

A full backup of Umpteenth has three parts: the database, the stored files and the encryption key in `config.yml`.

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

Dump the database with `pg_dump`:

```bash
pg_dump -Fc "postgres://umpteenth:<password>@<host>/umpteenth" > umpteenth.dump
```

Copy the stored files as well, unless `file_storage.backend` is `database`, which keeps them in the dump.

## Restore

1. Stop every Umpteenth container.
2. Put back `config.yml` with the encryption key that was in use when you took the backup.
3. Put back the database.
   For a single node, replace `data/` with the one from the backup.
   For Postgres, load the dump with `pg_restore`:

   ```bash
   pg_restore -d "postgres://umpteenth:<password>@<host>/umpteenth" --clean --if-exists umpteenth.dump
   ```

4. Put back the stored files: `data/blobs`, or the bucket.
5. Start Umpteenth.

To restore onto a new host, put `docker-compose.yml`, `config.yml` and `data/` into one directory there and run `docker compose up -d`.
Compose pulls the app image, and Umpteenth pulls the sandbox image when it starts.
At the first run of a job with its own Dockerfile, Umpteenth builds the job's image again, unless your registry holds it.
