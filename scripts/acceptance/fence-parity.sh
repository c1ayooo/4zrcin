#!/usr/bin/env bash
# The fence parity gate (4zrcin docs/19 P1-4 acceptance): run the acceptance
# corpus through the full admission path at a P0-era engine commit and at the
# working tree, then diff the VERDICT tables. A refactor that changes
# interception behavior fails here.
#
# Usage: scripts/acceptance/fence-parity.sh [BASE_REF]
#   BASE_REF defaults to the P0/P1-0 engine commit (c0b54f5e).
set -euo pipefail

BASE_REF="${1:-c0b54f5e}"
ENGINE="${ENGINE:-$HOME/cyber-harness}"
HERE="$(cd "$(dirname "$0")" && pwd)"
CUR_PKG="$ENGINE/exts/4zfence"
WORK="$(mktemp -d)"
trap 'git -C "$ENGINE" worktree remove --force "$WORK" 2>/dev/null || rm -rf "$WORK"' EXIT

echo "== baseline worktree at $BASE_REF"
git -C "$ENGINE" worktree add -q "$WORK" "$BASE_REF"
BASE_PKG="$WORK/exts/guardrail"
cp "$HERE/acceptance_baseline_test.go" "$BASE_PKG/acceptance_parity_test.go"
mkdir -p "$BASE_PKG/testdata"
cp "$CUR_PKG/testdata/acceptance_samples.json" "$BASE_PKG/testdata/"
(cd "$BASE_PKG" && go test -tags fenceparity . -run TestAcceptanceCorpusBaseline -v 2>&1 | grep '^ *acceptance_parity_test.go' | sed 's/^ *acceptance_parity_test.go:[0-9]*: //' | sort) > "$WORK/base.table"

echo "== current tree"
(cd "$CUR_PKG" && go test -tags fenceparity . -run TestAcceptanceCorpus -v 2>&1 | grep '^ *acceptance_test.go' | sed 's/^ *acceptance_test.go:[0-9]*: //' | sort) > "$WORK/cur.table"

echo "== parity diff (empty = identical interception behavior)"
if diff -u "$WORK/base.table" "$WORK/cur.table"; then
  echo "PASS: $(wc -l < "$WORK/cur.table") verdicts identical"
else
  echo "FAIL: interception behavior changed since $BASE_REF" >&2
  exit 1
fi
