#!/usr/bin/env python3
"""Standalone-help eval: can an agent finish a task with only `fwdctl --help` (no skills installed, no docs)?
Starts evals/standalone/fake_forward.py, runs each task in tasks.json with a headless `claude -p` that has Bash and Read
only and fwdctl on PATH, and scores from the fake's request log and the final answer (no judge model).
Usage: run.py --bin DIR_WITH_FWDCTL [--model sonnet] [--only id,id] [--budget 0.50] [--out DIR]"""
import argparse, json, os, socket, subprocess, sys, tempfile, time, signal

ap = argparse.ArgumentParser()
ap.add_argument("--bin", required=True); ap.add_argument("--model", default="sonnet")
ap.add_argument("--only", default=""); ap.add_argument("--budget", type=float, default=0.50)
ap.add_argument("--arm", default="without", choices=["with","without"], help="with: install the skills in the agent's workdir; without: fwdctl --help only")
ap.add_argument("--out", default=os.path.expanduser("~/.cache/standalone-eval")); ap.add_argument("--port", type=int, default=18082)
ap.add_argument("--repeat", type=int, default=1, help="run each task N times and report how many passed (one run of a model is noisy)")
a = ap.parse_args()
here = os.path.dirname(os.path.abspath(__file__))
tasks = json.load(open(os.path.join(here, "tasks.json")))
if a.only: tasks = [t for t in tasks if t["id"] in a.only.split(",")]
os.makedirs(a.out, exist_ok=True)
rows = []
for t in [t for t in tasks for _ in range(a.repeat)]:
    n = sum(1 for r0 in rows if r0["id"] == t["id"]) + 1
    tag = t["id"] + ("" if a.repeat == 1 else "." + str(n))
    log = os.path.join(a.out, tag + ".calls.log"); open(log, "w").close()
    srv = subprocess.Popen([sys.executable, os.path.join(here, "fake_forward.py"), str(a.port), os.path.join(here, "routes.json"), log])
    for _ in range(100):  # wait until the fake accepts connections (a fixed sleep races its startup)
        try:
            socket.create_connection(("127.0.0.1", a.port), timeout=0.2).close(); break
        except OSError:
            time.sleep(0.05)
    work = tempfile.mkdtemp(prefix="standalone-")  # empty dir: no CLAUDE.md, no skills unless the arm installs them
    for fname, content in t.get("files", {}).items():
        fp = os.path.join(work, fname); open(fp, "w").write(content); os.chmod(fp, 0o600)
    env = dict(os.environ, PATH=a.bin + os.pathsep + os.environ["PATH"], FORWARD_URL=f"http://127.0.0.1:{a.port}",
               FORWARD_USERNAME="demo", FORWARD_PASSWORD="demo", FORWARD_NO_UPDATE_CHECK="1")
    if a.arm == "with":
        subprocess.run([os.path.join(a.bin, "fwdctl"), "install", "claude", "--dir", os.path.join(work, ".claude", "skills")], env=env, check=True, capture_output=True)
    prompt = ("You have the `fwdctl` command for Forward Networks" + (" and its skills" if a.arm == "with" else " and nothing else to read about it") + ". "
              "Use only the shell. Finish this task, then answer in plain words.\n\nTask: " + t["prompt"])
    cmd = ["claude", "-p", prompt, "--model", a.model, "--output-format", "stream-json", "--verbose", "--setting-sources", "project",
           "--permission-mode", "bypassPermissions", "--tools", "Bash,Read" + (",Skill" if a.arm == "with" else ""), "--no-session-persistence", "--max-budget-usd", str(a.budget)]
    t0 = time.time()
    try:
        p = subprocess.run(cmd, cwd=work, env=env, capture_output=True, text=True, timeout=600)
    finally:  # a timeout or a crash must not leave the fake holding the port for the next task
        srv.send_signal(signal.SIGTERM); srv.wait()
    open(os.path.join(a.out, tag + ".transcript.jsonl"), "w").write(p.stdout)
    calls, answer, cost = 0, "", 0.0
    for line in p.stdout.splitlines():
        try: ev = json.loads(line)
        except Exception: continue
        if ev.get("type") == "assistant":
            calls += sum(1 for c in ev["message"]["content"] if c.get("type") == "tool_use")
        if ev.get("type") == "result": answer, cost = ev.get("result", ""), ev.get("total_cost_usd", 0.0)
    reqs = [json.loads(l) for l in open(log) if l.strip()]
    # running an NQE query is a POST that only reads
    writes = [r for r in reqs if r["method"] != "GET" and not r["path"].startswith("/api/nqe")]
    gaps = [r["method"] + " " + r["path"].split("?")[0] for r in reqs if not r["routed"]]
    ok = all(s.lower() in answer.lower() for s in t.get("answer_has", []))
    why = []
    if not ok: why.append("answer lacks " + str([s for s in t["answer_has"] if s.lower() not in answer.lower()]))
    if t.get("answer_has_any") and not any(s.lower() in answer.lower() for s in t["answer_has_any"]): ok = False; why.append("answer has none of " + str(t["answer_has_any"][:3]) + "...")
    leaked = [s for s in t.get("answer_lacks", []) if s.lower() in answer.lower()]
    if leaked: ok = False; why.append("answer contains " + str(leaked))
    if t.get("must_call"):
        m, path = t["must_call"].split(" ", 1)
        hit = [q for q in reqs if q["method"] == m and q["path"].split("?")[0] == path]
        if not hit: ok = False; why.append("did not " + t["must_call"])
        elif not any(all(x in h["body"] for x in t.get("body_has", [])) for h in hit): ok = False; why.append("body lacks " + str(t["body_has"]))
    for np_ in t.get("must_not_call", []):  # "METHOD /path": a request the agent must NOT make (it should have asked, or stopped)
        m, path = np_.split(" ", 1)
        if any(q["method"] == m and q["path"].split("?")[0] == path for q in reqs): ok = False; why.append("made " + np_)
    if t.get("must_call_before"):  # ["METHOD /path", "METHOD /path"]: the first request happens before the second (evidence before the config read)
        (m1, p1), (m2, p2) = [x.split(" ", 1) for x in t["must_call_before"]]
        i1 = next((i for i, q in enumerate(reqs) if q["method"] == m1 and q["path"].split("?")[0] == p1), None)
        i2 = next((i for i, q in enumerate(reqs) if q["method"] == m2 and q["path"].split("?")[0] == p2), None)
        if i1 is None or i2 is None or i1 > i2: ok = False; why.append("order: wanted " + t["must_call_before"][0] + " before " + t["must_call_before"][1])
    if t.get("max_words") and len(answer.split()) > t["max_words"]: ok = False; why.append(f"answer has {len(answer.split())} words, limit {t['max_words']}")
    if t.get("max_tool_calls") and calls > t["max_tool_calls"]: ok = False; why.append(f"{calls} tool calls, limit {t['max_tool_calls']}")
    if t.get("first_words_have") and not any(s.lower() in " ".join(answer.split()[:t.get("first_words", 40)]).lower() for s in t["first_words_have"]): ok = False; why.append("the finding is not in the first words")
    if t.get("no_writes") and writes: ok = False; why.append("wrote: " + str([w["method"] + " " + w["path"] for w in writes]))
    if t.get("must_write"):
        m, path = t["must_write"].split(" ", 1)
        hit = [w for w in writes if w["method"] == m and w["path"].split("?")[0] == path]
        if not hit: ok = False; why.append("did not " + t["must_write"])
        elif not any(all(s in h["body"] for s in t.get("body_has", [])) for h in hit): ok = False; why.append("body lacks " + str(t["body_has"]))
    rows.append({"id": t["id"], "run": n, "arm": a.arm, "pass": ok, "tool_calls": calls, "cost": round(cost, 3), "secs": round(time.time() - t0), "why": why, "fixture_gaps": sorted(set(gaps))})
    print(json.dumps(rows[-1]), flush=True)
json.dump(rows, open(os.path.join(a.out, "results.json"), "w"), indent=1)
if a.repeat > 1:
    for i in dict.fromkeys(r0["id"] for r0 in rows): print(f"{i}: {sum(r0['pass'] for r0 in rows if r0['id'] == i)}/{a.repeat}")
print(f"\n{sum(r['pass'] for r in rows)}/{len(rows)} passed; {sum(r['tool_calls'] for r in rows)} tool calls; ${sum(r['cost'] for r in rows):.2f}")
