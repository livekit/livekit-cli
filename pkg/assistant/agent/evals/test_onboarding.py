"""Evals for LK's onboarding behavior, from failures seen in real sessions.

They run the real LLM with lk's tools replaced by stubs that reply the way lk
does, so they need LiveKit Cloud credentials for LiveKit Inference
(LIVEKIT_URL, LIVEKIT_API_KEY, LIVEKIT_API_SECRET). They aren't part of the
embedded agent. Run them from the agent directory:

    uv run --with pytest --with pytest-asyncio pytest evals -v

LLM output varies, so run them a few times after changing the instructions.
"""

import json
import os
import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

import agent  # noqa: E402
from livekit.agents import AgentSession, inference  # noqa: E402

pytestmark = [
    pytest.mark.asyncio,
    pytest.mark.skipif(
        not all(os.environ.get(k) for k in ("LIVEKIT_URL", "LIVEKIT_API_KEY", "LIVEKIT_API_SECRET")),
        reason="needs LiveKit Cloud credentials for LiveKit Inference",
    ),
]

EMPTY = {"kind": "empty"}
LIVEKIT_PROJECT = {
    "kind": "livekit",
    "summary": "a Python project that uses LiveKit Agents",
    "projects": [{"path": ".", "language": "Python", "livekit_packages": [{"name": "livekit-agents", "version": "~=1.8"}]}],
    "has_agents": True,
}
READY = {"ready": True}

# What lk replies to each tool, close to the real text in cmd/lk.
ALREADY_SET_UP = (
    "Nothing was installed: LiveKit's skills and the Docs MCP server are already set up. Tell the user, "
    'and that "lk skills update" gets the latest skills. If they still want to reinstall, call '
    "set_up_coding_agent again with reinstall=true."
)
CONFIRM = (
    "Waiting for the user to confirm. Confirmation ID: confirm1. Command: {command}. "
    "Ask the user in one short sentence whether to go ahead. They can answer by voice or on screen. If they agree by voice, call confirm_action "
    "with this ID and approved=true; if they decline, approved=false."
)
EXISTING_AGENT = (
    "You can only run an agent here that you created in this session. Existing agents can need their "
    "frontend, dispatch, or other services, so they run in the user's own terminal. In one short sentence, "
    "tell the user you can't start it here, and that they can run it in their terminal to talk to it. Then "
    'call show_code with title "Terminal", language "shell", and this command: lk agent console.'
)


def stub_lk(calls: list, ready: bool = False):
    async def run_lk_tool(name: str, args: dict | None = None, timeout: float = 60) -> str:
        calls.append((name, args or {}))
        if name in ("create_project", "set_up_coding_agent", "try_project") and (args or {}).get("confirmed"):
            return "Done. The user confirmed by voice, and it ran."
        if name == "create_project":
            return CONFIRM.format(command="lk agent init . --template agent-starter-python --install")
        if name == "set_up_coding_agent":
            if ready and not (args or {}).get("reinstall"):
                return ALREADY_SET_UP
            return CONFIRM.format(command="lk skills install")
        if name == "confirm_action":
            return "Done. The project is in the current directory."
        if name == "try_project":
            return EXISTING_AGENT
        if name == "get_project_info":
            return json.dumps({"directory": "/Users/dev/my-app", "folder": EMPTY})
        if name == "list_templates":
            return "agent-starter-python: Python voice agent. agent-starter-node: Node.js voice agent."
        return "Done."

    return run_lk_tool


@pytest.fixture
def lk_calls(monkeypatch):
    calls: list = []
    monkeypatch.setattr(agent, "run_lk_tool", stub_lk(calls))
    return calls


@pytest.fixture
def lk_calls_ready(monkeypatch):
    """lk's tools when skills and the Docs MCP server are already set up."""
    calls: list = []
    monkeypatch.setattr(agent, "run_lk_tool", stub_lk(calls, ready=True))
    return calls


def assistant(monkeypatch, folder: dict, coding_agents: dict | None = None) -> agent.Assistant:
    monkeypatch.setenv("LK_ASSISTANT_PROJECT", "/Users/dev/my-app")
    monkeypatch.setenv("LK_ASSISTANT_FOLDER", json.dumps(folder))
    monkeypatch.setenv("LK_ASSISTANT_CODING_AGENTS", json.dumps(coding_agents or {}))
    return agent.Assistant()


def tool_calls(result) -> list[str]:
    return [e.item.name for e in result.events if e.type == "function_call"]


def judge_llm() -> inference.LLM:
    return inference.LLM(model=agent.LLM_MODEL)


async def test_creates_project_without_asking_twice(monkeypatch, lk_calls):
    """The user agreed to an offer; LK shouldn't ask again before running it."""
    async with AgentSession(llm=inference.LLM(model=agent.LLM_MODEL)) as session:
        await session.start(assistant(monkeypatch, EMPTY))
        await session.run(user_input="I want to build a voice agent in Python.")
        result = await session.run(user_input="Yes, create it here.")
        # Either path runs it in this turn: confirmed=true, or confirm_action right after.
        ran = any(
            (n == "create_project" and a.get("confirmed")) or (n == "confirm_action" and a.get("approved"))
            for n, a in lk_calls
        )
        assert ran, f"asked again instead of running it: {lk_calls}"


async def test_existing_agent_gets_command(monkeypatch, lk_calls):
    """An existing agent can't run here; LK says so and shows the command."""
    async with AgentSession(llm=inference.LLM(model=agent.LLM_MODEL)) as session:
        await session.start(assistant(monkeypatch, LIVEKIT_PROJECT, READY))
        result = await session.run(user_input="Can you start my agent so I can talk to it?")
        result.expect.contains_function_call(name="show_code")
        shown = [json.loads(e.item.arguments) for e in result.events if e.type == "function_call" and e.item.name == "show_code"]
        assert any("lk agent console" in s.get("code", "") for s in shown), shown
        await result.expect.contains_message(role="assistant").judge(
            judge_llm(),
            intent="Says it can't start the user's existing agent here, and doesn't claim the agent is running.",
        )


async def test_bug_fix_goes_to_coding_agent(monkeypatch, lk_calls):
    """LK can't read code; a bug fix becomes a prompt for the coding agent."""
    async with AgentSession(llm=inference.LLM(model=agent.LLM_MODEL)) as session:
        await session.start(assistant(monkeypatch, LIVEKIT_PROJECT, READY))
        result = await session.run(user_input="My agent crashes when the user hangs up. Can you fix it?")
        await result.expect.contains_message(role="assistant").judge(
            judge_llm(),
            intent="Doesn't claim to have read or fixed the code. Either hands the task to the user's coding agent or offers to.",
        )


async def test_no_setup_offer_when_ready(monkeypatch, lk_calls_ready):
    """Skills and the Docs MCP server are already installed."""
    async with AgentSession(llm=inference.LLM(model=agent.LLM_MODEL)) as session:
        await session.start(assistant(monkeypatch, LIVEKIT_PROJECT, READY))
        result = await session.run(user_input="Should I install LiveKit's skills for Cursor?")
        assert not any(a.get("reinstall") for n, a in lk_calls_ready if n == "set_up_coding_agent")
        await result.expect.contains_message(role="assistant").judge(
            judge_llm(), intent="Tells the user it's already set up, and doesn't offer to install it."
        )


async def test_docs_question_uses_docs(monkeypatch, lk_calls):
    """Answers about LiveKit come from the docs, with the source on screen."""
    async with AgentSession(llm=inference.LLM(model=agent.LLM_MODEL)) as session:
        await session.start(assistant(monkeypatch, EMPTY))
        result = await session.run(user_input="How does LiveKit decide when I've finished talking?")
        calls = tool_calls(result)
        assert any(c in calls for c in ("docs_search", "get_pages", "get_docs_overview")), calls
        assert any(c in calls for c in ("show_answer", "show_link")), f"no source shown: {calls}"


async def test_copy_targets_the_prompt(monkeypatch, lk_calls):
    """"Copy it" after a prompt card copies the prompt, not an older card."""
    async with AgentSession(llm=inference.LLM(model=agent.LLM_MODEL)) as session:
        await session.start(assistant(monkeypatch, EMPTY))
        await session.run(user_input="Show me the command to install LiveKit's skills.")
        await session.run(user_input="Now write a prompt for Cursor to change my agent's voice to something calmer.")
        result = await session.run(user_input="Copy the prompt.")
        call = result.expect.contains_function_call(name="copy_to_clipboard")
        title = json.loads(call.event().item.arguments).get("title", "")
        assert title == "" or "prompt" in title.lower(), title
