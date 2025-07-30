# syntax=docker/dockerfile:1
# DEV MODE Dockerfile for a LiveKit agent using pip
ARG PYTHON_VERSION=3.11.6
FROM python:${PYTHON_VERSION}-slim

# --- Environment Configuration ---
ENV PYTHONUNBUFFERED=1
ENV AGENT_WORKDIR=/home/appuser
# Token MUST be set at runtime: e.g., -e DEV_SYNC_TOKEN="your-secret"
ENV DEV_SYNC_TOKEN=""

# --- Install Dev Mode System Dependencies ---
# Install curl, Node.js (for nodemon), and cloudflared.
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
    curl \
    ca-certificates \
    gnupg \
    && mkdir -p /etc/apt/keyrings \
    && curl -fsSL https://deb.nodesource.com/gpgkey/nodesource-repo.gpg.key | gpg --dearmor -o /etc/apt/keyrings/nodesource.gpg \
    && NODE_MAJOR=20 \
    && echo "deb [signed-by=/etc/apt/keyrings/nodesource.gpg] https://deb.nodesource.com/node_$NODE_MAJOR.x nodistro main" | tee /etc/apt/sources.list.d/nodesource.list \
    && apt-get update \
    && apt-get install -y nodejs \
    && npm install -g nodemon \
    && ARCH=$(dpkg --print-architecture) \
    && echo "Detected architecture: $ARCH" \
    && curl -L --output cloudflared.deb https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-${ARCH}.deb \
    && dpkg -i cloudflared.deb && rm cloudflared.deb \
    && rm -rf /var/lib/apt/lists/*

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

# --- Setup Dev Tools ---
# Create an isolated directory for our dev tools and copy them in.
# The entrypoint script is placed in /usr/local/bin to be in the system's PATH.
RUN mkdir -p /opt/livekit-dev-tools
COPY dev-tools/sync_server.py /opt/livekit-dev-tools/
COPY dev-tools/live-dev-entrypoint.sh /usr/local/bin/
RUN chmod +x /usr/local/bin/live-dev-entrypoint.sh

# --- Setup Agent Application ---
WORKDIR ${AGENT_WORKDIR}

# Copy the dependency file first to leverage Docker's layer caching
COPY requirements.txt .

# Install Python dependencies
RUN python -m pip install --no-cache-dir -r requirements.txt

# Copy the rest of the application source code
COPY . .

# Change ownership of all application and tool files to the new user
RUN chown -R appuser:appuser ${AGENT_WORKDIR} && chown -R appuser:appuser /opt/livekit-dev-tools

# Switch to the non-privileged user for runtime
USER appuser

# --- Runtime Execution ---
# The entrypoint script starts all dev services and then runs the CMD.
ENTRYPOINT ["/usr/local/bin/live-dev-entrypoint.sh"]

# The original CMD is passed as arguments ("$@") to the entrypoint.
# This allows developers to use their standard start command.
CMD ["python", "script.py", "start"]