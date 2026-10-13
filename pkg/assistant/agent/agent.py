"""The LiveKit assistant: a voice agent that helps developers build with LiveKit.

`lk assistant` runs this agent in console mode and shows the conversation in
its overlay. Code the agent shows with `show_code` appears as a card in the
overlay, with a button to copy it.
"""

import asyncio
import json
import logging
import os
import re
import time
import urllib.request
from collections.abc import Awaitable, Callable

from livekit import agents
from livekit.agents import (
    Agent,
    AgentServer,
    AgentTask,
    AgentSession,
    RunContext,
    TurnHandlingOptions,
    APIConnectOptions,
    function_tool,
    inference,
    llm,
    mcp,
    text_transforms,
)
from livekit.agents.beta.tools import EndCallTool
from livekit.agents.voice import TranscriptSynchronizer, io
from livekit.agents.voice.agent_session import SessionConnectOptions

# Private module: the SDK's own stripper for expressive markup, the same one the
# room transcription output uses. Check it when upgrading livekit-agents.
from livekit.agents.tts._provider_format import TranscriptMarkupStripper

DOCS_MCP_URL = "https://docs.livekit.io/mcp"

STT_MODEL = "assemblyai/universal-3-6-pro"
STT_KEYTERMS = [
    "LiveKit",
    "LK",
    "LiveKit Cloud",
    "LiveKit Inference",
    "AgentSession",
    "agent dispatch",
    "turn detector",
    "WebRTC",
    "SIP",
    "MCP",
    "Node.js",
    "TypeScript",
    "Cartesia",
    "Deepgram",
    "AssemblyAI",
]
TTS_MODEL = "cartesia/sonic-3"

# Settings to compare in simulations (see simulations/README.md). The defaults
# are what lk ships.
LLM_MODEL = os.environ.get("LK_ASSISTANT_LLM", "openai/gpt-5.4-mini")
# How much the LLM reasons before it answers. Lower is faster, which matters
# in a voice conversation. "" uses the model's default.
REASONING_EFFORT = os.environ.get("LK_ASSISTANT_REASONING", "")

INTRO = "Hi, I'm LK, your LiveKit assistant."



def load_folder() -> dict:
    """What lk found in the user's current folder before the session started."""
    try:
        return json.loads(os.environ.get("LK_ASSISTANT_FOLDER", "{}"))
    except json.JSONDecodeError:
        return {}


def load_coding_agents() -> dict:
    """What LiveKit has already set up for the user's coding agents: skills and the
    Docs MCP server, in their project and globally."""
    try:
        return json.loads(os.environ.get("LK_ASSISTANT_CODING_AGENTS", "{}"))
    except json.JSONDecodeError:
        return {}


def greeting(folder: dict, coding_agents: dict) -> str:
    """A short, fixed greeting for what's in the user's folder. It offers what
    fits without describing the folder; the agent knows the details from its
    instructions. It's not generated, so it starts right away."""
    kind = folder.get("kind")
    # One sentence that offers what fits the folder, without describing it.
    if kind == "empty":
        return f"{INTRO} I can help you set up a new voice agent, or answer your questions about LiveKit."
    if kind == "livekit":
        return f"{INTRO} I see you have a LiveKit project up and going. Any questions about LiveKit I can help you with?"
    return f"{INTRO} Any questions about LiveKit I can help you with?"


# Pronunciations for the TTS, as Cartesia inline IPA overrides: "live" as in
# "alive", not "liver". They apply only to the speech, so transcripts keep the
# real spelling. Matching ignores case, and longer keys match first, so the
# possessive gets its own key. The syntax is Cartesia-specific; see
# docs.livekit.io/agents/multimodality/text/#built-in-replace-transform.
LIVEKIT_IPA = "<<ˈ|l|aɪ|v>> <<ˈ|k|ɪ|t>>"
LIVEKITS_IPA = "<<ˈ|l|aɪ|v>> <<ˈ|k|ɪ|t|s>>"
PRONUNCIATIONS = {
    "LiveKit's": LIVEKITS_IPA,
    "LiveKit’s": LIVEKITS_IPA,
    "LiveKit": LIVEKIT_IPA,
    "Live Kit": LIVEKIT_IPA,
    # Said as letters, without the dot.
    "Next.js": "Next J S",
    "Node.js": "Node J S",
}

# The assistant's name, LK, and the lk command are said as the letters L and
# K. Matching is case-sensitive, and the lowercase keys include the spaces or
# punctuation around the word, so that words like "walk" don't match. The
# Markdown filter has already removed backticks.
LK_IPA = "<<ˈ|ɛ|l>> <<ˈ|k|eɪ>>"
NAME_PRONUNCIATIONS = {
    "LK": LK_IPA,
    " lk ": f" {LK_IPA} ",
    " lk.": f" {LK_IPA}.",
    " lk,": f" {LK_IPA},",
    " lk?": f" {LK_IPA}?",
}

# Links to read more must point here.
DOCS_URL = "https://docs.livekit.io/"

# The Docs MCP tools the agent uses.
DOCS_TOOLS = ("get_docs_overview", "docs_search", "read_docs", "code_search")
# The Docs MCP tools the agent uses. It reads pages with its own read_docs
# instead of get_pages, so code limits how many pages and how much text.
MCP_DOCS_TOOLS = ("get_docs_overview", "docs_search", "code_search")

# What the agent says while it looks something up, if it hasn't said anything
# yet this turn. Docs lookups take a second or two.
# Docs MCP results, such as searches, can be long.
# Cap them, because every character slows the LLM, and a large context made
# replies time out. Most answers come from the start of a page.
MAX_TOOL_RESULT_CHARS = 20000

# What old docs results become once a reply used them (see
# Assistant.on_user_turn_completed).
DOCS_RESULT_STUB = "[Docs content from an earlier answer, removed to keep replies fast. Look it up again if you need it.]"

INSTRUCTIONS = """You are LK, the LiveKit assistant, a voice assistant inside the LiveKit CLI.
You help developers build voice AI agents with LiveKit, add LiveKit to apps they already have, and
explore what LiveKit can do. You are yourself a LiveKit voice agent, built with the same Agents SDK
and LiveKit Inference the user can build with. Share the knowledge that helps the user build a
voice agent that works well, and give context where it helps.

Match the user's level. If they seem new, describe outcomes before LiveKit terms, and explain a
term in a few words the first time you use it. If they seem experienced, be terse and precise, and
skip the basics. If they ask you to be briefer or to go deeper, do that for the rest of the
conversation.

You speak out loud, so:
- Keep replies short: one to three sentences. Don't cover everything; the user can ask for more.
  Don't end with offers such as "If you want, I can".
- Use plain sentences in the style of Simplified Technical English: one idea per sentence, active
  voice, and common words. No markdown, lists, emojis, or symbols, except backticks around a short
  command or name, such as `lk agent dev`. Backticks show it in monospace and aren't spoken.
- Never read code, commands, or long identifiers aloud. Call show_code to show them,
  then say one short sentence about what you showed. When the user asks how to do something in
  code, or asks for an example, call show_code with a short example from the docs right away.
  Never say code "looks like this" without showing it.
- When it helps, use yourself as a one-sentence example after the answer. You're a LiveKit agent,
  so you're built from the same parts; see "How you, LK, are built" below.

LiveKit changes often, and what you remember about it is out of date. For every question about
LiveKit features, APIs, SDKs, models, plugins, CLI commands, pricing, or configuration, call
docs_search first, then answer only from what the docs say. Do this even when you think you know
the answer. For questions about how something works or how to configure it, also read the most
relevant page with read_docs before you answer. When you're not sure where a topic lives, or the
user asks what LiveKit can do, call get_docs_overview to see how the docs are organized. If the
docs don't cover it, say so. Never guess an API. When a docs page covers the topic in more depth,
call show_link with it.

Finding the right pages:
- Pick pages from the docs page list by title: the page whose title matches the topic, not one
  that only shares a keyword. Prefer the main topic page over a related one. For a general "how
  do I" question, prefer an overview or guide page. Don't read pages under /recipes/ for a first
  answer.
- Keep lookups short, because the user is waiting: at most one docs_search per reply (put several
  queries in its queries list), then one read_docs call with {pages}, then answer.
  When a title in the list clearly matches, skip docs_search and go straight to read_docs.
- Use only paths from the docs page list or from search results. Don't guess paths.

Be honest about what LiveKit does:
- If the docs don't confirm that a feature exists, say so. Don't combine features unless the docs
  show they work together.
- When LiveKit supports something only partly, say what's missing. A "yes" that leaves out a
  known limit is worse than a "no".
- Don't state numbers, such as prices, limits, or latencies, unless they're in a page you read.
- If a question is ambiguous, ask one short question to find out what the user means. Speech
  recognition can garble words, so read an odd question as a phonetic hint, such as "live kit"
  for LiveKit.

You can't see or change the user's files. You help them plan and decide, and you set them up to
do the work:
- To learn about their LiveKit Cloud project and current directory, call get_project_info. To check
  on deployed agents, call list_cloud_agents.
- For a new voice agent, call create_voice_agent. It asks the user to confirm and to pick a
  language, creates the project, and offers to let them talk to it. It creates the project only
  in the current folder, and only if the folder is empty. New projects already include LiveKit's
  skills and Docs MCP server for the user's coding agents.
- For an existing project, offer set_up_coding_agent, which installs LiveKit's skills and Docs MCP
  server into the user's coding agents (such as Claude Code, Cursor, or Codex), unless they're
  already set up.
- To add LiveKit to an existing app (get_project_info reports its framework), explain the three
  parts in plain words: the app gets a token from a small server endpoint, joins a LiveKit room,
  and the agent joins that room through dispatch. Then create the agent with create_voice_agent
  and give the frontend and token work to their coding agent with hand_off_task, based on the docs.
- While the user's own agent runs, the user talks to it, not to you. Never speak as their agent.
  To let them talk to it again later, call try_project.
- If the user asks how you work, or what they'll be building, call explain_yourself.
- When the user is ready to write code, call hand_off_task with a prompt for their coding agent:
  what to build or change, plus what you learned from the docs, such as package names, APIs, and
  CLI commands. If what they want is still vague, such as "add some tools", first ask one short
  question about what exactly.
- For anything you can't do here, such as running an agent they already had, say so in one
  sentence and give the next best thing: a command to run, with show_code, or a prompt for their
  coding agent.

set_up_coding_agent and try_project don't run right away. They show the command to the user and
return a confirmation ID. Ask the user in one short sentence whether to go ahead. They can answer
by voice or on screen. If they agree by voice, call confirm_action with the ID and approved=true;
if they decline, approved=false. Never call confirm_action unless the user has just agreed.

When the user says they're done or says goodbye, call end_call. Don't say goodbye yourself;
end_call says it and closes the window.

Prefer the LiveKit CLI for project tasks. For example, "lk agent init" creates an agent from a
starter template, "lk agent dev" runs it, and "lk agent create" deploys it to LiveKit Cloud."""

# The docs table of contents: every page with a one-line description. With it
# in the prompt, the agent picks pages by title before it reads them. It's
# trimmed to what onboarding questions need, about 10,000 tokens, and cached.
DOCS_TOC_URL = "https://docs.livekit.io/api/toc/"
DOCS_TOC_SKIP = re.compile(
    r"/plugins/|/reference/|/components/|/robotics|/recipes/|youtube|"
    r"/models/(stt|tts|llm|avatar)/[a-z]|/transport/self-hosting|/transport/media/ingress"
)
DOCS_TOC_TTL = 24 * 60 * 60


def docs_toc() -> str:
    """The trimmed docs table of contents, from the cache or the docs site, or ""."""
    cache = os.path.join(os.path.expanduser("~/Library/Caches"), "livekit", "assistant-docs-toc.md")
    try:
        if time.time() - os.path.getmtime(cache) < DOCS_TOC_TTL:
            return open(cache, encoding="utf-8").read()
    except OSError:
        pass
    try:
        with urllib.request.urlopen(DOCS_TOC_URL, timeout=2) as resp:
            text = resp.read().decode("utf-8")
    except Exception:
        try:
            return open(cache, encoding="utf-8").read()  # a stale copy is better than none
        except OSError:
            return ""
    toc = "\n".join(line for line in text.splitlines() if line.strip() and not DOCS_TOC_SKIP.search(line))
    try:
        os.makedirs(os.path.dirname(cache), exist_ok=True)
        with open(cache, "w", encoding="utf-8") as f:
            f.write(toc)
    except OSError:
        pass
    return toc


# How many docs pages one reply may read. Fewer is faster.
MAX_PAGES = int(os.environ.get("LK_ASSISTANT_MAX_PAGES", "3"))

# How much docs text one read_docs call returns, split between its pages. Every
# character slows the LLM, and most answers come from the start of a page.
DOCS_READ_CHARS = int(os.environ.get("LK_ASSISTANT_DOCS_CHARS", "12000"))


def docs_page_url(path: str) -> str:
    """The Markdown URL for a docs path or URL."""
    path = path.strip()
    for prefix in ("https://docs.livekit.io", "http://docs.livekit.io"):
        if path.startswith(prefix):
            path = path[len(prefix) :]
    path = "/" + path.strip("/").removesuffix(".md")
    return "https://docs.livekit.io" + path + ".md"


def fetch_docs_page(url: str) -> str:
    with urllib.request.urlopen(url, timeout=8) as resp:
        return resp.read().decode("utf-8")


async def read_docs_pages(paths: list[str]) -> str:
    """Fetches docs pages as Markdown, in parallel, each trimmed to its share of
    DOCS_READ_CHARS."""
    if not paths:
        return "No paths given. Pick pages from the docs page list."
    share = DOCS_READ_CHARS // len(paths)

    async def one(path: str) -> str:
        url = docs_page_url(path)
        try:
            text = await asyncio.to_thread(fetch_docs_page, url)
        except Exception as e:
            return f"## {path}\n\nCouldn't read this page ({e}). Check the path in the docs page list."
        if len(text) > share:
            text = text[:share] + "\n... [the rest of the page is cut]"
        return f"## {url.removesuffix('.md')}\n\n{text}"

    return "\n\n".join(await asyncio.gather(*(one(p) for p in paths)))


def about_yourself() -> str:
    """Facts about how LK is built, from its own settings, so it can use itself as
    an example."""
    return f"""How you, LK, are built. Every part is something the user can use too:
- A cascaded pipeline on LiveKit Inference: STT {STT_MODEL}, with keyterms that help it hear
  LiveKit's names; LLM {LLM_MODEL}; and TTS {TTS_MODEL}, in expressive mode. Text transforms fix
  how the TTS says "LiveKit".
- Turn detection: LiveKit's turn detector model, with dynamic endpointing that learns the user's
  pauses.
- Tools: the LiveKit Docs MCP server, connected with MCPToolset, to search the docs, plus your own
  function tools, such as read_docs to read pages, show_code for code cards, and hand_off_task
  for prompts.
- A task: creating the user's agent is an AgentTask. Code runs its steps, and the model only
  reads the user's answers and words the replies, so it can't skip a step.
- Testing: Agent Simulations, where an LLM plays the user and a judge grades each conversation,
  in text and audio mode, with scenarios to compare configurations and catch regressions.
- You run on the user's machine in console mode, started by lk.

For example: "I work this way too: I call the Docs MCP server as a tool to get current docs."
Don't use yourself as an example in every answer, and only say what's in this list."""


def build_instructions() -> str:
    pages = "only the one most relevant path" if MAX_PAGES == 1 else f"up to {MAX_PAGES} paths"
    instructions = INSTRUCTIONS.replace("{pages}", pages) + "\n\n" + about_yourself()
    toc = docs_toc() if os.environ.get("LK_ASSISTANT_TOC", "1") == "1" else ""
    if not toc:
        return instructions
    return (
        instructions
        + "\n\nThe docs pages, as [Section > Title](path): description. Pick pages from this list "
        "by title, and pass their paths to read_docs.\n\n"
        + toc
    )


async def truncate_result(ctx: mcp.MCPToolResultContext) -> str:
    if len(ctx.result.content) == 1:
        text = str(ctx.result.content[0].model_dump_json())
    elif ctx.result.content:
        text = json.dumps([item.model_dump() for item in ctx.result.content])
    else:
        return "The tool returned no content."
    if len(text) > MAX_TOOL_RESULT_CHARS:
        text = text[:MAX_TOOL_RESULT_CHARS] + "\n... [truncated]"
    return text


class Assistant(Agent):
    def __init__(self, folder: dict | None = None, coding_agents: dict | None = None) -> None:
        instructions = build_instructions()
        folder = load_folder() if folder is None else folder
        coding_agents = load_coding_agents() if coding_agents is None else coding_agents
        self.folder = folder
        self.created = False  # whether create_voice_agent created a project
        if project := os.environ.get("LK_ASSISTANT_PROJECT"):
            instructions += f"\n\nThe user's current directory is {project}."
        if folder:
            instructions += (
                " Before the session started, lk looked at the project files in it and its "
                f"subfolders, and found this: {json.dumps(folder)}. Your greeting only offered what fits, "
                "without describing the folder. Use these details when they help, but don't list them unless the "
                "user asks. Versions are as the project files give them. Call get_project_info if "
                "you need more, or if the folder may have changed."
            )
        if coding_agents:
            instructions += (
                "\n\nWhat LiveKit has already set up for the user's coding agents (LiveKit skills and "
                f"the Docs MCP server, in this project and globally): {json.dumps(coding_agents)}. "
                "Offer set_up_coding_agent only for what's missing, and say what's missing. If both "
                "are already set up, don't offer it. If the user asks, tell them it's already done, "
                'and that "lk skills update" gets the latest skills. After set_up_coding_agent runs, '
                "call get_project_info for the current state."
            )
        super().__init__(
            instructions=instructions,
            tools=[
                mcp.MCPToolset(
                    id="livekit-docs",
                    mcp_server=mcp.MCPServerHTTP(
                        DOCS_MCP_URL,
                        allowed_tools=list(MCP_DOCS_TOOLS),
                        tool_result_resolver=truncate_result,
                    ),
                ),
                # lk closes its overlay when it sees this tool called. Console
                # mode has no real room, so there's nothing to delete.
                *EndCallTool(
                    extra_description=(
                        "Call this whenever the user wants to stop or leave, such as saying exit, "
                        "close, quit, bye, or that's all. Saying goodbye without calling it leaves "
                        "the window open."
                    ),
                    delete_room=False,
                    end_instructions="Say a short, friendly goodbye in one sentence.",
                ).tools,
            ],
        )

    async def on_user_turn_completed(self, turn_ctx, new_message) -> None:
        # Docs pages are large, and every later turn would send them to the LLM
        # again. Once a reply has used them, keep only a stub; the agent can
        # look a page up again.
        changed = drop_old_docs(turn_ctx)
        ctx = self.chat_ctx.copy()
        if drop_old_docs(ctx) or changed:
            await self.update_chat_ctx(ctx)

    @function_tool()
    async def read_docs(self, context: RunContext, paths: list[str]) -> str:
        """Read LiveKit docs pages. Pick them by title from the docs page list or from search
        results.

        Args:
            paths: Page paths, such as /agents/models/pipelines. Only the first few are read.
        """
        return await read_docs_pages(paths[:MAX_PAGES])

    @function_tool()
    async def show_code(self, context: RunContext, title: str, language: str, code: str) -> str:
        """Show code or a terminal command to the user. The user sees it on screen and can
        copy it. Use this for all code and commands instead of saying them.

        Args:
            title: A file name such as agent.py, or "Terminal" for shell commands.
            language: The language of the code, such as python, typescript, or shell.
            code: The complete code or command, exactly as the user should use it.
        """
        # `lk assistant` reads this call from the session events and shows it.
        return (
            "The code is on the user's screen, with a Copy button. If you haven't said what it is, "
            "say so in one sentence. Don't repeat what you already said."
        )

    @function_tool()
    async def show_link(self, context: RunContext, title: str, url: str) -> str:
        """Show a link to a LiveKit docs page where the user can read more. The user can open
        it in their browser or copy it.

        Args:
            title: The page title, such as "LiveKit turn detector".
            url: The page's full URL, exactly as your docs search or read_docs returned it. It
                must start with https://docs.livekit.io/.
        """
        # `lk assistant` reads this call from the session events and shows it.
        if not url.startswith(DOCS_URL):
            return f"Not shown: links must be docs pages under {DOCS_URL}, from your docs searches."
        return "The link is on the user's screen. Don't repeat what you already said."

    @function_tool()
    async def copy_to_clipboard(self, context: RunContext, title: str = "") -> str:
        """Copy a code, command, or prompt card on screen to the user's clipboard. Use it only
        when the user asks you to copy something.

        Args:
            title: Part of the card's title, such as "agent.py", "Terminal", or "Prompt". Leave it
                empty for the most recent card.
        """
        return await run_lk_tool("copy_to_clipboard", {"title": title})

    @function_tool()
    async def hand_off_task(self, context: RunContext, task: str) -> str:
        """Show the user a prompt to paste into their coding agent, with a button to copy it.
        Call it right away when the user wants a change; don't ask whether to.

        Args:
            task: A short, self-contained prompt addressed to the coding agent, like a senior
                engineer's ticket. For a change, such as a new voice: the exact change, with the
                API, model, and values you looked up in the docs, and how to check it works.
                Pick one option; if the choice is the user's, ask them first. If the request is
                vague, such as "add some tools", ask one short question about what exactly before
                you call this; don't write a prompt that leaves the work undefined. For a bug: the
                problem, how to reproduce it, and how to check it's fixed, without guessing a
                fix, because you haven't seen the code.
        """
        # `lk assistant` reads this call from the session events and shows it.
        return (
            "The prompt is on the user's screen. In one sentence, tell the user what the change is, "
            "naming any choice you made, such as the voice you picked, and that they can copy it "
            "into their coding agent. You can't send it yourself, so don't say you handed it over."
        )

    @function_tool()
    async def get_project_info(self, context: RunContext) -> str:
        """Get the user's LiveKit Cloud project (the one lk uses), their current directory, and
        whether it's an agent project, including the ID of its deployed agent if it has one."""
        return await run_lk_tool("get_project_info")

    @function_tool()
    async def list_cloud_agents(self, context: RunContext) -> str:
        """List the agents deployed to the user's LiveKit Cloud project, with their status."""
        return await run_lk_tool("list_cloud_agents")

    @function_tool()
    async def list_templates(self, context: RunContext) -> str:
        """List LiveKit's project templates: agents (Python and Node.js) and frontends (web,
        mobile, and others), with a description of each."""
        return await run_lk_tool("list_templates")

    @function_tool()
    async def create_voice_agent(self, context: RunContext, another: bool = False) -> str | None:
        """Create a new voice agent for the user from a LiveKit starter template, then offer to
        let them talk to it. Call it when the user wants to build, create, or set up a new agent.
        It takes over until the project exists: it asks the user to confirm and to pick Python or
        Node.js, so don't ask first.

        Args:
            another: True only if the user explicitly asks for a second, separate agent after
                one was already created in this conversation.
        """
        if self.created and not another:
            return (
                "Nothing was created: the user's agent already exists from earlier in this "
                "conversation. Don't create another unless the user explicitly asks for a second one."
            )
        if self.folder.get("kind") != "empty":
            return (
                "Nothing was created: LK creates a new agent only in an empty current folder, and this "
                "folder isn't empty. Tell the user to make an empty folder, go to it, and run "
                "`lk assistant` there."
            )
        result = await NewAgentTask(chat_ctx=self.chat_ctx)
        if result.startswith("Created") or result == STARTED:
            self.created = True
            self.folder = {"kind": "livekit", "has_agents": True}
        if result == STARTED:
            # The task already announced the hand-off, and the user is about to
            # talk to their agent, so there's nothing to add.
            return None
        return result

    @function_tool()
    async def set_up_coding_agent(
        self,
        context: RunContext,
        all_projects: bool = False,
        reinstall: bool = False,
        confirmed: bool = False,
    ) -> str:
        """Install LiveKit's agent skills and the LiveKit Docs MCP server into the user's coding
        agents (such as Claude Code, Cursor, and Codex), with lk skills install. New projects from
        create_voice_agent already have them. If they're already set up, it says so and installs
        nothing. Unless confirmed is true, it shows the command and waits for the user to confirm
        (see confirm_action).

        Args:
            all_projects: Install for all of the user's projects instead of only the current one.
            reinstall: Install even though they're already set up. Only if the user asks for it.
            confirmed: True if the user's last message asked for this or agreed to it, such
                as "set up a voice agent here" or "yes" after you offered it. Then it runs
                right away, without asking again. Otherwise leave it false, and ask.
        """
        return await run_lk_tool(
            "set_up_coding_agent",
            {"global": all_projects, "reinstall": reinstall, "confirmed": confirmed},
        )

    @function_tool()
    async def try_project(self, context: RunContext, confirmed: bool = False) -> str:
        """Run the agent the user created in this session, in this window, so they can talk to
        it. The conversation switches to their agent until they press B to come back to you.
        Unless confirmed is true, it waits for the user to confirm (see confirm_action). For any
        other agent, it tells you how the user can run it in their own terminal.

        Args:
            confirmed: True if the user's last message asked for this or agreed to it, such
                as "set up a voice agent here" or "yes" after you offered it. Then it runs
                right away, without asking again. Otherwise leave it false, and ask.
        """
        return await run_lk_tool("try_project", {"confirmed": confirmed})

    @function_tool()
    async def explain_yourself(self, context: RunContext) -> str:
        """Show the user how you, LK, are built: your own AgentSession code, on screen. Use it
        only when they ask how you work or how you're built. For an example of how to build an
        agent, show code from the docs with show_code instead."""
        code = own_session_code()
        if lk is not None and code:
            lk.send({"type": "show_code", "title": "agent.py (how LK is built)", "language": "python", "code": code})
        return (
            "Your own setup is on the user's screen. You run on the LiveKit Agents SDK in "
            "console mode, inside lk. Your pipeline: speech to text with AssemblyAI, the "
            "gpt-5.4-mini LLM, and text to speech with Cartesia, all through LiveKit Inference. "
            "LiveKit's turn detector decides when the user has finished, with dynamic "
            "endpointing. Expressive mode adds emotion and pacing to your voice. Your tools are "
            "the LiveKit Docs MCP server plus lk commands. In one or two sentences, explain this "
            "and point out that a project from agent-starter-python has the same structure."
        )

    @function_tool()
    async def confirm_action(self, context: RunContext, confirmation_id: str, approved: bool) -> str:
        """Run or cancel a command that set_up_coding_agent or try_project proposed, after the
        user answers by voice. Only call it right after the user agrees or declines.

        Args:
            confirmation_id: The ID the proposing tool returned, such as confirm1. Works for
                set_up_coding_agent and try_project.
            approved: True if the user agreed, false if they declined.
        """
        # The command changes the user's files. While it runs, user speech
        # mustn't interrupt this step, or its result is never spoken.
        context.disallow_interruptions()
        try:
            return await run_lk_tool(
                "confirm_action",
                {"confirmation_id": confirmation_id, "approved": approved},
                timeout=COMMAND_TIMEOUT,
            )
        finally:
            # The reply after the command reuses this step's speech. Let the
            # user talk over it, or what they say while it plays is dropped.
            context.speech_handle.allow_interruptions = True


# Confirmed commands can take a while, such as installing dependencies.
COMMAND_TIMEOUT = 600


def drop_old_docs(ctx) -> bool:
    """Replaces the output of docs lookups in ctx with DOCS_RESULT_STUB.
    Reports whether anything changed."""
    docs_calls = {
        item.call_id
        for item in ctx.items
        if item.type == "function_call" and item.name in DOCS_TOOLS
    }
    changed = False
    for item in ctx.items:
        if item.type == "function_call_output" and item.call_id in docs_calls and item.output != DOCS_RESULT_STUB:
            item.output = DOCS_RESULT_STUB
            changed = True
    return changed


# The starter templates create_voice_agent uses, by language.
STARTERS = {"python": "agent-starter-python", "node": "agent-starter-node"}

# How each starter opens a conversation, for the hand-off: the Node.js starter
# greets the user when it joins, and the Python starter waits for them.
STARTER_OPENINGS = {
    "python": "once it connects, they should say hello, because it waits for them to speak first",
    "node": "once it connects, it greets them first",
}

NEW_AGENT_INSTRUCTIONS = """You are LK, the LiveKit assistant, helping the user create a new voice
agent from a LiveKit starter template. Code runs the steps. Say only what you're asked to say, in
short, plain sentences, and don't read commands aloud. Never say a step happened unless you're told
it did."""

# The answers each question in NewAgentTask accepts, for read_answer.
LANGUAGE_ANSWERS = {
    "python": "chooses or confirms Python",
    "node": "chooses or confirms Node.js, JavaScript, or TypeScript",
    "cancel": "doesn't want to create an agent now",
    "leave": "wants to stop talking to LK or close it",
    "other": "is unsure, asks a question, or says something else",
}
TRY_ANSWERS = {
    "yes": "wants to talk to their new agent now",
    "no": "doesn't want to talk to it now",
    "leave": "wants to stop talking to LK or close it",
    "other": "asks a question or says something else",
}


async def read_answer(model: llm.LLM, conversation: str, answers: dict[str, str], answer: str) -> str:
    """Asks the model which of answers the user's answer means, given the last
    few lines of the conversation, including the question LK actually asked. A
    narrow call like this one is more reliable than a conversation that must
    decide to call a tool. Returns "other" if the reply isn't one of the
    answers."""
    ctx = llm.ChatContext.empty()
    options = "\n".join(f"- {key}: the user {meaning}" for key, meaning in answers.items())
    ctx.add_message(
        role="system",
        content=(
            "Classify the user's answer to LK's last question. Read it in context: \"yes\" to "
            "\"Create it in Python?\" means Python. Reply with only one of these words:\n" + options
        ),
    )
    ctx.add_message(role="user", content=f"Conversation so far:\n{conversation}\n\nThe user's answer: {answer}")
    text = ""
    async with model.chat(chat_ctx=ctx) as stream:
        async for chunk in stream:
            if chunk.delta and chunk.delta.content:
                text += chunk.delta.content
    word = text.strip().strip(".\"'").lower()
    return word if word in answers else "other"


def recent_lines(chat_ctx, before: str, n: int = 4) -> str:
    """The last n messages before the message with id before, as "Role: text" lines."""
    lines = []
    for item in chat_ctx.items:
        if item.id == before:
            break
        if item.type == "message" and item.role in ("user", "assistant") and item.text_content:
            who = "User" if item.role == "user" else "LK"
            lines.append(f"{who}: {item.text_content}")
    return "\n".join(lines[-n:])


# What NewAgentTask returns when it has started the user's agent and announced it.
STARTED = "Started the user's agent, and told them how to come back."


class NewAgentTask(AgentTask[str]):
    """Creates the user's new agent and offers to let them talk to it.

    Code runs every step. Each of the user's answers is read with read_answer,
    acted on in code, and only then worded by the model. This happens in
    llm_node, which every reply goes through, whether the user spoke or typed.
    The model has no tools here, so it can't skip a step, answer its own
    question, or say a step happened when it didn't. Each question is also a
    choice card, so the user can answer on screen.
    """

    def __init__(self, chat_ctx) -> None:
        super().__init__(instructions=NEW_AGENT_INSTRUCTIONS, chat_ctx=chat_ctx)
        self.step = "language"  # language, creating, try, finished
        self.card = ""
        self.scripted = 0  # replies that code asked for, not yet generated
        # The id of the last user message code handled. The message that started
        # this task isn't an answer to its first question.
        self.handled = next(
            (i.id for i in reversed(chat_ctx.items) if i.type == "message" and i.role == "user"), ""
        )

    async def on_enter(self) -> None:
        await self._ask(
            "Create a voice agent in this folder?",
            [("python", "Python"), ("node", "Node.js"), ("cancel", "Not now")],
            "Ask, in these words or close to them: \"Do you want to use Python or Node?\" It goes in "
            "the current folder, so don't offer another folder. If they already said a language, "
            "ask them to confirm it instead.",
        )

    async def llm_node(self, chat_ctx, tools, model_settings):
        if self.scripted:
            # A reply code asked for with _say; its instructions are in chat_ctx.
            self.scripted -= 1
        else:
            user = next((i for i in reversed(chat_ctx.items) if i.type == "message" and i.role == "user"), None)
            if user is None or user.id == self.handled:
                return
            self.handled = user.id
            say = await self._handle(user.text_content or "", recent_lines(chat_ctx, user.id))
            if not say:
                return
            chat_ctx = chat_ctx.copy()
            chat_ctx.add_message(role="system", content=say)
        async for chunk in Agent.default.llm_node(self, chat_ctx, [], model_settings):
            yield chunk

    async def _handle(self, answer: str, conversation: str) -> str | None:
        """Acts on the user's answer, and returns what the reply should say, if anything."""
        if self.step == "language":
            choice = await read_answer(self.session.llm, conversation, LANGUAGE_ANSWERS, answer)
            if choice in STARTERS:
                await self._answered(choice)
                asyncio.create_task(self._create(choice))
                name = "Python" if choice == "python" else "Node.js"
                return f"In one short sentence, tell the user you're creating their {name} agent now."
            if choice == "cancel":
                await self._answered("cancel")
                self._done("The user chose not to create an agent now. Nothing was created.")
                return None
            if choice == "leave":
                self._done("Nothing was created. The user wants to stop, so call end_call.")
                return None
            return (
                "Answer what the user said in one or two short sentences. If they're unsure: Python "
                "has the most examples, and Node.js fits if their app is JavaScript or TypeScript. "
                "Then ask: \"Do you want to use Python or Node?\""
            )
        if self.step == "try":
            choice = await read_answer(self.session.llm, conversation, TRY_ANSWERS, answer)
            if choice in ("yes", "no"):
                await self._answered("try" if choice == "yes" else "later")
                asyncio.create_task(self._finish(choice == "yes"))
                return None
            if choice == "leave":
                self._done("Created the project. The user wants to stop, so call end_call.")
                return None
            return (
                "Answer what the user said in one or two short sentences, then ask whether they want "
                "to talk to their new agent now."
            )
        if self.step == "creating":
            return "In a few words, tell the user their agent is still being created."
        return None

    def _say(self, instructions: str):
        """Has the model say something code decided. Returns its SpeechHandle."""
        self.scripted += 1
        return self.session.generate_reply(instructions=instructions, tool_choice="none")

    async def _ask(self, question: str, options: list[tuple[str, str]], say: str) -> None:
        """Shows a choice card and asks the question."""
        card = await run_lk_tool(
            "show_choice",
            {"question": question, "options": [{"id": i, "name": n} for i, n in options]},
        )
        self.card = card
        if lk is not None:
            lk.on_choice(card, self._clicked)
        self._say(say)

    async def _answered(self, option: str) -> None:
        """Marks the open choice card with an answer given by voice."""
        if self.card:
            await run_lk_tool("choice_made", {"id": self.card, "option": option})
            self.card = ""

    async def _clicked(self, option: str) -> None:
        """Handles an answer clicked on the open choice card."""
        self.card = ""
        if self.step == "language" and option == "cancel":
            self._done("The user chose not to create an agent now. Nothing was created.")
        elif self.step == "language" and option in STARTERS:
            self._say("In one short sentence, tell the user you're creating their agent now.")
            await self._create(option)
        elif self.step == "try":
            await self._finish(option == "try")

    async def _create(self, language: str) -> None:
        self.step = "creating"
        self.language = language
        args = {"template": STARTERS[language], "in_current_directory": True, "confirmed": True}
        result = await run_lk_tool("create_project", args, timeout=600)
        if result.startswith("Error") or "only available" in result:
            self._done(f"Creating the project failed: {result}")
            return
        self.step = "try"
        await self._ask(
            "Talk to your new agent now?",
            [("try", "Talk to it"), ("later", "Not now")],
            "In one sentence, tell the user their agent is ready, and ask whether they want to "
            "talk to it now.",
        )

    async def _finish(self, yes: bool) -> None:
        if not yes:
            self._done("Created the project. The user didn't want to talk to it yet.")
            return
        self.step = "finished"
        # Say the whole hand-off before the agent starts: once it connects, it
        # takes over the audio, and anything still playing is cut off.
        await self._say(
            "In two short sentences, tell the user you're starting their agent, that "
            + STARTER_OPENINGS[self.language]
            + ", and that they can press B to come back to you."
        ).wait_for_playout()
        await run_lk_tool("try_project", {"confirmed": True})
        self.complete(STARTED)

    def _done(self, result: str) -> None:
        if self.step == "finished":
            return
        self.step = "finished"
        self.complete(result)


def own_session_code() -> str:
    """Returns this agent's AgentSession setup, from its own source."""
    try:
        source = open(__file__).read()
    except OSError:
        return ""
    # The last occurrence: the first is this function's own search string.
    start = source.rfind("    session = AgentSession(")
    end = source.find("\n    )\n", start)
    if start < 0 or end < 0:
        return ""
    lines = source[start : end + 6].splitlines()
    code = "\n".join(line[4:] if line.startswith("    ") else line for line in lines)
    # Show the model names themselves, not the constants.
    for name, value in (("STT_MODEL", STT_MODEL), ("LLM_MODEL", LLM_MODEL), ("TTS_MODEL", TTS_MODEL)):
        code = code.replace(f"model={name}", f'model="{value}"')
    # Leave out a setting that's only for comparing configurations.
    code = re.sub(r"\n\s*extra_kwargs=.*REASONING_EFFORT.*", "", code)
    return code


async def run_lk_tool(name: str, args: dict | None = None, timeout: float = 60) -> str:
    """Runs an lk tool through lk and returns its output for the LLM."""
    if lk is None:
        return "This tool is only available when the assistant runs inside lk."
    try:
        reply = await lk.request({"type": "lk_tool", "name": name, "args": args or {}}, timeout=timeout)
    except asyncio.TimeoutError:
        return "lk didn't respond in time."
    if not reply.get("ok"):
        return f"Error: {reply.get('error', 'unknown error')}"
    return reply.get("output") or "Done."


class LkChannel:
    """The local connection to lk, as JSON lines in both directions.

    The agent sends its transcript and requests to run lk tools. lk sends
    replies to requests, and can ask the agent to tell the user something.
    """

    def __init__(self, reader: asyncio.StreamReader, writer: asyncio.StreamWriter) -> None:
        self._reader = reader
        self._writer = writer
        self._pending: dict[str, asyncio.Future[dict]] = {}
        self._next_id = 0
        self._session: AgentSession | None = None
        self._read_task: asyncio.Task[None] | None = None
        self._choice_handlers: dict[str, Callable[[str], Awaitable[None]]] = {}

    def start(self, session: AgentSession) -> None:
        self._session = session
        self._read_task = asyncio.create_task(self._read())

    def send(self, msg: dict) -> None:
        if not self._writer.is_closing():
            self._writer.write((json.dumps(msg) + "\n").encode())

    def on_choice(self, card: str, handler: Callable[[str], Awaitable[None]]) -> None:
        """Calls handler with the option the user clicks on a choice card."""
        self._choice_handlers[card] = handler

    async def request(self, msg: dict, timeout: float = 10) -> dict:
        """Sends msg and waits for lk's reply."""
        self._next_id += 1
        msg_id = str(self._next_id)
        fut: asyncio.Future[dict] = asyncio.get_running_loop().create_future()
        self._pending[msg_id] = fut
        self.send({**msg, "id": msg_id})
        try:
            return await asyncio.wait_for(fut, timeout)
        finally:
            self._pending.pop(msg_id, None)

    async def _read(self) -> None:
        while line := await self._reader.readline():
            try:
                msg = json.loads(line)
            except json.JSONDecodeError:
                continue
            if msg.get("type") == "reply":
                fut = self._pending.get(msg.get("id", ""))
                if fut and not fut.done():
                    fut.set_result(msg)
            elif msg.get("type") == "choice":
                # The user clicked an option on a choice card.
                if handler := self._choice_handlers.pop(msg.get("id", ""), None):
                    asyncio.create_task(handler(msg.get("option", "")))
            elif msg.get("type") == "user_text" and self._session is not None:
                # The user typed instead of speaking: stop talking and answer it.
                # Speech that can't be interrupted, such as a running
                # confirmation, finishes first.
                try:
                    self._session.interrupt()
                except RuntimeError:
                    pass
                self._session.generate_reply(user_input=msg.get("text", ""))
            elif msg.get("type") == "say" and self._session is not None:
                # lk has news for the user.
                self._session.generate_reply(instructions=msg.get("instructions", ""))


# The connection to lk, when lk runs this agent.
lk: LkChannel | None = None


class TranscriptForwarder(io.TextOutput):
    """Sends the agent's transcript to lk as JSON lines, in time with its speech.

    Each line is {"type": "text", "text": "<new text>"} or {"type": "flush"} at the
    end of a segment. Expressive markup is removed.
    """

    def __init__(self, channel: LkChannel) -> None:
        super().__init__(label="lk-assistant", next_in_chain=None)
        self._channel = channel
        self._stripper = TranscriptMarkupStripper()

    async def capture_text(self, text: str) -> None:
        self._send_text(self._stripper.push(text))

    def flush(self) -> None:
        self._send_text(self._stripper.flush())
        self._send({"type": "flush"})
        self._stripper = TranscriptMarkupStripper()

    def _send_text(self, text: str) -> None:
        if text:
            self._send({"type": "text", "text": text})

    def _send(self, msg: dict) -> None:
        self._channel.send(msg)


async def connect_lk(session: AgentSession) -> None:
    """Connects to lk, if lk is running this agent, and streams the agent's
    spoken text to it.

    Console mode only reports what the agent said after it finishes speaking. It
    also syncs the text to the audio, but sends the result nowhere. This swaps in a
    synchronizer whose text goes to lk instead.
    """
    global lk
    addr = os.environ.get("LK_ASSISTANT_ADDR")
    if not addr:
        return
    host, port = addr.rsplit(":", 1)
    reader, writer = await asyncio.open_connection(host, int(port))
    lk = LkChannel(reader, writer)
    lk.start(session)
    # For lk's X-ray view of the pipeline.
    lk.send({"type": "pipeline", "stt": STT_MODEL, "llm": LLM_MODEL, "tts": TTS_MODEL, "turn_detection": "LiveKit turn detector, dynamic endpointing"})

    audio = session.output.audio
    if audio is None:
        return
    if audio.label == "TranscriptSynchronizer" and audio.next_in_chain is not None:
        audio = audio.next_in_chain
    sync = TranscriptSynchronizer(next_in_chain_audio=audio, next_in_chain_text=TranscriptForwarder(lk))
    session.output.audio = sync.audio_output
    session.output.transcription = sync.text_output


TURN_ANSWERS = {
    "leave": (
        "clearly asks to close LK or end the conversation now, such as \"exit\" or \"that's all, "
        "bye\". Apologies, fillers, fragments, and thanks in the middle of a conversation, such as "
        "\"Sorry,\" or \"Um\" or \"Thanks, and\", are not leaving, and neither are questions about "
        "closing or exiting something else, such as \"How do I exit the console?\""
    ),
    "code": "asks to see code, an example, a snippet, or how something looks in code",
    "other": "says anything else",
}


def watch_user_turns(session: AgentSession) -> None:
    """Acts on what each user turn asks for, when the model can't be relied on to.

    After each user turn, a short model call in the background reads the turn. It
    runs beside the reply, so it adds no delay.
    - If the user wants to leave, it tells lk to close once the agent is quiet,
      even if the model didn't call end_call.
    - If the user asked for code and the reply ends without a code card, it asks
      for one more reply that must call show_code.
    """
    log = logging.getLogger("lk-assistant")
    listening = asyncio.Event()  # set when a reply ends and the agent listens again

    @session.on("agent_state_changed")
    def _on_state(ev) -> None:
        if ev.new_state == "listening" and ev.old_state in ("thinking", "speaking"):
            listening.set()

    async def check(item, conversation: str) -> None:
        try:
            listening.clear()
            answer = await read_answer(session.llm, conversation, TURN_ANSWERS, item.text_content)
            if answer == "leave" and lk is not None:
                lk.send({"type": "end"})
            elif answer == "code":
                await asyncio.wait_for(listening.wait(), timeout=60)
                await show_code_if_missing(item)
        except asyncio.TimeoutError:
            pass
        except Exception:
            log.exception("couldn't check the user's turn")

    async def show_code_if_missing(item) -> None:
        if not isinstance(session.current_agent, Assistant):
            return  # a task, such as creating the user's agent, is running
        items = session.history.items
        after = [i for i in items[next((n for n, i in enumerate(items) if i.id == item.id), len(items)) + 1 :]]
        if any(i.type == "message" and i.role == "user" for i in after):
            return  # the user has moved on
        if any(i.type == "function_call" and i.name == "show_code" for i in after):
            return
        session.generate_reply(
            instructions=(
                "The user asked for code, and you haven't shown it. Call show_code now with a short "
                "example from the docs page you read, then say one short sentence about it."
            ),
            tool_choice={"type": "function", "function": {"name": "show_code"}},
        )

    @session.on("conversation_item_added")
    def _on_item(ev) -> None:
        item = ev.item
        if getattr(item, "role", None) != "user" or not item.text_content:
            return
        asyncio.create_task(check(item, recent_lines(session.history, item.id)))


server = AgentServer()


async def simulation_end(ctx) -> None:
    """Checks the tools a simulation called (see lk_simulation.py)."""
    from lk_simulation import check_simulation

    check_simulation(ctx)


@server.rtc_session(agent_name="lk-assistant", on_simulation_end=simulation_end)
async def assistant(ctx: agents.JobContext):
    # STT from the Voice AI quickstart
    # (docs.livekit.io/agents/start/voice-ai-quickstart), with two changes:
    # - LLM: gpt-5.4-mini instead of Gemma, for more reliable tool calling.
    #   The assistant must search the docs before it answers.
    # - TTS: Cartesia instead of Fish Audio, for inline phoneme overrides that
    #   make it say "LiveKit" correctly every time.
    session = AgentSession(
        stt=inference.STT(
            model=STT_MODEL,
            language="en",
            # Help the STT hear LiveKit's names and terms.
            extra_kwargs={"keyterms_prompt": STT_KEYTERMS},
        ),
        llm=inference.LLM(
            model=LLM_MODEL,
            extra_kwargs={"reasoning_effort": REASONING_EFFORT} if REASONING_EFFORT else {},
        ),
        tts=inference.TTS(
            model=TTS_MODEL,
            voice="a167e0f3-df7e-4d52-a9c3-f949145efdab",
            language="en",
        ),
        turn_handling=TurnHandlingOptions(
            turn_detection=inference.TurnDetector(),
            # Developers often pause mid-sentence to think. With the turn
            # detector, the shortest wait defaults to 0.3 seconds, which can
            # end a turn mid-thought. Dynamic endpointing learns the user's
            # pauses; the higher floor leaves room for a short one.
            endpointing={"mode": "dynamic", "min_delay": 0.8},
        ),
        # A reply after a docs lookup reads a lot of context. The default
        # 10-second timeout can cut it off.
        conn_options=SessionConnectOptions(llm_conn_options=APIConnectOptions(timeout=20.0)),
        # The LLM marks up emotion and pacing for the TTS. The markup is
        # stripped from transcripts.
        expressive=True,
        tts_text_transforms=[
            "filter_emoji",
            "filter_markdown",
            text_transforms.replace(PRONUNCIATIONS),
            text_transforms.replace(NAME_PRONUNCIATIONS, case_sensitive=True),
        ],
    )

    # Fetch the docs table of contents, or read it from the cache, off the
    # event loop; the instructions read it from the cache.
    await asyncio.to_thread(docs_toc)
    folder, coding_agents = load_folder(), load_coding_agents()
    if sim := ctx.simulation_context():
        # Agent Simulations (see simulations/README.md): the scenario sets the
        # folder, and lk's tools are mocked. The simulation module isn't
        # embedded in lk.
        from lk_simulation import setup_simulation

        folder, coding_agents = setup_simulation(sim, session, Assistant)

    watch_user_turns(session)
    await session.start(room=ctx.room, agent=Assistant(folder, coding_agents))
    await connect_lk(session)

    # A fixed greeting for what is in the folder. It starts without waiting on the LLM.
    session.say(greeting(folder, coding_agents))


if __name__ == "__main__":
    agents.cli.run_app(server)
