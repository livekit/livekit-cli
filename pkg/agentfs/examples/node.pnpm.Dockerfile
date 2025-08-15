# syntax=docker/dockerfile:1
# For detailed documentation and guides, see:
# https://github.com/livekit/livekit-cli/blob/main/pkg/agentfs/examples/README.md
# For more help: https://docs.livekit.io/agents/
# For help with building and deployment: https://docs.livekit.io/agents/ops/deployment/cloud/build

ARG NODE_VERSION=22
FROM node:${NODE_VERSION}-slim AS base

# Define the program entrypoint file where your agent is started
ARG PROGRAM_MAIN="{{.ProgramMain}}"

WORKDIR /app

# Install pnpm globally
RUN npm install -g pnpm@9.15.9

ENV PNPM_HOME="/pnpm"
ENV PATH="$PNPM_HOME:$PATH"

# === BUILD STAGE ===
FROM base AS build

# Install CA certificates for HTTPS connections
RUN apt-get update -qq && apt-get install --no-install-recommends -y ca-certificates

# Copy all files (pnpm needs the full context)
COPY --link . .

# Install dependencies with frozen lockfile
RUN pnpm install --frozen-lockfile

# Build TypeScript to JavaScript
RUN pnpm run build

# Remove dev dependencies
RUN pnpm prune --prod

# === RUNTIME STAGE ===
FROM base

ENV NODE_ENV=production

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

# Copy SSL certificates for HTTPS
COPY --from=build /etc/ssl/certs /etc/ssl/certs

# Set ownership and switch user
RUN chown -R appuser:appuser /app
USER appuser

# Start the agent
CMD [ "node", "{{.ProgramMain}}", "start" ]