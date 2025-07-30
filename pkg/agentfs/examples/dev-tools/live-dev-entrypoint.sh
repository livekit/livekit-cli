#!/bin/bash
# Exit immediately if a command exits with a non-zero status.
set -e

# Enable debugging to see each command as it's executed
set -x

echo "=== LiveKit Dev Mode Entrypoint Starting ==="
echo "Current user: $(whoami)"
echo "Current directory: $(pwd)"
echo "Home directory: $HOME"
echo "PATH: $PATH"

# --- Configuration & Validation ---
# These must be passed into the container at runtime.
# AGENT_WORKDIR: The agent's source code directory, which nodemon will watch.
# DEV_SYNC_TOKEN: The secret token for authorizing file pushes.

echo "=== Checking environment variables ==="
echo "AGENT_WORKDIR: ${AGENT_WORKDIR:-<not set>}"
echo "DEV_SYNC_TOKEN: ${DEV_SYNC_TOKEN:+<set>}"
echo "DEV_SYNC_TOKEN length: ${#DEV_SYNC_TOKEN}"

# List all environment variables (redact sensitive ones)
echo "=== All environment variables ==="
env | grep -v "TOKEN\|SECRET\|KEY" | sort
echo "=== Sensitive variables (names only) ==="
env | grep "TOKEN\|SECRET\|KEY" | cut -d= -f1 | sort

if [ -z "$AGENT_WORKDIR" ]; then
  echo "FATAL: AGENT_WORKDIR environment variable must be set."
  exit 1
fi

echo "=== Checking AGENT_WORKDIR ==="
echo "AGENT_WORKDIR is set to: $AGENT_WORKDIR"
if [ -d "$AGENT_WORKDIR" ]; then
  echo "AGENT_WORKDIR exists and is a directory"
  echo "Contents of AGENT_WORKDIR:"
  ls -la "$AGENT_WORKDIR" | head -20
else
  echo "ERROR: AGENT_WORKDIR does not exist or is not a directory!"
fi

if [ -z "$DEV_SYNC_TOKEN" ]; then
  echo "WARNING: DEV_SYNC_TOKEN environment variable is not set."
  echo "The sync server will start but file synchronization will not work without a valid token."
  echo "Waiting 10 seconds for secret injection..."
  sleep 10
  
  # Check again after waiting
  if [ -z "$DEV_SYNC_TOKEN" ]; then
    echo "FATAL: DEV_SYNC_TOKEN is still not set after waiting."
    echo "Make sure the secret is properly configured in the agent deployment."
    exit 1
  fi
fi

TOOLS_DIR="/opt/livekit-dev-tools"
echo "=== Checking tools directory ==="
echo "TOOLS_DIR: $TOOLS_DIR"
if [ -d "$TOOLS_DIR" ]; then
  echo "Tools directory exists. Contents:"
  ls -la "$TOOLS_DIR"
else
  echo "ERROR: Tools directory does not exist!"
  exit 1
fi

echo "--- Starting LiveKit Dev Mode Services ---"

# --- 1. Start the appropriate Sync Server in the background ---
echo "=== Starting sync server ==="
if [ -f "$TOOLS_DIR/sync_server.js" ]; then
    echo "Found Node.js sync server at: $TOOLS_DIR/sync_server.js"
    echo "Starting Node.js sync server with command:"
    echo "node $TOOLS_DIR/sync_server.js --token <redacted> --workdir $AGENT_WORKDIR"
    node $TOOLS_DIR/sync_server.js --token "$DEV_SYNC_TOKEN" --workdir "$AGENT_WORKDIR" &
    SYNC_PID=$!
    echo "Sync server started with PID: $SYNC_PID"
elif [ -f "$TOOLS_DIR/sync_server.py" ]; then
    echo "Found Python sync server at: $TOOLS_DIR/sync_server.py"
    echo "Checking if Python is available:"
    which python3 || echo "python3 not found in PATH!"
    python3 --version || echo "Failed to get Python version!"
    echo "Starting Python sync server with command:"
    echo "python3 $TOOLS_DIR/sync_server.py --token <redacted> --workdir $AGENT_WORKDIR"
    python3 $TOOLS_DIR/sync_server.py --token "$DEV_SYNC_TOKEN" --workdir "$AGENT_WORKDIR" &
    SYNC_PID=$!
    echo "Sync server started with PID: $SYNC_PID"
else
    echo "FATAL: No sync server script found in $TOOLS_DIR"
    echo "Looking for sync_server.js or sync_server.py"
    exit 1
fi

# Wait a moment for the server to bind to its port
echo "Waiting 2 seconds for sync server to initialize..."
sleep 2

# Check if sync server is still running
if kill -0 $SYNC_PID 2>/dev/null; then
    echo "Sync server is running"
else
    echo "ERROR: Sync server died immediately!"
    wait $SYNC_PID
    echo "Exit code: $?"
fi

# --- 2. Start the Cloudflared tunnel in the background ---
echo "=== Starting cloudflared tunnel ==="
echo "Checking if cloudflared is available:"
which cloudflared || echo "cloudflared not found in PATH!"
cloudflared version 2>&1 || echo "Failed to get cloudflared version!"

echo "Starting cloudflared tunnel to http://localhost:8080 ..."
# The tunnel output (including the public URL) will go to the container logs
cloudflared tunnel --url http://localhost:8080 &
CLOUDFLARED_PID=$!
echo "Cloudflared started with PID: $CLOUDFLARED_PID"

# Wait a moment for cloudflared to initialize
echo "Waiting 3 seconds for cloudflared to initialize..."
sleep 3

# Check if cloudflared is still running
if kill -0 $CLOUDFLARED_PID 2>/dev/null; then
    echo "Cloudflared is running"
else
    echo "ERROR: Cloudflared died immediately!"
    wait $CLOUDFLARED_PID
    echo "Exit code: $?"
fi

# --- 3. Start the Agent with nodemon ---
echo "=== Starting agent with nodemon ==="
echo "Checking if nodemon is available:"
which nodemon || echo "nodemon not found in PATH!"
nodemon --version || echo "Failed to get nodemon version!"

echo "--- Dev Mode Ready. Starting agent with nodemon... ---"
echo "Watching for file changes in: $AGENT_WORKDIR"
echo "Command line arguments: $@"
echo "Number of arguments: $#"
echo "---------------------------------------------------------"

# Use 'exec' to replace this shell process with the nodemon process.
# This ensures that nodemon receives signals (like SIGTERM from 'docker stop') correctly.
# "$@" is a special variable that expands to all arguments passed to this script,
# which will be the contents of the Dockerfile's CMD instruction.
echo "Executing: nodemon --watch \"$AGENT_WORKDIR\" --ext \"py,js,ts,json\" $@"
exec nodemon --watch "$AGENT_WORKDIR" --ext "py,js,ts,json" --exec "$*"