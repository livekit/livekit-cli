"""Minimal one-file echo agent for the `lk agent debugger` e2e test.

Has the full voice pipeline (STT, LLM, TTS) through LiveKit Inference, so the
test can drive it in both text and audio mode. Echoes what the user says, which
the test asserts on.
"""

from dotenv import load_dotenv
from livekit.agents import Agent, AgentServer, AgentSession, JobContext, cli, inference

load_dotenv()

server = AgentServer()


@server.rtc_session()
async def entrypoint(ctx: JobContext):
    session = AgentSession(
        stt=inference.STT(model="deepgram/nova-3"),
        llm=inference.LLM(model="openai/gpt-4o-mini"),
        tts=inference.TTS(model="cartesia/sonic-3"),
    )
    await session.start(
        agent=Agent(
            instructions=(
                "You are an echo bot. Reply with exactly what the user says, "
                "verbatim, and nothing else."
            ),
        ),
        room=ctx.room,
    )
    await ctx.connect()


if __name__ == "__main__":
    cli.run_app(server)
