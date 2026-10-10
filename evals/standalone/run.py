#!/usr/bin/env python3
"""Standalone-help eval: can an agent finish a task with only `fwdctl --help` (no skills installed, no docs)?
Starts evals/standalone/fake_forward.py, runs each task in tasks.json with a headless `claude -p` that has Bash and Read
only and fwdctl on PATH, and scores from the fake's request log and the final answer (no judge model).
Usage: run.py --bin DIR_WITH_FWDCTL [--model sonnet] [--only id,id] [--budget 0.50] [--out DIR]"""
import argparse, json, os, subprocess, sys, tempfile, time, signal

ap = argparse.ArgumentParser()
ap.add_argument("--bin", required=True); ap.add_argument("--model", default="sonnet")
ap.add_argument("--only", default=""); ap.add_argument("--budget", type=float, default=0.50)
ap.add_argument("--out", default="/tmp/standalone-eval"); ap.add_argument("--port", type=int, default=18082)
a = ap.parse_args()
here = os.path.dirname(os.path.abspath(__file__))
tasks = json.load(open(os.path.join(here, "tasks.json")))
if a.only: tasks = [t for t in tasks if t["id"] in a.only.split(",")]
os.makedirs(a.out, exist_ok=True)
rows = []
for t in tasks:
    log = os.path.join(a.out, t["id"] + ".calls.log"); open(log, "w").close()
    srv = subprocess.Popen([sys.executable, os.path.join(here, "fake_forward.py"), str(a.port), os.path.join(here, "routes.json"), log])
    time.sleep(0.7)
    work = tempfile.mkdtemp(prefix="standalone-")  # empty dir: no CLAUDE.md, no skills
    env = dict(os.environ, PATH=a.bin + os.pathsep + os.environ["PATH"], FORWARD_URL=f"http://127.0.0.1:{a.port}",
               FORWARD_USERNAME="demo", FORWARD_PASSWORD="demo", FORWARD_NO_UPDATE_CHECK="1")
    prompt = ("You have the `fwdctl` command for Forward Networks and nothing else to read about it. "
              "Use only the shell. Finish this task, then answer in plain words.\n\nTask: " + t["prompt"])
    cmd = ["claude", "-p", prompt, "--model", a.model, "--output-format", "stream-json", "--verbose", "--setting-sources", "project",
           "--permission-mode", "bypassPermissions", "--tools", "Bash,Read", "--no-session-persistence", "--max-budget-usd", str(a.budget)]
    t0 = time.time()
    p = subprocess.run(cmd, cwd=work, env=env, capture_output=True, text=True, timeout=600)
    srv.send_signal(signal.SIGTERM); srv.wait()
    open(os.path.join(a.out, t["id"] + ".transcript.jsonl"), "w").write(p.stdout)
    calls, answer, cost = 0, "", 0.0
    for line in p.stdout.splitlines():
        try: ev = json.loads(line)
        except Exception: continue
        if ev.get("type") == "assistant":
            calls += sum(1 for c in ev["message"]["content"] if c.get("type") == "tool_use")
        if ev.get("type") == "result": answer, cost = ev.get("result", ""), ev.get("total_cost_usd", 0.0)
    reqs = [json.loads(l) for l in open(log) if l.strip()]
    writes = [r for r in reqs if r["method"] != "GET"]
    gaps = [r["method"] + " " + r["path"].split("?")[0] for r in reqs if not r["routed"]]
    ok = all(s.lower() in answer.lower() for s in t.get("answer_has", []))
    why = []
    if not ok: why.append("answer lacks " + str([s for s in t["answer_has"] if s.lower() not in answer.lower()]))
    if t.get("no_writes") and writes: ok = False; why.append("wrote: " + str([w["method"] + " " + w["path"] for w in writes]))
    if t.get("must_write"):
        m, path = t["must_write"].split(" ", 1)
        hit = [w for w in writes if w["method"] == m and w["path"].split("?")[0] == path]
        if not hit: ok = False; why.append("did not " + t["must_write"])
        elif not all(s in hit[0]["body"] for s in t.get("body_has", [])): ok = False; why.append("body lacks " + str(t["body_has"]))
    rows.append({"id": t["id"], "pass": ok, "tool_calls": calls, "cost": round(cost, 3), "secs": round(time.time() - t0), "why": why, "fixture_gaps": sorted(set(gaps))})
    print(json.dumps(rows[-1]), flush=True)
json.dump(rows, open(os.path.join(a.out, "results.json"), "w"), indent=1)
print(f"\n{sum(r['pass'] for r in rows)}/{len(rows)} passed; {sum(r['tool_calls'] for r in rows)} tool calls; ${sum(r['cost'] for r in rows):.2f}")
