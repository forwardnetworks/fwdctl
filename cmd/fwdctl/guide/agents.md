# For an AI agent: how to use the skills

Paste this into an agent's instructions (or let `fwdctl install agents` write it into AGENTS.md / CLAUDE.md).

Forward Networks skills for network questions. Most read Forward's digital twin and return evidence; the `edit-*` skills change Forward's own data (never a device).
Run one with `echo '<json inputs>' | fwdctl run <skill>` (needs `FORWARD_URL`, `FORWARD_USERNAME`, `FORWARD_PASSWORD`). List them with `fwdctl list`, read a skill's procedure with `fwdctl describe <skill>`, its inputs with `fwdctl run <skill> --help`.

**How to work**

0. **Not sure which skill?** `fwdctl which "<question>"` ranks the router table offline and names the skill or playbook.
1. **Start with a playbook when the question is a task.** `fwdctl describe plan-investigation` maps the question to a playbook (troubleshooting, security, change, audit, health) or to one skill. Follow the playbook's order and stop when the evidence answers the question.
2. **Read the result, not just the answer.** `status` is ok, failed, unknown or error: **unknown is never a pass**. Read `limits` (what was not measured), `omitted` (lists that were cut: say when you compare answers) and `context` (which snapshot and when) before you rely on it, and say them. `next_actions` names the skill that usually follows.
3. **Never improvise a write.** An `edit-*` skill is a dry run until its input has `apply: true`. Show the exact change and its undo, and apply only after approval. `fwdctl describe plan-safe-write` has the protocol.
4. **Recommend, never reconfigure.** No skill pushes config to a device. Say what to change; never claim you changed it.
5. **Exit codes:** 0 ok, 1 failed, 2 unknown, 3 error, 64 bad usage.
6. **Keep the context small.** After each result write one line (what it showed, the snapshot, what is still unknown) and carry that forward instead of the raw evidence; ask for the page or the filter you need (`limit`, `view`, a device) rather than everything. For a long investigation keep a short running findings list, and where you can delegate, give each independent branch (one device, one flow, one CVE) to a sub-agent and ask it to return only its conclusion with the snapshot and the evidence it rests on.
