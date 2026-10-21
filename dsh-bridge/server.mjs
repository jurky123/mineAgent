import { createServer } from 'node:http';
import { timingSafeEqual, randomUUID } from 'node:crypto';
import { readFile, stat } from 'node:fs/promises';
import { isAbsolute } from 'node:path';

const MAX_BYTES = 64 * 1024 * 1024;
const RUN_ID = /^[a-f0-9]{32}$/;
const CALL_ID = /^[a-zA-Z0-9_-]{1,128}$/;
const PRESET = 'mineagent-bridge';

function publicErrorCode(code) {
  return ['AUTHENTICATION_ERROR','RATE_LIMITED','CONTEXT_WINDOW_EXCEEDED','UNSUPPORTED_REASONING_EFFORT','UNSUPPORTED_CONTENT','MODEL_NOT_FOUND','INVALID_REQUEST','UNKNOWN_TOOL','API_ERROR'].includes(code) ? code : 'UNKNOWN';
}
function fail(status) { const e = new Error('bridge request rejected'); e.status = status; throw e; }
async function body(req) {
  if (!req.headers['content-type']?.startsWith('application/json')) fail(415);
  let size = 0; const chunks = [];
  for await (const c of req) { size += c.length; if (size > MAX_BYTES) fail(413); chunks.push(c); }
  try { return JSON.parse(Buffer.concat(chunks).toString('utf8')); } catch { fail(400); }
}
function json(res, status, data = {}) {
  res.writeHead(status, { 'Content-Type': 'application/json', 'Cache-Control': 'no-store' }); res.end(JSON.stringify(data));
}
function validate(input) {
  if (!input || !RUN_ID.test(input.id) || typeof input.instruction !== 'string' || !input.instruction ||
      typeof input.provider !== 'string' || !input.provider || typeof input.model !== 'string' || !input.model ||
      !['off','none','minimal','low','medium','high','xhigh','max'].includes(input.reasoningEffort) ||
      !Array.isArray(input.messages) || !input.messages.length || input.messages.length > 1000 ||
      !Array.isArray(input.tools) || input.tools.length > 256 ||
      (input.maxIterations !== undefined && (!Number.isSafeInteger(input.maxIterations) || input.maxIterations < 0))) fail(400);
  const names = new Set();
  for (const t of input.tools) {
    if (!t || !/^[a-zA-Z0-9_-]{1,128}$/.test(t.name) || names.has(t.name) || typeof t.description !== 'string' || t.parameters?.type !== 'object') fail(400);
    names.add(t.name);
  }
  for (const m of input.messages) {
    if (!['user','assistant'].includes(m.role) || !Array.isArray(m.content) || !m.content.length) fail(400);
    for (const b of m.content) {
      if (b.type === 'text' && typeof b.text === 'string') continue;
      if (m.role === 'user' && b.type === 'image' && typeof b.data === 'string' && ['image/png','image/jpeg','image/webp','image/gif'].includes(b.mimeType)) continue;
      fail(400);
    }
  }
  if (input.messages.at(-1).role !== 'user') fail(400);
}

// Each task gets a new DSH agent seeded with MineAgent's authoritative history.
// This never resumes an unrelated native DSH Web session or installs its tools.
async function durableMessages(ctx, input, deps) {
  const messages = [];
  for (const m of input.messages) {
    const images = m.content.filter(b => b.type === 'image');
    const refs = images.length ? await deps.admitEncodedImages(ctx.attachments, images.map(b => ({ data: b.data, mediaType: b.mimeType }))) : [];
    let next = 0;
    const content = m.content.map(b => b.type === 'text' ? { type:'text', text:b.text } : { type:'image', attachment:refs[next++] });
    messages.push(m.role === 'user' ? deps.createUserMessage({ content, source:{ kind:'user' } }) :
      deps.createAssistantMessage({ content, source:{ provider:input.provider, model:input.model } }));
  }
  return messages;
}

function historySeed(messages, instruction, deps) {
  const events = [];
  const push = (type, data, surface = false) => events.push({ type, seq:events.length, time:Date.now(), data, ...(surface ? {surfaceOp:'append'} : {}) });
  // A standalone closed prefix preserves actual roles instead of elevating old
  // user input into the system prompt. The live turn only receives the last user.
  let turn = 0, step = 1, open = false, previousRole;
  for (const message of messages.slice(0,-1)) {
    if (open && message.role === 'user' && previousRole === 'assistant') {
      push('step/end', {turn,step});push('turn/end', {turn,reason:{kind:'completed'}});open=false;
    }
    if (!open) {
      turn++;step=1;open=true;
      push('turn/start', {turn});push('step/start', {turn,step});
      if (turn === 1) push('system/message', {turn,step,message:deps.createSystemMessage(instruction)},true);
    }
    if (message.role === 'user') push('user/message',message,true);
    else push('assistant/message',{turn,step,message,stream:[]},true);
    previousRole=message.role;
  }
  if (open) {push('step/end',{turn,step});push('turn/end',{turn,reason:{kind:'completed'}});}

  return events;
}

export async function startBridge(ctx, config, deps) {
  if (!config || !['127.0.0.1','::1'].includes(config.host ?? '127.0.0.1') ||
      !Number.isSafeInteger(config.port) || config.port < 1 || config.port > 65535 ||
      !isAbsolute(config.cwd ?? '') || !isAbsolute(config.tokenFile ?? '')) throw new Error('invalid bridge configuration');
  const st = await stat(config.tokenFile);
  if (!st.isFile() || (st.mode & 0o077) || st.size > 4096) throw new Error('bridge token file must be private');
  if (!(await stat(config.cwd)).isDirectory()) throw new Error('bridge cwd is not a directory');
  const token = (await readFile(config.tokenFile,'utf8')).trim();
  if (token.length < 32 || /\s/.test(token)) throw new Error('invalid bridge token');
  const auth = Buffer.from('Bearer '+token);
  const unpreset = await ctx.agentPresets.register({ id:PRESET, name:'MineAgent', description:'MineAgent scoped tools', plugins:[{id:'compaction',name:'cordis:group',group:true,isolate:{compaction:true},config:[{id:'compaction-basic',name:'@deepseek-ai/dsh-compaction-basic'}]}] });
  const runs = new Map();
  const retired = new Set();
  function retire(id) { retired.add(id); if (retired.size > 4096) retired.delete(retired.values().next().value); }
  let closing = false, poisoned = false;
  function authorized(req) {
    const supplied = Buffer.from(req.headers.authorization ?? '');
    return supplied.length === auth.length && timingSafeEqual(supplied,auth);
  }
  async function stop(run) {
    if (!run || run.stopped) return;
    run.cancelled = true; run.abort.abort();
    run.handle?.agent.cancel({kind:'user'});
    for (const p of run.pending.values()) p.reject(new Error('cancelled'));
    await run.settled;
    if (!run.stopped) fail(503);
  }
  async function drive(run, input, res) {
    let terminal = { type:'error', code:'UNKNOWN' };
    let releaseEvents, releaseError, releaseBudget;
    try {
      const messages = await durableMessages(ctx,input,deps);
      if (run.cancelled) throw new Error('cancelled');
      run.handle = await ctx.agents.create({
        sessionId:'mineagent-'+randomUUID(), meta:{cwd:config.cwd,agentPreset:PRESET},
        seed:historySeed(messages,input.instruction,deps),
        signal:run.abort.signal,
        agentOptions:{provider:input.provider,model:input.model,reasoningEffort:input.reasoningEffort},
        setup:async (agentCtx) => {
          // Restrictions filter inherited tools; local proxy registrations are
          // then the entire allowed surface. There is no host bash/file tool.
          agentCtx.tools.restrict({allow:[]});
          agentCtx.systemPrompt.suppressRuntimeContext();
          agentCtx.systemPrompt.section({name:'mineagent',order:0,text:input.instruction,complete:true,interpolate:false});
          for (const def of input.tools) agentCtx.tools.register({
            ...def, output:{schema:{type:'string'}, render:(_args,value)=>[{type:'text',text:value}]},
            execute:async (args,exec) => {
              if (run.cancelled || exec.signal.aborted) throw new Error('cancelled');
              const id = String(exec.callId);
              if (!CALL_ID.test(id) || run.seen.has(id)) throw new Error('duplicate or invalid tool call');
              run.seen.add(id);
              return await new Promise((resolve,reject) => {
                const abort = () => { run.pending.delete(id); reject(new Error('cancelled')); };
                const cleanup = () => exec.signal.removeEventListener('abort',abort);
                run.pending.set(id,{
                  resolve:value=>{cleanup();run.pending.delete(id);resolve(value);},
                  reject:e=>{cleanup();run.pending.delete(id);reject(e);}
                });
                exec.signal.addEventListener('abort',abort,{once:true});
                if (exec.signal.aborted) { abort(); return; }
                run.send({type:'tool',id,name:def.name,arguments:args});
              });
            }
          });
        }
      });
      const agent = run.handle.agent;
      if (run.cancelled) {agent.cancel({kind:'user'});throw new Error('cancelled');}
      let text = '', failed = false, ended = false, steps = 0;
      releaseEvents = ctx.on('session/event',(session,event)=>{
        if (session !== agent.session) return;
        if (event.type === 'assistant/message') {
          const visible = event.data.message.content.filter(b=>b.type==='text').map(b=>b.text).join('');
          if (visible.trim()) text=visible;
        }
        if (event.type === 'turn/end') {ended=true;if(event.data.reason.kind!=='completed')failed=true;}
      });
      releaseError = ctx.on('agent/error',payload=>{if(payload.agent===agent){failed=true;terminal={type:'error',code:publicErrorCode(payload.error?.code)};}});
      if (input.maxIterations > 0) releaseBudget = agent.ctx.on('agent/pre-step',async (payload,next)=>{
        if (payload.agent !== agent) return next();
        if (steps++ >= input.maxIterations) {agent.cancel({kind:'hook',reason:'iteration budget'});return {kind:'reject'};}
        return next();
      });
      agent.followup(messages.at(-1));
      await agent.whenIdle();
      if (!run.cancelled && !failed && ended) terminal={type:'done',text};
    } catch { /* No raw tool/model errors, tokens, arguments or URLs in transport logs. */ }
    finally {
      releaseBudget?.();releaseEvents?.();releaseError?.();
      try { await run.handle?.dispose();run.stopped=true; } catch { poisoned=true;terminal={type:'unsafe'}; }
      if (!res.destroyed) {run.send(terminal);res.end();}
      runs.delete(input.id);
    }
  }
  const server = createServer(async (req,res)=>{
    try {
      if (closing || poisoned) return json(res,503);
      // No browser/CORS surface; only the token-authenticated server client.
      if (req.headers.origin || !authorized(req)) return json(res,401);
      if (req.method === 'GET' && req.url === '/v1/health') return json(res,200,{protocol:1,active:runs.size,hostRunning:ctx.agents.list?.().filter(a=>a.status==='running').length ?? null,providers:ctx.llm.listProviders().map(p=>p.id)});
      if (req.method !== 'POST') return json(res,405);
      req.setTimeout(15000,()=>req.destroy());
      const input=await body(req);req.setTimeout(0);
      if (res.destroyed) return;
      if (req.url === '/v1/runs') {
        validate(input);
        if (!ctx.llm.listProviders().some(p=>p.id===input.provider)) return json(res,422);
        if (runs.size || retired.has(input.id)) return json(res,409);
        retire(input.id);
        const run={handle:null,cancelled:false,stopped:false,abort:new AbortController(),seen:new Set(),pending:new Map(),settled:null,
          send:frame=>{if(!res.destroyed)res.write(JSON.stringify(frame)+'\n');}};
        runs.set(input.id,run);
        res.writeHead(200,{'Content-Type':'application/x-ndjson','Cache-Control':'no-store','X-Content-Type-Options':'nosniff'});
        run.send({type:'ready'});
        const heartbeat=setInterval(()=>run.send({type:'heartbeat'}),15000);heartbeat.unref();
        res.on('close',()=>{clearInterval(heartbeat);if(!run.stopped)void stop(run).catch(()=>{});});
        run.settled=drive(run,input,res);
        return;
      }
      const match=/^\/v1\/runs\/([a-f0-9]{32})\/(cancel|tools\/([a-zA-Z0-9_-]{1,128}))$/.exec(req.url);
      if (!match) return json(res,404);
      const run=runs.get(match[1]);
      if (match[2]==='cancel') {retire(match[1]);await stop(run);return json(res,200,{stopped:true});}
      const pending=run?.pending.get(match[3]);
      if (!pending || run.cancelled || typeof input.value!=='string' || typeof input.error!=='boolean') return json(res,409);
      if (input.error) pending.reject(new Error(input.value)); else pending.resolve(input.value);
      return json(res,200);
    } catch (e) {
      if (!res.headersSent) json(res,e.status ?? 500); else res.destroy();
    }
  });
  server.requestTimeout=0; // Tasks are cancelled explicitly; no hidden total runtime limit.
  server.headersTimeout=15000;
  try { await new Promise((resolve,reject)=>{server.once('error',reject);server.listen(config.port,config.host ?? '127.0.0.1',resolve);}); }
  catch(e){await unpreset();throw e;}
  return {server,close:async()=>{
    closing=true;
    await Promise.allSettled([...runs.values()].map(stop));
    await new Promise(resolve=>server.close(resolve));
    await unpreset();
  }};
}
