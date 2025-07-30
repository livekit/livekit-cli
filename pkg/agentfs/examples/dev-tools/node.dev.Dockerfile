# syntax=docker/dockerfile:1
# DEV MODE Dockerfile for a LiveKit agent (Node.js)
FROM node:20-slim

# --- Environment Configuration ---
ENV AGENT_WORKDIR=/app
# Development sync token (will be replaced with generated UUID)
ENV DEV_SYNC_TOKEN=""

# --- Install Dev Mode System Dependencies ---
# Install curl and cloudflared. nodemon is already part of the Node ecosystem.
RUN apt-get update && \
    apt-get install -y --no-install-recommends curl \
    && npm install -g pnpm@9.7.0 nodemon \
    && ARCH=$(dpkg --print-architecture) \
    && echo "Detected architecture: $ARCH" \
    && curl -L --output cloudflared.deb https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-${ARCH}.deb \
    && dpkg -i cloudflared.deb && rm cloudflared.deb \
    && rm -rf /var/lib/apt/lists/*

# --- Setup Dev Tools ---
# Create an isolated directory for our dev tools and copy them in.
# The entrypoint script is placed in /usr/local/bin to be in the system's PATH.
RUN mkdir -p /opt/livekit-dev-tools
COPY dev-tools/sync_server.js /opt/livekit-dev-tools/
COPY dev-tools/live-dev-entrypoint.sh /usr/local/bin/
# Install dependencies for the Node.js sync server
RUN cd /opt/livekit-dev-tools && npm install yargs tar && chmod +x /usr/local/bin/live-dev-entrypoint.sh

# --- Security and Permissions ---
# Create a non-privileged user to run the application
ARG UID=10001
RUN adduser \
    --disabled-password \
    --gecos "" \
    --home "/home/appuser" \
    --shell "/bin/bash" \
    --uid "${UID}" \
    appuser

# --- Setup Agent Application ---
WORKDIR ${AGENT_WORKDIR}

# Copy the agent files
COPY . .

# Install agent dependencies and build as root for better compatibility
RUN pnpm install --frozen-lockfile
RUN pnpm run build

# Change ownership of all application and tool files to the new user
RUN chown -R appuser:appuser ${AGENT_WORKDIR} && chown -R appuser:appuser /opt/livekit-dev-tools

# Switch to the non-privileged user for runtime
USER appuser

# Download any required files/models at build time
# RUN node ./dist/agent.js download-files || echo "No download-files command available"

# expose healthcheck port
EXPOSE 8081

# --- Runtime Execution ---
# The entrypoint script starts all dev services and then runs the CMD.
ENTRYPOINT ["/usr/local/bin/live-dev-entrypoint.sh"]

# The original CMD is passed as arguments ("$@") to the entrypoint.
# This allows developers to use their standard start command.
CMD ["node", "./dist/agent.js", "start"]