#!/usr/bin/env bash
set -euo pipefail

# Usage:
#   UCENTRAL_SCHEMA_REF=<tag-or-commit> ./scripts/fetch-ucentral-schema.sh
#
# Development may use a branch ref, but CI/release should use a tag or commit SHA.

UCENTRAL_SCHEMA_REF="${UCENTRAL_SCHEMA_REF:-REPLACE_WITH_PINNED_TAG_OR_COMMIT}"
BASE_URL="https://raw.githubusercontent.com/Telecominfraproject/olg-ucentral-schema/${UCENTRAL_SCHEMA_REF}"

mkdir -p schemas/ucentral

curl -fsSL "${BASE_URL}/schema.json" -o schemas/ucentral/schema.json
curl -fsSL "${BASE_URL}/ucentral.schema.full.json" -o schemas/ucentral/ucentral.schema.full.json

sha256sum schemas/ucentral/schema.json
sha256sum schemas/ucentral/ucentral.schema.full.json
