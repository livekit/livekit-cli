#!/usr/bin/env python3
import argparse
import re
import json
import shlex
from pathlib import Path

# --- Constants: The code blocks to be injected into the Dockerfile ---

# This RUN command installs all system-level dependencies for the dev mode.
# It switches to the root user to guarantee permissions for installation.
INSTALL_BLOCK = """
# === BEGIN LIVEKIT DEV-MODE INJECTION ===
# Switch to root to install dependencies
USER root

# Install system dependencies for dev mode: curl, Node.js (for nodemon), and cloudflared
RUN apt-get update && apt-get install -y --no-install-recommends curl ca-certificates gnupg \\
    && mkdir -p /etc/apt/keyrings \\
    && curl -fsSL https://deb.nodesource.com/gpgkey/nodesource-repo.gpg.key | gpg --dearmor -o /etc/apt/keyrings/nodesource.gpg \\
    && NODE_MAJOR=20 \\
    && echo "deb [signed-by=/etc/apt/keyrings/nodesource.gpg] https://deb.nodesource.com/node_$NODE_MAJOR.x nodistro main" | tee /etc/apt/sources.list.d/nodesource.list \\
    && apt-get update \\
    && apt-get install -y nodejs \\
    && npm install -g nodemon \\
    && ARCH=$(dpkg --print-architecture) \\
    && echo "Detected architecture: $ARCH" \\
    && curl -L --output cloudflared.deb https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-${ARCH}.deb \\
    && dpkg -i cloudflared.deb && rm cloudflared.deb \\
    && rm -rf /var/lib/apt/lists/*
"""

# This block copies the dev tools into a standard location and sets permissions.
COPY_BLOCK = """
# Copy the dev tools into a standard, isolated location
COPY dev-tools /opt/livekit-dev-tools/
RUN chmod +x /opt/livekit-dev-tools/live-dev-entrypoint.sh
"""

FINAL_INSTRUCTIONS = """
# Set the entrypoint to our dev-mode script. It will start all background
# services and then execute the original CMD from below.
ENTRYPOINT ["/opt/livekit-dev-tools/live-dev-entrypoint.sh"]

# The original command is passed as arguments to the new entrypoint
CMD {final_cmd}
# === END LIVEKIT DEV-MODE INJECTION ===
"""

def parse_instruction(line):
    """Parses a Dockerfile instruction, handling both shell and exec forms."""
    parts = line.strip().split(maxsplit=1)
    if len(parts) < 2:
        return []
    instruction_body = parts[1]

    # Try to parse as JSON array (exec form)
    try:
        # This handles cases like CMD ["/bin/sh", "-c", "echo hello"]
        return json.loads(instruction_body)
    except json.JSONDecodeError:
        # Fallback to shell form parsing
        return shlex.split(instruction_body)


def main():
    parser = argparse.ArgumentParser(
        description="""A script to convert a standard Dockerfile into a dev-mode enabled Dockerfile for LiveKit Agents.
        This script is non-destructive and creates a new 'Dockerfile.dev' file."""
    )
    parser.add_argument(
        "dockerfile_path",
        type=Path,
        help="Path to the original Dockerfile.",
    )
    args = parser.parse_args()

    if not args.dockerfile_path.is_file():
        print(f"Error: Dockerfile not found at '{args.dockerfile_path}'")
        exit(1)

    print(f"Processing '{args.dockerfile_path}'...")
    lines = args.dockerfile_path.read_text().splitlines()

    # --- Analysis Phase ---
    # Find the last instance of key instructions, as these are the ones that apply at runtime.
    last_from_idx = -1
    last_user_idx = -1
    last_user_val = "root" # Default Docker user is root
    original_entrypoint = []
    original_cmd = []

    for i, line in enumerate(lines):
        line_upper = line.strip().upper()
        if line_upper.startswith("FROM"):
            last_from_idx = i
        elif line_upper.startswith("USER"):
            last_user_idx = i
            last_user_val = line.strip().split(maxsplit=1)[1]
        elif line_upper.startswith("ENTRYPOINT"):
            original_entrypoint = parse_instruction(line)
            lines[i] = f"# DEV-MODE: Original command commented out\n# {line}"
        elif line_upper.startswith("CMD"):
            original_cmd = parse_instruction(line)
            lines[i] = f"# DEV-MODE: Original command commented out\n# {line}"

    if last_from_idx == -1:
        print("Error: Could not find a 'FROM' instruction in the Dockerfile.")
        exit(1)

    # Combine original ENTRYPOINT and CMD to form the final command to be run
    final_command_list = original_entrypoint + original_cmd
    if not final_command_list:
        print("Warning: Could not determine the original CMD or ENTRYPOINT. The final container may not start correctly.")
        print("Please ensure your original Dockerfile has a CMD or ENTRYPOINT instruction.")
        final_command_list = ["/bin/echo", "Warning: No original CMD or ENTRYPOINT found."]

    # --- Injection Phase ---
    new_lines = []
    # Add an instruction to switch back to the original user after installations
    user_reset_block = f"\n# Switch back to the original user\nUSER {last_user_val}\n"

    # Insert our blocks into the list of lines
    # 1. Add dependency installation right after the FROM instruction
    lines.insert(last_from_idx + 1, INSTALL_BLOCK + user_reset_block)

    # 2. Add the final instructions at the end of the file
    final_cmd_json = json.dumps(final_command_list)
    lines.append(FINAL_INSTRUCTIONS.format(final_cmd=final_cmd_json))

    # 3. Add the COPY block just before the final instructions
    # A good place is before the original USER instruction if it exists, otherwise before the end.
    insertion_point = last_user_idx if last_user_idx != -1 else len(lines) - 1
    lines.insert(insertion_point, COPY_BLOCK)

    # --- Output Phase ---
    output_path = args.dockerfile_path.parent / "Dockerfile.dev"
    output_path.write_text("\n".join(lines))

    print("\n✅ Success! ✨")
    print(f"A new dev-mode enabled Dockerfile has been created at: {output_path}")
    print("\nNext steps:")
    print("1. Ensure the 'dev-tools' directory is in the same folder.")
    print(f"2. Build the new image: docker build -t my-agent-dev -f {output_path} .")
    print("3. Run the container with the required environment variables:")
    print("   docker run --rm -it -e DEV_SYNC_TOKEN=\"your-secret\" -e AGENT_WORKDIR=\"/path/inside/container\" my-agent-dev")


if __name__ == "__main__":
    main()