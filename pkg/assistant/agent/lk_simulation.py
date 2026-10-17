"""Agent Simulations support for LK. lk doesn't embed this file.

Under a simulation, there's no lk to run commands, so lk's tools are mocked
with replies like the ones lk sends (see cmd/lk/assistant_tools.go). The docs
tools stay real. Each scenario's userdata sets the folder context:

    userdata:
      folder: {kind: empty}                 # as pkg/assistant/folder.go reports it
      coding_agents: {ready: false}         # as cmd/lk/assistant_skills.go reports it
      expect:
        calls: [create_project]             # tools the agent must call
        not_calls: [set_up_coding_agent]    # tools it must not call
        any_calls: [show_link, show_answer] # it must call at least one
        show_code_contains: lk agent console
        lk_calls: [create_project]          # lk tools that must run, from code or the model
        confirmed: [create_project]         # ran without asking again
        call_contains: {hand_off_task: voice}

The simulation's judge doesn't see tool calls, so check_simulation checks them
when the simulation ends, and fails it if they don't match.
"""

from __future__ import annotations

import inspect
import json

import sys

from livekit.agents import AgentSession

DIR = "/Users/dev/my-app"


def setup_simulation(sim, session: AgentSession, assistant_cls) -> tuple[dict, dict]:
    userdata = sim.userdata() or {}
    folder = userdata.get("folder", {"kind": "empty"})
    coding_agents = userdata.get("coding_agents", {"ready": False})
    # The agent's tools and its tasks reach lk through run_lk_tool. Replace it
    # in the module that defines the agent, which runs as __main__. Each
    # simulation job runs in its own process, so this is per job.
    global mocks
    mocks = LkMocks(folder, coding_agents)
    sys.modules[assistant_cls.__module__].run_lk_tool = mocks.run
    return folder, coding_agents


# The job's mocks, so check_simulation can see which lk tools ran.
mocks: "LkMocks | None" = None


class LkMocks:
    # Each mock takes the tool's RunContext first, as the real tools do: the
    # SDK passes it positionally.
    def __init__(self, folder: dict, coding_agents: dict) -> None:
        self.folder = folder
        self.coding_agents = coding_agents
        self.pending: dict[str, str] = {}  # confirmation id → what it runs
        self.n = 0
        self.created = False
        self.calls: list[str] = []  # lk tools run, in order

    async def run(self, name: str, args: dict | None = None, timeout: float = 60) -> str:
        """Stands in for agent.run_lk_tool."""
        args = args or {}
        self.calls.append(name)
        if name == "show_choice":
            self.n += 1
            return f"choice{self.n}"
        if name == "choice_made":
            return "Marked."
        tool = self.tools().get(name)
        if tool is None:
            return f"Error: unknown tool {name!r}"
        # Pass only the arguments this mock takes, so older agents work too.
        params = inspect.signature(tool).parameters
        return await tool(None, **{k: v for k, v in args.items() if k in params})

    def tools(self) -> dict:
        return {
            "get_project_info": self.get_project_info,
            "list_cloud_agents": self.list_cloud_agents,
            "list_templates": self.list_templates,
            "create_project": self.create_project,
            "set_up_coding_agent": self.set_up_coding_agent,
            "confirm_action": self.confirm_action,
            "try_project": self.try_project,
            "copy_to_clipboard": self.copy_to_clipboard,
        }

    def propose(self, what: str, command: str, confirmed: bool) -> str:
        if confirmed:
            return self.perform(what)
        self.n += 1
        cid = f"confirm{self.n}"
        self.pending[cid] = what
        return (
            f"Waiting for the user to confirm. Confirmation ID: {cid}. Command: {command}. "
            "The command is on screen, so don't say it. Ask the user in a few words whether to go ahead. They can answer by voice or on "
            "screen. If they agree by voice, call confirm_action with this ID and approved=true; if "
            "they decline, approved=false."
        )

    def perform(self, what: str) -> str:
        if what.startswith("create"):
            template = what.partition(":")[2] or "agent-starter-python"
            self.created = True
            self.folder = {"kind": "livekit", "has_agents": True}
            return (
                f"Created an agent project from the {template} template in the current "
                "directory and installed its dependencies. It includes LiveKit's skills and Docs MCP "
                "server for coding agents."
            )
        if what == "skills":
            self.coding_agents = {"ready": True}
            return "Installed LiveKit's skills and the Docs MCP server for Cursor and Claude Code."
        if what == "try":
            # In a simulation, the user's agent doesn't really run. This is what lk tells the
            # assistant when the user comes back from it (see returnToAssistant).
            return (
                "The user's agent ran here, and the user talked to it. They're back now. Welcome them "
                "back in one short sentence and ask how it went or what they'd like to change."
            )
        return "Done."

    async def get_project_info(self, context) -> str:
        return json.dumps(
            {
                "directory": DIR,
                "folder": self.folder,
                "coding_agents": self.coding_agents,
                "livekit_project": {"name": "my-project", "url": "wss://my-project.livekit.cloud"},
            }
        )

    async def list_cloud_agents(self, context) -> str:
        return "[]"

    async def list_templates(self, context) -> str:
        return (
            "Agent templates:\n- agent-starter-python: A complete voice AI starter for LiveKit Agents "
            "with Python.\n- agent-starter-node: A complete voice AI starter for LiveKit Agents with "
            "Node.js.\n\nFrontend and app templates:\n- agent-starter-react: A web voice AI frontend "
            "built with Next.js."
        )

    async def create_project(
        self, context, template: str, name: str = "", in_current_directory: bool = False, confirmed: bool = False
    ) -> str:
        target = "." if in_current_directory else (name or "my-agent")
        return self.propose(f"create:{template}", f"lk agent init {target} --template {template} --install", confirmed)

    async def set_up_coding_agent(
        self, context, all_projects: bool = False, reinstall: bool = False, confirmed: bool = False
    ) -> str:
        if self.coding_agents.get("ready") and not reinstall:
            return (
                "Nothing was installed: LiveKit's skills and the Docs MCP server are already set up. "
                'Tell the user, and that "lk skills update" gets the latest skills.'
            )
        return self.propose("skills", "lk skills install" + (" --global" if all_projects else ""), confirmed)

    async def confirm_action(
        self, context, confirmation_id: str, approved: bool) -> str:
        what = self.pending.pop(confirmation_id, None)
        if what is None:
            return f'Error: there\'s no pending confirmation "{confirmation_id}"'
        if not approved:
            return "Cancelled. Nothing was changed."
        return self.perform(what)

    async def try_project(
        self, context, confirmed: bool = False) -> str:
        if not self.created:
            return (
                "You can only run an agent here that you created in this session. In one short "
                "sentence, tell the user you can't start it here, and that they can run it in their "
                'terminal. Then call show_code with title "Terminal", language "shell", and this '
                "command: lk agent console"
            )
        return self.propose("try", "lk agent console", confirmed)

    async def copy_to_clipboard(
        self, context, title: str = "") -> str:
        return f'Copied "{title or "the latest card"}" to the user\'s clipboard.'


def check_simulation(ctx) -> None:
    expect = (ctx.userdata() or {}).get("expect")
    if not expect:
        return
    calls = [
        item
        for item in ctx.job_context.primary_session.history.items
        if item.type == "function_call"
    ]
    names = [c.name for c in calls]
    problems = []
    for name in expect.get("calls", []):
        if name not in names:
            problems.append(f"didn't call {name}")
    if any_of := expect.get("any_calls"):
        if not any(name in names for name in any_of):
            problems.append(f"didn't call any of {', '.join(any_of)}")
    ran = mocks.calls if mocks else []
    for name in expect.get("lk_calls", []):
        if name not in ran:
            problems.append(f"didn't run lk tool {name}")
    for name in expect.get("not_calls", []):
        if name in names:
            problems.append(f"called {name}")
    for name in expect.get("confirmed", []):
        # Ran without asking again: confirmed=true, or confirm_action right after.
        ran = any(
            (c.name == name and json.loads(c.arguments or "{}").get("confirmed"))
            or c.name == "confirm_action"
            for c in calls
        )
        if not ran:
            problems.append(f"asked again before running {name}")
    for name, want in expect.get("call_contains", {}).items():
        args = [c.arguments.lower() for c in calls if c.name == name]
        if not any(want.lower() in a for a in args):
            problems.append(f"didn't call {name} with {want!r}")
    if want := expect.get("show_code_contains"):
        shown = [c.arguments for c in calls if c.name == "show_code"]
        if not any(want in s for s in shown):
            problems.append(f"didn't show {want!r} with show_code")
    if problems:
        ctx.fail(reason="Tool calls: " + "; ".join(problems) + f". Called: {', '.join(names) or 'nothing'}.")
