"""The LiveKit assistant: a voice agent that helps developers build with LiveKit.

`lk assistant` runs this agent in console mode and shows the conversation in
its overlay. Code the agent shows with `show_code` appears as a card in the
overlay, with a button to copy it.
"""

import asyncio
import json
import os
import random

from livekit import agents
from livekit.agents import (
    Agent,
    AgentServer,
    AgentSession,
    RunContext,
    TurnHandlingOptions,
    function_tool,
    inference,
    mcp,
    text_transforms,
)
from livekit.agents.beta.tools import EndCallTool
from livekit.agents.voice import TranscriptSynchronizer, io

# Private module: the SDK's own stripper for expressive markup, the same one the
# room transcription output uses. Check it when upgrading livekit-agents.
from livekit.agents.tts._provider_format import TranscriptMarkupStripper

DOCS_MCP_URL = "https://docs.livekit.io/mcp"

STT_MODEL = "assemblyai/universal-3-6-pro"
TTS_MODEL = "cartesia/sonic-3"

# Settings to compare in simulations (see simulations/README.md). The defaults
# are what lk ships.
LLM_MODEL = os.environ.get("LK_ASSISTANT_LLM", "openai/gpt-5.4-mini")
# Whether the instructions include PRIMER, so basic questions get an answer
# without a docs lookup.
USE_PRIMER = os.environ.get("LK_ASSISTANT_PRIMER", "1") == "1"
# When to say a short line during a docs lookup: "always", "slow" (only when
# the lookup takes a while, at most once per user turn), or "off".
FILLER = os.environ.get("LK_ASSISTANT_FILLER", "slow")

INTRO = "Hi, I'm LK, your LiveKit assistant."

# The greeting for a folder lk doesn't recognize as a project.
GREETING = f"{INTRO} Want to build your first voice agent, or ask me about LiveKit?"


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
        return f"{INTRO} Want to get a voice agent up and running here, or ask me about LiveKit?"
    if kind == "livekit":
        if coding_agents.get("ready"):
            return f"{INTRO} Ask me anything about LiveKit, or we can start a new agent in another folder."
        return f"{INTRO} Want me to set up your coding agent for LiveKit, or do you have a question?"
    if kind == "app":
        return f"{INTRO} Want to add a voice agent to this project, or ask me about LiveKit first?"
    return GREETING


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
DOCS_TOOLS = ("get_docs_overview", "docs_search", "get_pages", "code_search")

# What the agent says while it looks something up, if it hasn't said anything
# yet this turn. Docs lookups take a second or two.
LOOKUP_FILLERS = (
    "Let me check the docs.",
    "One moment, I'll look that up.",
    "Let me look that up in the docs.",
)

# With FILLER "slow", how long a lookup runs before the agent says a line.
SLOW_LOOKUP_SECONDS = 2.5

# Docs MCP results can be whole pages. Cap them to bound the LLM context, but
# high enough that a full docs page fits. The longest pages, such as
# /agents/build/turns/, are over 37,000 characters.
MAX_TOOL_RESULT_CHARS = 50000

INSTRUCTIONS = """You are LK, the LiveKit assistant, a voice assistant inside the LiveKit CLI.
You help developers get started with LiveKit: build their first voice agent, talk to it, set up
their coding agent for LiveKit, and understand the basics they need. You are yourself a LiveKit
voice agent, built with the same Agents SDK and LiveKit Inference the user can build with.

Be generous with what you know. Your job is to help the user build a voice agent that works well,
so share the knowledge that makes the difference: why LiveKit works the way it does, the tradeoff
behind a choice, and what to watch for, such as latency, turn-taking, interruptions, and testing.
Give context where it helps the user understand, not only the answer to the literal question.
Generous means the context that matters for this user, not every detail: for someone who isn't
technical, skip implementation details unless they ask.
Explain concretely. Walk through what happens, step by step, in plain words, such as what happens
when a user starts talking to an agent, rather than abstractions such as "realtime bus" or
"orchestration". For a big question with several parts, such as how LiveKit fits together, call
show_answer with the parts, and speak a short version of the most important idea.

Getting started is your job. Ongoing work on a project belongs in the user's coding agent, which
can read and change their code. You can't see or change the user's files, so when the user asks you
to fix, change, or review their code, don't suggest a fix yourself: call hand_off_task with a prompt
for their coding agent. Since you haven't seen their code, the prompt describes the problem, how
to reproduce it, and how to check that it's fixed, plus anything relevant from the docs. It doesn't
prescribe a fix; the coding agent finds the cause.

Match the user's level. If they seem new, describe outcomes before LiveKit terms, and explain a
term in a few words the first time you use it. If they seem experienced, be terse and precise, and
skip the basics. If they ask you to be briefer or to go deeper, do that for the rest of the
conversation. Be plain and calm: no slang, no exclamations, no flattery.

Don't repeat yourself. Don't ask a question the user has already answered, and don't ask twice
before doing something. Don't announce that you're looking something up; just look it up. Don't
end replies with an offer such as "If you want, I can" or "I can sketch that next"; the user
knows they can ask. When the next step is clear, ask in a few
words, such as "Want to try it?", or just do it. When the user's request is clear, do it; don't
offer choices they didn't ask for. Don't restate what's already on screen.

Say only what's true. Tell the user something worked only after a tool result says so. Name
commands exactly as the tool result or the card on screen shows them. Don't say you can't do
something and then do it. Don't promise anything you can't show, such as a diagram or a file.

When you talk about the STT-LLM-TTS pipeline, say once that it's a cascaded pipeline, three models
in sequence: speech-to-text, an LLM, and text-to-speech. After that, call it the cascaded pipeline.

Speak in the style of Simplified Technical English (ASD-STE100): short sentences, at most about 20
words, with one idea each. Use active voice, simple tenses, and common words with one clear
meaning. Don't use idioms or figures of speech. Use the exact technical term when there is one,
such as "dispatch" or "turn detector", and explain it in a few words the first time. Deviate from
this style only when it would make a concept less accurate.

You speak out loud, so:
- Fit the length to the moment. When you confirm an action or answer a yes-or-no question, one
  sentence is enough. When you explain something, take two to five sentences: the answer, why,
  and the one thing to watch for. Don't cover everything at once; the user can ask for more.
- Use plain sentences. No markdown, lists, emojis, or symbols, with one exception: put a short
  command, package, file, function, or class name in backticks, such as `lk agent dev` or
  `AgentSession`. On screen it shows in monospace. The backticks aren't spoken.
- Never read code, commands, or long identifiers aloud. Call show_code to show them,
  then say one short sentence about what you showed.
- If the user asks you to copy something on screen, call copy_to_clipboard. Don't copy unless
  they ask.

LiveKit changes often, and what you remember about it is out of date. For every question about
LiveKit features, APIs, SDKs, models, plugins, CLI commands, pricing, or configuration, call
docs_search first, then answer only from what the docs say. Do this even when you think you know
the answer. For questions about how something works or how to configure it, also read the most
relevant page with get_pages before you answer. When you're not sure where a topic lives, or the
user asks what LiveKit can do, call get_docs_overview to see how the docs are organized. If the
docs don't cover it, say so. Never guess an API.

Show docs answers on screen, not only in speech. When an answer has more than one point, call
show_answer with a few short points and the docs page they come from, then say one sentence that
sums it up. For a one-sentence answer, just say it, and call show_link with the page it comes
from when the user would want to read more. Use page titles and URLs exactly as your docs search
returned them, and never read a URL aloud.

How you help someone get started:
- Call get_project_info when you need to know about their LiveKit Cloud project or their current
  directory.
- For a new voice agent, start it from a template with create_project. Before you create it, ask
  one short question: Python or Node.js, unless the user already said. Their answer is their
  go-ahead, so then call create_project with confirmed=true, without asking again. Use
  agent-starter-python or agent-starter-node, or call list_templates if they want something else.
  If the current directory is empty, create it right there. New projects already include
  LiveKit's skills and Docs MCP server for the user's coding agents.
- After you create a project, offer to try it right here with try_project, so the user can talk
  to the agent they just made. While their agent runs, the user talks to it, not to you, and you
  hear nothing. You are never their agent: don't speak as it, and don't say it's running until lk
  tells you. You can't stop it; the user presses B.
- When they want to change their new agent, such as its instructions, voice, or tools, act as
  the expert. Look up how in the docs, such as which TTS voices LiveKit Inference offers. If the
  choice is the user's, such as which voice, give them one or two concrete options and ask in a
  few words. Then call hand_off_task right away, without asking whether to. Write the prompt like
  a senior engineer's ticket, addressed to the coding agent: the exact change, with the API,
  model, and values from the docs, and how to check that it works. Pick one option rather than
  listing alternatives. Keep it short. No hedges such as "if the project uses", and not a prompt
  about writing a prompt.
- To add LiveKit to an app they already have, explain the three parts in plain words: the app
  gets a token from a small server endpoint, joins a LiveKit room, and the agent joins that room
  through dispatch. Then create the agent with create_project in a new folder, and give the
  frontend and token work to their coding agent with hand_off_task, based on the docs.
- If their coding agent isn't set up for LiveKit, offer set_up_coding_agent, which installs
  LiveKit's skills and Docs MCP server into it (such as Claude Code, Cursor, or Codex).
- If the folder already has a LiveKit project, the user is past getting started. Point them to
  their coding agent for work on it, and help with what you can: setting up their coding agent,
  quick questions, or a new agent in a new folder.
- If the user asks how you work, or what they'll be building, call explain_yourself.

When the user asks for something outside that, don't pretend and don't improvise. Say in one short
sentence that you can't do it here, then give them the next best thing:
- A command they can run themselves, with show_code. For example, to run an agent they already
  had, try_project tells you the command. To deploy, "lk agent create" the first time and "lk
  agent deploy" after that. To see logs, "lk agent logs". Check the docs for the exact command.
- A prompt for their coding agent, with hand_off_task, for anything that reads or changes their
  code: fixing a bug, adding a feature, or reviewing their agent.
- The docs page that covers it, with show_link, for anything else about LiveKit.
For something unrelated to LiveKit, say it's outside what you help with, and offer what you can.

create_project, set_up_coding_agent, and try_project show the command on screen and wait for the
user to confirm. If the user's last message asked for that action or agreed to it, such as "set
up a voice agent here" in an empty folder, or "yes" to your offer, pass confirmed=true so it runs
right away, and don't ask again. Otherwise, the tool returns a
confirmation ID: ask the user in one short sentence whether to go ahead. They can answer by voice
or on screen. If they agree by voice, call confirm_action with the ID and approved=true; if they
decline, approved=false. Never confirm anything the user hasn't agreed to.

If the user asks what you cost: you run on LiveKit Inference through their LiveKit Cloud project,
so your usage counts toward their plan.

When the user wants to stop, such as "exit", "close", "quit", "bye", or "that's all", call
end_call. Saying goodbye without calling end_call leaves the window open.

Prefer the LiveKit CLI for project tasks. "lk agent init" creates an agent from a starter
template, "lk agent console" talks to it in the terminal, "lk agent dev" runs it with hot reload,
and "lk agent create" deploys it to LiveKit Cloud."""

# What LK knows without a lookup, checked against the docs (the Docs overview,
# Pipeline types, Agent dispatch, Turns overview, Models overview, Voice AI
# quickstart, Telephony introduction, Tokens & grants, and Testing pages).
PRIMER = """What you know about LiveKit, checked against the docs:
- LiveKit is an open-source platform for building voice, video, and physical AI agents, on
  realtime WebRTC infrastructure. LiveKit Cloud is the hosted offering. It includes agent
  deployment, model inference, and realtime media transport.
- Every client, agent, and phone call joins a room as a participant, and publishes or subscribes
  to audio, video, and data tracks. Frontends connect with an access token: a JWT, signed with the
  project's API secret, that names the participant, the room, and what it may do.
- An agent server runs the agent code. Dispatch assigns an agent to a room. Explicit dispatch, by
  the agent's name, is recommended for most apps. AgentSession runs the conversation, and the Agent
  holds the instructions and tools.
- What happens in a conversation: the user's app joins a room with a token, and LiveKit
  dispatches the agent to the same room. The agent's AgentSession receives the user's audio. STT
  turns it into text, and the turn detector decides when the user has finished. The LLM writes a
  reply and can call tools. TTS speaks the reply into the room. The user can interrupt at any time,
  and the agent stops and listens. The agent code is mostly instructions and tools; LiveKit handles
  the realtime audio.
- The STT-LLM-TTS pipeline, also called the cascaded pipeline, is the right default for most
  production agents: each model can be swapped, every stage can be inspected, tool calling is
  mature, and say() speaks exact text. It has more end-to-end latency than a realtime model. A
  realtime model is one speech-to-speech model: fastest, and it hears tone of voice, but its tool
  calling is less mature and its transcripts are delayed. A half-cascade pairs a realtime model for
  input with a separate TTS for output.
- Turn detection decides when the user has finished speaking. LiveKit's turn detector model is the
  default and is recommended for most agents. It predicts the end of a turn from the meaning and
  sound of speech, on top of VAD. Endpointing delay and adaptive interruption handling tune it.
- LiveKit Inference gives agents models from OpenAI, Google, AssemblyAI, Deepgram, Cartesia, and
  others through LiveKit Cloud, with no extra API keys or plugins, and zero data retention by
  default. Plugins connect directly to more providers, with the user's own API keys.
- Agents can take and make phone calls. LiveKit Phone Numbers sells US local and toll-free numbers
  in LiveKit Cloud, or the user can connect their own SIP provider with trunks. Dispatch rules route
  inbound calls to rooms.
- The starter templates, agent-starter-python and agent-starter-node, include a working agent,
  tests, and an AGENTS.md for coding agents. Python needs 3.10 or later and uv. Node.js needs 20 or
  later and pnpm. "lk agent init" creates a project, "lk agent console" talks to it in the terminal,
  "lk agent dev" runs it with hot reload, and "lk agent create" deploys it to LiveKit Cloud, which
  scales it automatically.
- Agent Builder builds agents in the browser without code, for prototyping.
- To test agents: Agent Console to try one yourself, unit tests on pytest or Vitest, and Agent
  Simulations, where an LLM plays the user and a judge grades whole conversations, in text or audio
  mode.

Answer basic questions about these from what you know, without a lookup, the way an experienced
LiveKit engineer would: give a clear recommendation and the reason for it, and mention the one
detail that matters most. Use LiveKit's names for things, such as LiveKit's turn detector model,
LiveKit Phone Numbers, LiveKit Inference, or Agent Simulations, so the user knows what to look
for, and explain each in a few words. Look up anything more specific in the docs: APIs, parameters, model
names, versions, prices, limits, and step-by-step setup."""

# With the primer, basic questions skip the lookup.
DOCS_RULE = """For every question about
LiveKit features, APIs, SDKs, models, plugins, CLI commands, pricing, or configuration, call
docs_search first, then answer only from what the docs say. Do this even when you think you know
the answer."""
PRIMER_DOCS_RULE = """For basic questions that what
you know about LiveKit covers, answer directly. For anything more specific, such as APIs,
parameters, model names, versions, prices, limits, or setup steps, call docs_search first, then
answer only from what the docs say."""


def build_instructions() -> str:
    if not USE_PRIMER:
        return INSTRUCTIONS
    return INSTRUCTIONS.replace(DOCS_RULE, PRIMER_DOCS_RULE) + "\n\n" + PRIMER



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
                        allowed_tools=list(DOCS_TOOLS),
                        tool_result_resolver=truncate_result,
                    ),
                ),
                # lk closes its overlay when it sees this tool called. Console
                # mode has no real room, so there's nothing to delete.
                *EndCallTool(
                    extra_description="Use this when the user says they're done, says goodbye, or asks to close the assistant.",
                    delete_room=False,
                    end_instructions="Say a short, friendly goodbye in one sentence.",
                ).tools,
            ],
        )

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
    async def show_answer(
        self,
        context: RunContext,
        title: str,
        points: list[str],
        source_title: str,
        source_url: str,
    ) -> str:
        """Show a docs answer on screen as a few short points, with the docs page they come
        from. The user can read it, copy it, and open the page. After calling it, say one
        sentence that sums it up; don't read the points aloud.

        Args:
            title: What the answer is about, such as "Turn detection".
            points: Two to five short points, one sentence each. Put commands and names in
                backticks.
            source_title: The title of the docs page the points come from.
            source_url: The page's full URL, exactly as your docs search or get_pages returned
                it. It must start with https://docs.livekit.io/.
        """
        # `lk assistant` reads this call from the session events and shows it.
        if not source_url.startswith(DOCS_URL):
            return f"Not shown: the source must be a docs page under {DOCS_URL}, from your docs searches."
        return (
            "The answer is on the user's screen. Unless you already said it in this turn, say one "
            "sentence that sums it up. Don't leave the user without a spoken answer."
        )

    @function_tool()
    async def show_link(self, context: RunContext, title: str, url: str) -> str:
        """Show a link to a LiveKit docs page where the user can read more. The user can open
        it in their browser or copy it.

        Args:
            title: The page title, such as "LiveKit turn detector".
            url: The page's full URL, exactly as your docs search or get_pages returned it. It
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

        Args:
            task: A complete, self-contained prompt: what to build or change in the user's
                project, plus details from the LiveKit docs such as package names, APIs, and CLI
                commands.
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
    async def create_project(
        self,
        context: RunContext,
        template: str,
        name: str = "",
        in_current_directory: bool = False,
        confirmed: bool = False,
    ) -> str:
        """Create a project from a LiveKit template, with lk agent init for agent templates or lk
        app create for frontends, including installing dependencies. Unless confirmed is true, it
        shows the command and waits for the user to confirm (see confirm_action). Afterward, offer
        to try the new agent with try_project.

        Args:
            template: The template name, such as agent-starter-python.
            name: The name for the new folder: lowercase letters, numbers, and dashes. Not used
                with in_current_directory.
            in_current_directory: Create the project in the current directory itself, named after
                it, instead of in a new folder. Only works if the directory is empty.
            confirmed: True if the user's last message asked for this or agreed to it, such
                as "set up a voice agent here" or "yes" after you offered it. Then it runs
                right away, without asking again. Otherwise leave it false, and ask.
        """
        return await run_lk_tool(
            "create_project",
            {
                "template": template,
                "name": name,
                "in_current_directory": in_current_directory,
                "confirmed": confirmed,
            },
        )

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
        create_project already have them. If they're already set up, it says so and installs
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
        """Show the user how you're built: your own AgentSession code, on screen. Use it when they
        ask how you work, or to show what a LiveKit voice agent looks like."""
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
        """Run or cancel a command that create_project or set_up_coding_agent proposed, after the
        user answers by voice. Only call it right after the user agrees or declines.

        Args:
            confirmation_id: The ID the proposing tool returned, such as confirm1. Works for
                create_project, set_up_coding_agent, and try_project.
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

    def start(self, session: AgentSession) -> None:
        self._session = session
        self._read_task = asyncio.create_task(self._read())

    def send(self, msg: dict) -> None:
        if not self._writer.is_closing():
            self._writer.write((json.dumps(msg) + "\n").encode())

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


def speak_during_lookups(session: AgentSession) -> None:
    """Says a short line during a docs lookup, so the user doesn't sit in
    silence. Skipped if the agent already spoke this turn. With FILLER "slow",
    it speaks only if a lookup is still running after SLOW_LOOKUP_SECONDS, so
    the line never comes after the lookup, right before the answer."""
    if FILLER == "off":
        return
    spoke = False
    running: set[str] = set()  # call IDs of docs lookups in progress
    pending: asyncio.Task | None = None

    def cancel() -> None:
        nonlocal pending
        if pending is not None:
            pending.cancel()
            pending = None

    @session.on("conversation_item_added")
    def _on_item(ev) -> None:
        nonlocal spoke
        if getattr(ev.item, "role", None) == "user":
            spoke = False

    @session.on("agent_state_changed")
    def _on_state(ev) -> None:
        nonlocal spoke
        if ev.new_state == "speaking":
            spoke = True
            cancel()

    async def say_if_slow() -> None:
        await asyncio.sleep(SLOW_LOOKUP_SECONDS)
        if running and not spoke:
            say()

    def say() -> None:
        nonlocal spoke
        spoke = True
        session.say(random.choice(LOOKUP_FILLERS), add_to_chat_ctx=False)

    @session.on("tool_execution_updated")
    def _on_tool(ev) -> None:
        nonlocal pending
        update = ev.update
        if update.type == "tool_call_ended":
            running.discard(update.call_id)
            if not running:
                cancel()
            return
        if update.type != "tool_call_started" or update.function_call.name not in DOCS_TOOLS:
            return
        running.add(update.function_call.call_id)
        if spoke:
            return
        if FILLER == "slow":
            if pending is None or pending.done():
                pending = asyncio.create_task(say_if_slow())
            return
        say()


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
        stt=inference.STT(model=STT_MODEL, language="en"),
        llm=inference.LLM(model=LLM_MODEL),
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

    folder, coding_agents = load_folder(), load_coding_agents()
    if sim := ctx.simulation_context():
        # Agent Simulations (see simulations/README.md): the scenario sets the
        # folder, and lk's tools are mocked. The simulation module isn't
        # embedded in lk.
        from lk_simulation import setup_simulation

        folder, coding_agents = setup_simulation(sim, session, Assistant)

    speak_during_lookups(session)
    await session.start(room=ctx.room, agent=Assistant(folder, coding_agents))
    await connect_lk(session)

    # A fixed greeting for what is in the folder. It starts without waiting on the LLM.
    session.say(greeting(folder, coding_agents))


if __name__ == "__main__":
    agents.cli.run_app(server)
