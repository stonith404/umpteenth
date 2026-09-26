"""Runs inside a throwaway container next to the stopped stack: moves the seeded history into the last two weeks."""
import json
import os
import sqlite3

plan = json.load(open("/plan/plan.json"))
c = sqlite3.connect("/data/umpteenth.db")
c.row_factory = sqlite3.Row

# Each run moves by its own offset, and its events and the playbook versions it wrote move with it
for r in plan["runs"]:
    row = c.execute("SELECT queued_at, started_at FROM runs WHERE id = ?", (r["id"],)).fetchone()
    if row is None:
        print("missing run", r["id"])
        continue
    delta = r["target"] - row["queued_at"]
    c.execute(
        "UPDATE runs SET queued_at = queued_at + :d, started_at = started_at + :d, finished_at = finished_at + :d, "
        "heartbeat_at = heartbeat_at + :d, reflection_requested_at = reflection_requested_at + :d WHERE id = :id",
        {"d": delta, "id": r["id"]},
    )
    c.execute("UPDATE run_events SET ts = ts + ? WHERE run_id = ?", (delta, r["id"]))
    c.execute("UPDATE playbook_versions SET created_at = created_at + ? WHERE source_run_id = ?", (delta, r["id"]))

m = plan["manual"]
c.execute("UPDATE playbook_versions SET created_at = ? WHERE job_id = ? AND version = ?", (m["target"], m["job"], m["version"]))

# Jobs were created shortly before their first run
for job_id, (created, updated) in plan["jobs"].items():
    c.execute("UPDATE jobs SET created_at = ?, updated_at = ? WHERE id = ?", (created, updated, job_id))
    last = c.execute("SELECT MAX(finished_at) FROM runs WHERE job_id = ?", (job_id,)).fetchone()[0]
    if last:
        c.execute("UPDATE job_state SET updated_at = ? WHERE job_id = ?", (last, job_id))
        c.execute("UPDATE playbook_stats SET updated_at = ? WHERE job_id = ?", (last, job_id))

# MCP servers: their tool lists, test times and the OAuth login of linear
for server_id, s in plan["mcp"].items():
    c.execute("UPDATE mcp_servers SET created_at = ?, updated_at = ?, tools_cache = ?, tools_cached_at = ? WHERE id = ?",
              (s["created"], s["created"], json.dumps(s["tools"]), s["cached"], server_id))
    if "oauth" in s:
        o = s["oauth"]
        c.execute("UPDATE mcp_servers SET oauth_supported = 1, oauth_logged_in_at = ?, oauth_expires_at = ?, oauth_refreshable = 1, oauth_credentials = randomblob(96) WHERE id = ?",
                  (o["loggedIn"], o["expires"], server_id))

c.execute("UPDATE secrets SET created_at = ?, updated_at = ?", (plan["secrets"], plan["secrets"]))
c.execute("UPDATE workspaces SET created_at = ?", (plan["workspace"],))
c.execute("UPDATE users SET created_at = ?", (plan["workspace"] + 60_000,))

# The scripted provider becomes the Anthropic provider it stands in for, with a stored key that nothing can decrypt
c.execute("UPDATE providers SET kind = 'anthropic', created_at = ?, api_key_enc = randomblob(72), key_id = (SELECT key_id FROM secrets LIMIT 1) WHERE kind = 'fake'", (plan["workspace"] + 120_000,))
c.execute("UPDATE models SET created_at = ?, synced = 1, follow_catalog = 1", (plan["workspace"] + 121_000,))
c.commit()

for row in c.execute("SELECT j.name, MIN(r.queued_at), MAX(r.queued_at), COUNT(*) FROM runs r JOIN jobs j ON j.id = r.job_id GROUP BY j.name"):
    print(tuple(row))
print(c.execute("SELECT id, name, kind, api_key_enc IS NOT NULL FROM providers").fetchall())
