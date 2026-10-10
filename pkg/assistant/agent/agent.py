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
LLM_MODEL = "openai/gpt-5.4-mini"
TTS_MODEL = "cartesia/sonic-3"

GREETING = (
    "Hi, I'm LK, your LiveKit assistant. I can help you build a voice agent, add LiveKit to an "
    "app you already have, or explore what LiveKit can do, like realtime voice models, phone "
    "calls, and simulations for testing your agents. What would you like to do?"
)

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
}

# The assistant's name, LK, is said as the letters L and K. Matching is
# case-sensitive so that words like "walk" don't match.
NAME_PRONUNCIATIONS = {"LK": "<<ˈ|ɛ|l>> <<ˈ|k|eɪ>>"}

# The Docs MCP tools the agent uses.
DOCS_TOOLS = ("get_docs_overview", "docs_search", "get_pages", "code_search")

# What the agent says while it looks something up, if it hasn't said anything
# yet this turn. Docs lookups take a second or two.
LOOKUP_FILLERS = (
    "Let me check the docs.",
    "One moment, I'll look that up.",
    "Let me look that up in the docs.",
)

# Docs MCP results can be whole pages. Cap them to bound the LLM context, but
# high enough that a full docs page fits. The longest pages, such as
# /agents/build/turns/, are over 37,000 characters.
MAX_TOOL_RESULT_CHARS = 50000

INSTRUCTIONS = """You are LK, the LiveKit assistant, a voice assistant inside the LiveKit CLI.
You help developers build voice AI agents with LiveKit, add LiveKit to apps they already have, and
explore what LiveKit can do. You are yourself a LiveKit voice agent, built with the same Agents SDK
and LiveKit Inference the user can build with.

Match the user's level. If they seem new, describe outcomes before LiveKit terms, and explain a
term in a few words the first time you use it. If they seem experienced, be terse and precise, and
skip the basics. If they ask you to be briefer or to go deeper, do that for the rest of the
conversation.

You speak out loud, so:
- Keep replies short: one to three sentences. Offer to go deeper instead of covering everything.
- Use plain sentences. No markdown, lists, emojis, or symbols.
- Never read code, commands, or long identifiers aloud. Call show_code to show them,
  then say one short sentence about what you showed.

LiveKit changes often, and what you remember about it is out of date. For every question about
LiveKit features, APIs, SDKs, models, plugins, CLI commands, pricing, or configuration, call
docs_search first, then answer only from what the docs say. Do this even when you think you know
the answer. For questions about how something works or how to configure it, also read the most
relevant page with get_pages before you answer. When you're not sure where a topic lives, or the
user asks what LiveKit can do, call get_docs_overview to see how the docs are organized. If the
docs don't cover it, say so. Never guess an API.

You can't see or change the user's files. You help them plan and decide, and you set them up to
do the work:
- To learn about their LiveKit Cloud project and current directory, call get_project_info. To check
  on deployed agents, call list_cloud_agents.
- For a new project, start it from a template with create_project. If the user hasn't said which
  template, call list_templates and suggest the best fit: for a voice agent, agent-starter-python
  or agent-starter-node. Call get_project_info first: if the current directory is empty, ask
  whether to create the project right there or in a new folder. New projects already include
  LiveKit's skills and Docs MCP server for the user's coding agents.
- For an existing project, offer set_up_coding_agent, which installs LiveKit's skills and Docs MCP
  server into the user's coding agents (such as Claude Code, Cursor, or Codex).
- To add LiveKit to an existing app (get_project_info reports its framework), explain the three
  parts in plain words: the app gets a token from a small server endpoint, joins a LiveKit room,
  and the agent joins that room through dispatch. Then create the agent with create_project and
  give the frontend and token work to their coding agent with hand_off_task, based on the docs.
- After a project is created, offer to try it right here with try_project, so the user can talk
  to the agent they just made.
- If the user asks how you work, or what they'll be building, call explain_yourself.
- When the user is ready to write code, call hand_off_task with a prompt for their coding agent:
  what to build or change, plus what you learned from the docs, such as package names, APIs, and
  CLI commands.

create_project and set_up_coding_agent don't run right away. They show the command to the user and
return a confirmation ID. Ask the user in one short sentence whether to go ahead. They can answer
by voice or on screen. If they agree by voice, call confirm_action with the ID and approved=true;
if they decline, approved=false. Never call confirm_action unless the user has just agreed.

When the user says they're done or says goodbye, call end_call.

Prefer the LiveKit CLI for project tasks. For example, "lk agent init" creates an agent from a
starter template, "lk agent dev" runs it, and "lk agent create" deploys it to LiveKit Cloud."""


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
    def __init__(self) -> None:
        instructions = INSTRUCTIONS
        if project := os.environ.get("LK_ASSISTANT_PROJECT"):
            instructions += f"\n\nThe user's current directory is {project}."
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
        return "The code is on the user's screen. They can copy it with the Copy button."

    @function_tool()
    async def hand_off_task(self, context: RunContext, task: str) -> str:
        """Show the user a prompt to paste into their coding agent, with a button to copy it.

        Args:
            task: A complete, self-contained prompt: what to build or change in the user's
                project, plus details from the LiveKit docs such as package names, APIs, and CLI
                commands.
        """
        # `lk assistant` reads this call from the session events and shows it.
        return "The prompt is on the user's screen. They can copy it into their coding agent."

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
        self, context: RunContext, template: str, name: str = "", in_current_directory: bool = False
    ) -> str:
        """Propose creating a project from a LiveKit template, with lk agent init for agent
        templates or lk app create for frontends, including installing dependencies. It doesn't run
        until the user confirms (see confirm_action). Afterward, tell them how to run it: for an
        agent, lk agent dev in the project folder.

        Args:
            template: The template name, from list_templates, such as agent-starter-python.
            name: The name for the new folder: lowercase letters, numbers, and dashes. Not used
                with in_current_directory.
            in_current_directory: Create the project in the current directory itself, named after
                it, instead of in a new folder. Only works if the directory is empty, and only if
                the user wants it.
        """
        return await run_lk_tool(
            "create_project",
            {"template": template, "name": name, "in_current_directory": in_current_directory},
        )

    @function_tool()
    async def set_up_coding_agent(self, context: RunContext, all_projects: bool = False) -> str:
        """Propose installing LiveKit's agent skills and the LiveKit Docs MCP server into the
        user's coding agents (such as Claude Code, Cursor, and Codex), with lk skills install. Use
        it for existing projects; new projects from create_project already have them. It doesn't
        run until the user confirms (see confirm_action).

        Args:
            all_projects: Install for all of the user's projects instead of only the current one.
        """
        return await run_lk_tool("set_up_coding_agent", {"global": all_projects})

    @function_tool()
    async def try_project(self, context: RunContext, path: str = "") -> str:
        """Propose running the user's agent in this window with lk agent console, so they can talk
        to it. The conversation switches to their agent until they press B to come back to you.
        It doesn't run until the user confirms (see confirm_action).

        Args:
            path: The project folder, relative to the user's current directory. Leave it empty
                for the project created in this session, or the current directory.
        """
        return await run_lk_tool("try_project", {"path": path})

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
    """Says a short line when a docs lookup starts, so the user doesn't sit in
    silence. Skipped if the agent already spoke this turn."""
    spoke = False

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

    @session.on("tool_execution_updated")
    def _on_tool(ev) -> None:
        nonlocal spoke
        update = ev.update
        if update.type != "tool_call_started" or update.function_call.name not in DOCS_TOOLS or spoke:
            return
        spoke = True
        session.say(random.choice(LOOKUP_FILLERS), add_to_chat_ctx=False)


server = AgentServer()


@server.rtc_session(agent_name="lk-assistant")
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

    speak_during_lookups(session)
    await session.start(room=ctx.room, agent=Assistant())
    await connect_lk(session)

    # A fixed greeting: the same every time, and it starts without waiting on the LLM.
    session.say(GREETING)


if __name__ == "__main__":
    agents.cli.run_app(server)
