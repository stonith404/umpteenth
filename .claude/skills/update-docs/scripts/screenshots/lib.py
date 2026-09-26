"""Helpers that drive the throwaway Umpteenth stack on :18086 for the docs screenshots."""
import json
import os
import sys
import time
import urllib.error
import urllib.request
from datetime import datetime
from zoneinfo import ZoneInfo

BASE = "http://localhost:18086"
HERE = os.path.dirname(os.path.abspath(__file__))
STATE = os.path.join(HERE, "state.json")
BERLIN = ZoneInfo("Europe/Berlin")

# Catalog prices in micro-USD per 1M tokens, from backend/internal/llm/catalog.json
PRICES = {
    "sonnet": dict(i=2_000_000, o=10_000_000, cr=200_000, cw=2_500_000),
    "haiku": dict(i=1_000_000, o=5_000_000, cr=100_000, cw=1_250_000),
}


def load_state():
    if os.path.exists(STATE):
        with open(STATE) as f:
            return json.load(f)
    return {"runs": [], "ids": {}}


def save_state(state):
    with open(STATE, "w") as f:
        json.dump(state, f, indent=2)


class Api:
    def __init__(self, cookie=None):
        self.cookie = cookie

    def call(self, method, path, body=None, headers=None, raw=False):
        data = None
        h = {"Accept": "application/json"}
        if body is not None:
            data = json.dumps(body).encode()
            h["Content-Type"] = "application/json"
        if self.cookie:
            h["Cookie"] = self.cookie
        if headers:
            h.update(headers)
        req = urllib.request.Request(BASE + path, data=data, method=method, headers=h)
        try:
            with urllib.request.urlopen(req, timeout=120) as res:
                text = res.read().decode()
                if raw:
                    return res, text
                return json.loads(text) if text else None
        except urllib.error.HTTPError as e:
            msg = e.read().decode()
            raise RuntimeError(f"{method} {path} -> {e.code}: {msg}") from None

    def get(self, path):
        return self.call("GET", path)

    def post(self, path, body=None, **kw):
        return self.call("POST", path, body if body is not None else {}, **kw)

    def patch(self, path, body):
        return self.call("PATCH", path, body)

    def put(self, path, body):
        return self.call("PUT", path, body)

    def delete(self, path):
        return self.call("DELETE", path)


def session():
    req = urllib.request.Request(BASE + "/api/test/session", data=b"", method="POST")
    with urllib.request.urlopen(req) as res:
        cookie = res.headers.get("Set-Cookie").split(";")[0]
    return cookie


def api():
    state = load_state()
    return Api(state.get("cookie"))


def cost(usage, price):
    p = PRICES[price]
    return (usage.get("input", 0) * p["i"] + usage.get("output", 0) * p["o"] + usage.get("cacheRead", 0) * p["cr"] + usage.get("cacheWrite", 0) * p["cw"]) // 1_000_000


def u(i, o, cr=0, cw=0):
    return {"input": i, "output": o, "cacheRead": cr, "cacheWrite": cw}


def turn(text=None, calls=None, usage=None, delay=0, reasoning=None):
    r = {}
    if text:
        r["text"] = text
    if reasoning:
        r["reasoning"] = reasoning
    if calls:
        r["toolCalls"] = [{"name": n, "args": a} for n, a in calls]
    if usage:
        r["usage"] = usage
    if delay:
        r["delayMs"] = delay
    return r


def finish(summary, outputs=None, status="success", usage=None, delay=0, text=None):
    args = {"status": status, "summary": summary}
    if outputs is not None:
        args["outputs"] = outputs
    return turn(text=text, calls=[("finish", args)], usage=usage, delay=delay)


def op(opname, **fields):
    full = {"op": opname, "rationale": fields.pop("rationale", ""), "id": None, "kind": None, "text": None, "when": None, "name": None, "content": None}
    full.update(fields)
    return full


def reflection(summary, ops=(), used=(), usage=None, delay=0):
    r = {"text": json.dumps({"summary": summary, "usedLearnings": list(used), "ops": list(ops)})}
    if usage:
        r["usage"] = usage
    if delay:
        r["delayMs"] = delay
    return r


def scale(responses, target_usd, price):
    """Scales the token counts of the agent turns so the run costs about target_usd."""
    base = sum(cost(r.get("usage", {}), price) for r in responses)
    if base == 0:
        return responses
    factor = target_usd * 1_000_000 / base
    for r in responses:
        if "usage" in r:
            r["usage"] = {k: int(v * factor) for k, v in r["usage"].items()}
    return responses


def script(a, responses):
    res = a.post("/api/test/llm-script", {"reset": True, "responses": responses})
    return res["pending"]


def pending(a):
    return a.post("/api/test/llm-script", {"reset": False, "responses": []})["pending"]


TERMINAL = {"succeeded", "failed", "cancelled", "timed_out", "skipped"}


def wait_run(a, run_id, timeout=600, reflect=True):
    start = time.time()
    while True:
        run = a.get(f"/api/runs/{run_id}")
        if run["status"] in TERMINAL:
            break
        if time.time() - start > timeout:
            raise RuntimeError(f"run {run_id} did not finish: {run['status']}")
        time.sleep(0.5)
    if reflect:
        while run["reflection"] in ("pending", "running"):
            if time.time() - start > timeout:
                raise RuntimeError(f"reflection of {run_id} did not finish")
            time.sleep(0.5)
            run = a.get(f"/api/runs/{run_id}")
    return run


def latest_run(a, job_id):
    runs = a.get(f"/api/runs?job={job_id}&sort=-number&pageSize=1")["items"]
    if not runs:
        runs = a.get(f"/api/runs?job={job_id}&pageSize=100")["items"]
    return max(runs, key=lambda r: r["number"]) if runs else None


def at(day, hhmm, sec=0):
    """A Berlin wall-clock time in September 2026 as epoch milliseconds."""
    h, m = map(int, hhmm.split(":"))
    return int(datetime(2026, 9, day, h, m, sec, tzinfo=BERLIN).timestamp() * 1000)


def record(state, run, target_ms, label=""):
    state["runs"].append({"id": run["id"], "job": run["jobId"], "number": run["number"], "target": target_ms, "label": label})
    save_state(state)


def describe(run):
    return f"#{run['number']} {run['status']} {run['mode']} cost=${run['cost']/1e6:.3f} refl={run['reflection']} ${run.get('reflectionCost',0)/1e6:.3f} turns={run['turns']} ms={run['msTotal']} v={run.get('reflectionVersion')} err={run.get('error')} rerr={run.get('reflectionError')}"


def trigger(a, job_id, how, body=None, token=None):
    if how == "manual":
        return a.post(f"/api/jobs/{job_id}/runs", body or {})["runId"]
    if how == "schedule":
        before = latest_run(a, job_id)
        a.post(f"/api/test/jobs/{job_id}/fire-schedule")
        for _ in range(100):
            now = latest_run(a, job_id)
            if now and (before is None or now["number"] > before["number"]):
                return now["id"]
            time.sleep(0.2)
        raise RuntimeError("the schedule did not start a run")
    if how == "webhook":
        res = a.call("POST", f"/hooks/{job_id}", body or {}, headers={"Authorization": f"Bearer {token}", "Cookie": ""})
        return res["runId"]
    raise ValueError(how)


def run_one(a, state, job_id, how, target, responses, label="", body=None, token=None, reflect=True, expect_pending=0):
    script(a, responses)
    run_id = trigger(a, job_id, how, body=body, token=token)
    run = wait_run(a, run_id, reflect=reflect)
    left = pending(a)
    print(describe(run), f"left={left}", label, flush=True)
    if left != expect_pending:
        print(f"  WARNING: {left} scripted responses left over", flush=True)
    record(state, run, target, label)
    return run


if __name__ == "__main__":
    print(at(int(sys.argv[1]), sys.argv[2]))
