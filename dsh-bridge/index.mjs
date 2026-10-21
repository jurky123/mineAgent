import { createUserMessage, createAssistantMessage, createSystemMessage } from '@deepseek-ai/dsh-llm';
import { admitEncodedImages } from '@deepseek-ai/dsh-attachment';
import { startBridge } from './server.mjs';

export const name = 'mineagent-dsh-bridge';
export const inject = ['agents', 'agentPresets', 'llm', 'attachments'];

export async function apply(ctx, config) {
  const bridge = await startBridge(ctx, config, { createUserMessage, createAssistantMessage, createSystemMessage, admitEncodedImages });
  ctx.on('dispose', () => bridge.close());
}
