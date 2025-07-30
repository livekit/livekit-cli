#!/bin/bash
# Exit immediately if a command exits with a non-zero status.
set -e

# --- Configuration & Validation ---
# These must be passed into the container at runtime.
# AGENT_WORKDIR: The agent's source code directory, which nodemon will watch.
# DEV_SYNC_TOKEN: The secret token for authorizing file pushes.

echo "Checking environment variables..."
echo "AGENT_WORKDIR: ${AGENT_WORKDIR:-<not set>}"
echo "DEV_SYNC_TOKEN: ${DEV_SYNC_TOKEN:+<set>}"

if [ -z "$AGENT_WORKDIR" ]; then
  echo "FATAL: AGENT_WORKDIR environment variable must be set."
  exit 1
fi

# DEV_SYNC_TOKEN is now hardcoded in the Dockerfile during build
if [ -z "$DEV_SYNC_TOKEN" ]; then
  echo "FATAL: DEV_SYNC_TOKEN is not set. This should not happen with dev mode Docker build."
  exit 1
fi

TOOLS_DIR="/opt/livekit-dev-tools"
echo "--- Starting LiveKit Dev Mode Services ---"

# --- 1. Start the appropriate Sync Server in the background ---
if [ -f "$TOOLS_DIR/sync_server.js" ]; then
    echo "Starting Node.js sync server..."
    node $TOOLS_DIR/sync_server.js --token "$DEV_SYNC_TOKEN" --workdir "$AGENT_WORKDIR" &
elif [ -f "$TOOLS_DIR/sync_server.py" ]; then
    echo "Starting Python sync server..."
    python3 $TOOLS_DIR/sync_server.py --token "$DEV_SYNC_TOKEN" --workdir "$AGENT_WORKDIR" &
else
    echo "FATAL: No sync server script found in $TOOLS_DIR"
    exit 1
fi

# Wait a moment for the server to bind to its port
sleep 2

# --- 2. Start the Cloudflared tunnel in the background ---
echo "Starting cloudflared tunnel to http://localhost:8080 ..."
# The tunnel output (including the public URL) will go to the container logs
cloudflared tunnel --url http://localhost:8080 &

# Wait a moment for cloudflared to initialize
sleep 3

# --- 3. Start the Agent with nodemon ---
echo "--- Dev Mode Ready. Starting agent with nodemon... ---"
echo "Watching for file changes in: $AGENT_WORKDIR"
echo "---------------------------------------------------------"

# Use 'exec' to replace this shell process with the nodemon process.
# This ensures that nodemon receives signals (like SIGTERM from 'docker stop') correctly.
# "$@" is a special variable that expands to all arguments passed to this script,
# which will be the contents of the Dockerfile's CMD instruction.
exec nodemon --watch "${AGENT_WORKDIR:-/home/appuser}" --ext "py,js,ts,json" --exec "$@"