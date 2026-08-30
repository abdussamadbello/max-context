#!/usr/bin/env bash
# Fetch and extract the LoCoBench dataset into ./data.
#
#   ./fetch-data.sh
#
# ~250MB download, ~790MB on disk, 8,000 scenarios across 1,000 projects.
# data/ is gitignored. Idempotent: an already-populated data/ is left alone
# unless --force is passed.
set -euo pipefail

DRIVE_ID="1pK1M1sRrVZUDMKYcwh49CdXug0UzStvl"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ZIP="${TMPDIR:-/tmp}/locobench.zip"

cd "$ROOT"

if [ "${1:-}" != "--force" ] && [ -d data/output/scenarios ]; then
	echo "data/ already populated ($(ls data/output/scenarios/*.json 2>/dev/null | wc -l) scenarios). Pass --force to refetch."
	exit 0
fi

if ! command -v gdown >/dev/null 2>&1; then
	echo "installing gdown…"
	python3 -m pip install --quiet --disable-pip-version-check gdown
fi
if ! command -v unzip >/dev/null 2>&1; then
	echo "error: unzip is required" >&2
	exit 1
fi

if [ ! -s "$ZIP" ]; then
	echo "downloading (~250MB)…"
	gdown "$DRIVE_ID" -O "$ZIP"
fi

# The archive already carries data/generated and data/output at its top level,
# so it extracts straight into place — no flattening step. __MACOSX/ is the
# authoring machine's resource forks and is excluded: it roughly doubles the
# file count and holds nothing the harness reads.
echo "extracting 181k files…"
unzip -q -o "$ZIP" -x '__MACOSX/*' -d "$ROOT"

scenarios=$(ls data/output/scenarios/*.json 2>/dev/null | wc -l)
projects=$(ls data/generated 2>/dev/null | wc -l)
echo "done: $scenarios scenarios, $projects projects, $(du -sh data | cut -f1)"
if [ "$scenarios" -eq 0 ] || [ "$projects" -eq 0 ]; then
	echo "error: extraction produced no scenarios or projects" >&2
	exit 1
fi
