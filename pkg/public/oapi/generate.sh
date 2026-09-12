#!/bin/sh
# Regenerates oapi.gen.go from the LiveKit Public API OpenAPI spec.
#
# The spec is served by the API itself at <base>/v1/openapi. The base defaults
# to production; override it for a local/staging server with LK_PUBLIC_API_URL.
# The directive is gated behind the `oapigen` build tag, so regenerate
# deliberately with:
#
#   go generate -tags oapigen ./pkg/public/...
#   LK_PUBLIC_API_URL=http://localhost:8000 go generate -tags oapigen ./pkg/public/...
#
# Invoked by the //go:generate directive in generate.go (runs in this directory,
# alongside cfg.yaml).
set -eu

BASE_URL="${LK_PUBLIC_API_URL:-https://beta-api.livekit.cloud}"
SPEC_URL="${BASE_URL}/v1/openapi"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

echo "oapi-codegen: fetching spec from ${SPEC_URL}"
curl -fsSL "${SPEC_URL}" -o "${TMP}/openapi.yaml"

# The spec is OpenAPI 3.1 emitted by grpc-gateway; its union `type` arrays
# (nullable [X,"null"] and 64-bit ints [integer,string]) can't be consumed by
# Go generators, so collapse them to 3.0-style single types + `nullable` first.
go run normalize.go "${TMP}/openapi.yaml" "${TMP}/normalized.yaml"

go tool oapi-codegen -config cfg.yaml "${TMP}/normalized.yaml"
