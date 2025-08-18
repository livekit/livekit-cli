# syntax=docker/dockerfile:1
# For detailed documentation and guides, see:
# https://github.com/livekit/livekit-cli/blob/main/pkg/agentfs/examples/README.md
# For more help: https://docs.livekit.io/agents/
# For help with building and deployment: https://docs.livekit.io/agents/ops/deployment/cloud/build

ARG BUN_VERSION=1
FROM oven/bun:${BUN_VERSION} AS base

# Define the program entrypoint file where your agent is started
ARG PROGRAM_MAIN="{{.ProgramMain}}"

WORKDIR /app

# === BUILD STAGE ===
FROM base AS build

# Copy package files first for layer caching
COPY package.json bun.lock* ./

# Install all dependencies
RUN bun install --frozen-lockfile

ENV NODE_ENV=production

# Copy source code
COPY . .

# Build if needed (Bun can run TypeScript directly)
RUN bun run build

# Reinstall production only
RUN bun install --production

# === RUNTIME STAGE ===
FROM base

# Create non-privileged user
ARG UID=10001
RUN adduser \
    --disabled-password \
    --gecos "" \
    --home "/app" \
    --shell "/sbin/nologin" \
    --uid "${UID}" \
    appuser

# Copy built application from build stage
COPY --from=build /app /app

# Set ownership and switch user
RUN chown -R appuser:appuser /app
USER appuser

# Bun can run TypeScript directly
CMD [ "bun", "run", "{{.ProgramMain}}", "start" ]