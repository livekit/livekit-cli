# This is an example Dockerfile that builds a minimal container for running LK Agents
# For more information on the build process, see https://docs.livekit.io/agents/ops/deployment/builds/
# syntax=docker/dockerfile:1

ARG NODE_VERSION=22
FROM node:${NODE_VERSION}-slim AS base

ENV PNPM_HOME="/pnpm"
ENV PATH="$PNPM_HOME:$PATH"

# Install ca-certificates for SSL support
RUN apt-get update -qq && apt-get install --no-install-recommends -y ca-certificates
RUN npm install -g pnpm@9.15.9

WORKDIR /app

FROM base AS build

COPY package.json pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile

COPY . .
RUN pnpm run build
RUN pnpm prune --prod

# Runtime stage
FROM base AS runtime

# Create unprivileged user for runtime
ARG UID=10001
RUN adduser \
    --disabled-password \
    --gecos "" \
    --home "/app" \
    --shell "/sbin/nologin" \
    --uid "${UID}" \
    appuser

COPY --from=build /app /app
COPY --from=build /etc/ssl/certs /etc/ssl/certs

# Ensure ownership and drop privileges
RUN chown -R appuser:appuser /app
USER appuser

ENV NODE_ENV=production
CMD [ "node", "./dist/agent.js", "start" ]
