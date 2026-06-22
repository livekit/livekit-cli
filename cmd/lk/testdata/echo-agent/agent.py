"""Minimal one-file echo agent for the `lk agent session` e2e test.

Driven in text mode, so an LLM is the only component needed. Echoes the user's
text verbatim, which the test asserts on.
"""

from dotenv import load_dotenv
from livekit.agents import Agent, AgentServer, AgentSession, JobContext, cli, inference

load_dotenv()

server = AgentServer()


@server.rtc_session()
async def entrypoint(ctx: JobContext):
    session = AgentSession(llm=inference.LLM(model="openai/gpt-4o-mini"))
    await session.start(
        agent=Agent(
            instructions=(
                "You are an echo bot. Reply with exactly the text the user "
                "sends, verbatim, and nothing else."
            ),
        ),
        room=ctx.room,
    )
    # No TTS, so disable audio output or the turn crashes in tts_node.
    session.output.set_audio_enabled(False)
    await ctx.connect()


if __name__ == "__main__":
    import sys

    argv = sys.argv[1:]
    if argv and argv[0] == "console":
        # The daemon launches `python agent.py console --connect-addr <addr>`, but
        # cli.run_app() sends `console` to the legacy click CLI (no --connect-addr),
        # so dispatch to the TCP console directly.
        from livekit.agents.cli.cli import _run_tcp_console

        _run_tcp_console(
            server=server,
            connect_addr=argv[argv.index("--connect-addr") + 1],
            record="--record" in argv,
        )
    else:
        cli.run_app(server)
