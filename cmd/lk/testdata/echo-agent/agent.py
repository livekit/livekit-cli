"""Minimal one-file echo agent for the `lk agent daemon` e2e test.

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
    cli.run_app(server)
