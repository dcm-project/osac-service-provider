#!/usr/bin/env bash
# TC-TB-040 / REQ-TB-050: fail if any Tier B manifest, values file, or
# workflow references a floating OSAC image/chart tag (`main`/`latest`)
# instead of a pinned `vX.Y.Z`. Mirrors upstream's own
# check-floating-tags.yaml guard on our side of the dependency (spec §3).
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")"

# Matches e.g. "ghcr.io/osac-project/charts/fulfillment-service" or
# "ghcr.io/osac/fulfillment-service" followed by a floating tag/version ref.
pattern='(ghcr\.io/osac(-project)?/[^[:space:]"'"'"']+)[:@](main|latest)\b'

violations=0
while IFS= read -r -d '' file; do
	if grep -EnH "$pattern" "$file"; then
		violations=1
	fi
done < <(find . ../../.github/workflows -type f \( -name '*.yaml' -o -name '*.yml' \) -print0 2>/dev/null)

if [ "$violations" -ne 0 ]; then
	echo "ERROR: found a floating (main/latest) OSAC image or chart tag above — pin to a real vX.Y.Z release (REQ-TB-050)." >&2
	exit 1
fi

echo "OK: no floating OSAC image/chart tags found."
