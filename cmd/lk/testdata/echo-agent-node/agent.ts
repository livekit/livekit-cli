/**
 * Minimal one-file echo agent for the `lk agent debugger` e2e test -- the Node
 * sibling of testdata/echo-agent/agent.py.
 *
 * Has the full voice pipeline (STT, LLM, TTS) through LiveKit Inference, so
 * the test can drive it in both text and audio mode. Echoes what the user
 * says, which the test asserts on.
 */
import { type JobContext, ServerOptions, cli, defineAgent, inference, voice } from '@livekit/agents';
import 'dotenv/config';
import { fileURLToPath } from 'node:url';

export default defineAgent({
  entry: async (ctx: JobContext) => {
    const session = new voice.AgentSession({
      stt: new inference.STT({ model: 'deepgram/nova-3' }),
      llm: new inference.LLM({ model: 'openai/gpt-4o-mini' }),
      tts: new inference.TTS({ model: 'cartesia/sonic-3' }),
    });
    await session.start({
      agent: new voice.Agent({
        instructions:
          'You are an echo bot. Reply with exactly what the user says, verbatim, and nothing else.',
      }),
      room: ctx.room,
    });
    await ctx.connect();
  },
});

cli.runApp(new ServerOptions({ agent: fileURLToPath(import.meta.url) }));
