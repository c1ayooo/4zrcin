#!/usr/bin/env python3
"""Report consecutive commander rounds that failed before reaching the model.

Why the ledger and not the journal: failCommanderRun (internal/api/commander.go:182)
covers three paths, and one of them is "cannot open the commander log"
(commander.go:348). A journal event cannot record the failure of the run whose
journal could not be opened; only the ledger row survives it. So this check
reads ledger.sqlite objects(kind='run') and needs nothing else running.

Trip condition (deliberately narrow):
  the newest commander runs, scanned from the tail, contain at least
  --threshold consecutive rows where status is not 'done' AND the exit reason
  carries the machine-readable token `prompt_anchor_missing`.

Widening it to "N failures in a row" would be useless here: the project already
carries 17 failed runs out of 497, so plain failure counting fires on healthy
operation. The token is what makes the rule discriminate: the three layout
failures share the prefix "指挥官轮布局：", so matching that prefix alone would
conflate a missing anchor with every other layout error.

Exit codes: 0 clean, 1 tripped, 2 cannot read the ledger (UNKNOWN is not pass),
3 self-test failed.
"""

from __future__ import annotations

import argparse
import json
import os
import sqlite3
import sys
import tempfile

DEFAULT_ANCHOR_HINTS = ("prompt_anchor_missing",)

SCHEMA = (
    "CREATE TABLE objects (kind TEXT NOT NULL, id TEXT NOT NULL, position INTEGER NOT NULL,"
    " version INTEGER NOT NULL, seq INTEGER NOT NULL, payload BLOB NOT NULL,"
    " PRIMARY KEY(kind,id))"
)


def load_runs(ledger: str) -> list[dict]:
    if not os.path.isfile(ledger):
        raise SystemExit(_fail(f"ledger not found: {ledger}"))
    con = sqlite3.connect(f"file:{ledger}?mode=ro", uri=True)
    try:
        rows = con.execute("SELECT id, payload FROM objects WHERE kind='run'").fetchall()
    except sqlite3.Error as exc:
        raise SystemExit(_fail(f"cannot read objects(kind='run') from {ledger}: {exc}"))
    finally:
        con.close()
    runs = []
    for rid, blob in rows:
        try:
            run = json.loads(blob)
        except (TypeError, ValueError):
            continue
        if not isinstance(run, dict):
            continue
        if run.get("purpose") != "commander" and not run.get("commander_round"):
            continue
        run.setdefault("id", rid)
        runs.append(run)
    return runs


def _order_key(run: dict) -> tuple:
    round_no = run.get("commander_round") or 0
    return (int(round_no), str(run.get("started_at") or ""))


def scan(runs: list[dict], threshold: int, hints: tuple[str, ...], window: int) -> dict:
    ordered = sorted(runs, key=_order_key)[-window:]
    tail = []
    for run in reversed(ordered):
        status = str(run.get("status") or "")
        reason = str(run.get("exit_reason") or run.get("stop_reason") or "")
        if status == "done":
            break
        lowered = reason.lower()
        if not any(h.lower() in lowered for h in hints):
            break
        tail.append({"id": run.get("id"), "round": run.get("commander_round"),
                     "status": status, "exit_reason": reason,
                     "started_at": run.get("started_at")})
    return {
        "commander_runs": len(ordered),
        "threshold": threshold,
        "trailing": tail,
        "tripped": len(tail) >= threshold,
        "last_status": (ordered[-1].get("status") if ordered else None),
        "last_reason": (ordered[-1].get("exit_reason") or ordered[-1].get("stop_reason")) if ordered else None,
    }


def _fail(msg: str) -> int:
    print(f"UNKNOWN: {msg}", file=sys.stderr)
    return 2


def report(result: dict) -> int:
    if result["tripped"]:
        print(f"TRIPPED: {len(result['trailing'])} consecutive commander rounds failed on an anchor error")
        for item in reversed(result["trailing"]):
            print(f"  round {item['round']} {item['id']} status={item['status']} exit_reason={item['exit_reason']}")
        return 1
    trailing = len(result["trailing"])
    print(f"ok: {result['commander_runs']} commander runs scanned, "
          f"{trailing} trailing anchor failure(s) < threshold {result['threshold']}")
    print(f"ok: last commander run status={result['last_status']} reason={result['last_reason']}")
    return 0


def self_test(threshold: int, hints: tuple[str, ...]) -> int:
    """Negative samples first: each case must produce the stated verdict."""
    cases = [
        ("three anchor failures in a row -> trip", [
            ("failed", "指挥官轮布局：prompt_anchor_missing: ## board"),
            ("failed", "指挥官轮布局：prompt_anchor_missing: ## 授权边界（当前口径）"),
            ("failed", "指挥官轮布局：prompt_anchor_missing: ## board"),
        ], True),
        ("three layout failures without the marker in a row -> no trip", [
            ("failed", "指挥官轮布局：详情段为空"),
            ("failed", "指挥官轮布局：本轮指派段为空"),
            ("failed", "指挥官轮布局：可续读入口段为空"),
        ], False),
        ("three plain failures in a row -> no trip", [
            ("failed", "指挥官笔记身份：token 已失效"),
            ("failed", "打开指挥官日志失败：permission denied"),
            ("failed", "准备指挥官日志目录失败：disk full"),
        ], False),
        ("two anchor failures -> no trip", [
            ("failed", "指挥官轮布局：prompt_anchor_missing: ## board"),
            ("failed", "指挥官轮布局：prompt_anchor_missing: ## board"),
        ], False),
        ("anchor failures broken by a done round -> no trip", [
            ("failed", "指挥官轮布局：prompt_anchor_missing: ## board"),
            ("done", "completed"),
            ("failed", "指挥官轮布局：prompt_anchor_missing: ## board"),
            ("failed", "指挥官轮布局：prompt_anchor_missing: ## board"),
        ], False),
        ("marker text on a done round -> no trip", [
            ("done", "completed，prompt_anchor_missing: ## board"),
        ], False),
    ]
    failures = []
    for label, rows, want in cases:
        with tempfile.TemporaryDirectory() as tmp:
            path = os.path.join(tmp, "ledger.sqlite")
            con = sqlite3.connect(path)
            con.execute(SCHEMA)
            for i, (status, reason) in enumerate(rows, start=1):
                payload = {"id": f"r_case_{i}", "purpose": "commander", "commander_round": i,
                           "status": status, "exit_reason": reason,
                           "started_at": f"2026-11-22T00:0{i}:00+08:00"}
                con.execute("INSERT INTO objects(kind,id,position,version,seq,payload) VALUES(?,?,?,?,?,?)",
                            ("run", payload["id"], i, 1, i, json.dumps(payload).encode()))
            con.commit()
            con.close()
            got = scan(load_runs(path), threshold, hints, window=50)["tripped"]
        verdict = "ok" if got == want else "WRONG"
        print(f"  self-test [{verdict}] {label}: tripped={got} want={want}")
        if got != want:
            failures.append(label)
    if failures:
        print(f"SELF-TEST FAILED: {len(failures)}/{len(cases)} cases wrong", file=sys.stderr)
        return 3
    print(f"  self-test: {len(cases)}/{len(cases)} cases as expected")
    return 0


def main(argv: list[str]) -> int:
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--ledger", default=os.path.join("data", "projects", "p-015c82af", "ledger.sqlite"))
    ap.add_argument("--threshold", type=int, default=3)
    ap.add_argument("--window", type=int, default=50)
    ap.add_argument("--anchor-hint", action="append", dest="hints",
                    help=f"substring that marks an anchor failure (default: {', '.join(DEFAULT_ANCHOR_HINTS)})")
    ap.add_argument("--json", action="store_true", help="print the scan result as JSON")
    ap.add_argument("--self-test", action="store_true", help="run the negative-sample cases and exit")
    args = ap.parse_args(argv)

    hints = tuple(args.hints) if args.hints else DEFAULT_ANCHOR_HINTS
    if args.self_test:
        return self_test(args.threshold, hints)
    result = scan(load_runs(args.ledger), args.threshold, hints, args.window)
    if args.json:
        print(json.dumps(result, ensure_ascii=False, indent=2))
        return 1 if result["tripped"] else 0
    return report(result)


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
