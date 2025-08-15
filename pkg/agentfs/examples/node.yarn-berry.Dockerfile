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

# Enable Corepack for Yarn Berry
RUN corepack enable

# === BUILD STAGE ===
FROM base AS build

# Install CA certificates for HTTPS connections
RUN apt-get update -qq && apt-get install --no-install-recommends -y ca-certificates

# Copy Yarn configuration
COPY .yarnrc.yml* ./

# Remove local yarnPath to use corepack version
RUN if [ -f .yarnrc.yml ]; then \
      grep -v "yarnPath:" .yarnrc.yml > .yarnrc.yml.tmp && mv .yarnrc.yml.tmp .yarnrc.yml; \
    fi

# Activate Yarn version
RUN corepack prepare yarn@stable --activate

# Copy package files first for layer caching
COPY package.json yarn.lock ./

# Install dependencies
RUN yarn install --immutable

# Copy source code
COPY . .

# Restore clean .yarnrc.yml
RUN if [ -f .yarnrc.yml ]; then \
      grep -v "yarnPath:" .yarnrc.yml > .yarnrc.yml.tmp && mv .yarnrc.yml.tmp .yarnrc.yml; \
    fi

# Build TypeScript to JavaScript
RUN yarn run build

# Remove dev dependencies
RUN yarn workspaces focus --production

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