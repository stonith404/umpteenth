"""Ada adds Rust to the digest topics by hand, which becomes playbook version 4 of the digest."""
from lib import api, at, load_state, save_state

a = api()
s = load_state()
job = s["ids"]["jobs"]["hn"]
pb = a.get(f"/api/jobs/{job}/playbook")
content = pb["content"]
for script in content["toolkit"]:
    if script["name"] == "render_digest":
        old = script["content"]
        new = old.replace(r'|\bk8s\b", re.IGNORECASE)', r'|\bk8s\b|\brust\b", re.IGNORECASE)')
        new = new.replace("★ marks self-hosting, Go and databases.", "★ marks self-hosting, Go, Rust and databases.")
        assert new != old
        script["content"] = new
res = a.put(f"/api/jobs/{job}/playbook", {"content": content, "baseVersion": pb["version"], "summary": "Added Rust to the digest topics"})
s["manualVersion"] = {"job": job, "version": res["version"], "target": at(22, "14:12", 40)}
save_state(s)
print(res["version"], res["author"])
