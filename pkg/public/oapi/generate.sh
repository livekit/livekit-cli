#!/bin/sh
# Regenerates oapi.gen.go from the LiveKit Public API OpenAPI spec.
#
# The spec is fetched directly from its published URL by oapi-codegen (which
# loads http(s) URLs natively) — no local copy of the spec is kept in the repo.
# Defaults to production; override for a local/staging server. The directive is
# gated behind the `oapigen` build tag, so regenerate deliberately with:
#
#   go generate -tags oapigen ./pkg/public/...
#   LK_OPENAPI_SPEC_URL=http://localhost:8080/openapi.yaml go generate -tags oapigen ./pkg/public/...
#
# Invoked by the //go:generate directive in generate.go (runs in this directory,
# alongside cfg.yaml).
set -eu

SPEC_URL="${LK_OPENAPI_SPEC_URL:-https://api.livekit.io/openapi.yaml}"
echo "oapi-codegen: generating client from ${SPEC_URL}"
exec go tool oapi-codegen -config cfg.yaml "${SPEC_URL}"
