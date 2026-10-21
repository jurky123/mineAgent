import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp,writeFile,rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createServer } from 'node:net';
import { randomUUID } from 'node:crypto';
import { startBridge } from './server.mjs';

async function fixture(t) {
  const root=await mkdtemp(join(tmpdir(),'dsh-bridge-unit-'));
  const token='b'.repeat(64),tokenFile=join(root,'token');await writeFile(tokenFile,token,{mode:0o600});
  const socket=createServer();await new Promise(r=>socket.listen(0,'127.0.0.1',r));const port=socket.address().port;await new Promise(r=>socket.close(r));
  const listeners=new Map(), state={created:0,disposed:0,tools:new Map()};
  const on=(name,fn)=>{const set=listeners.get(name)??new Set();set.add(fn);listeners.set(name,set);return()=>set.delete(fn);};
  const emit=(name,...args)=>{for(const fn of listeners.get(name)??[])fn(...args);};
  const ctx={on,agentPresets:{register:async def=>{state.preset=def;return async()=>{};}},llm:{listProviders:()=>[{id:'fixture'}]},attachments:{},agents:{create:async options=>{
    state.created++;state.options=options;let task=Promise.resolve();const abort=new AbortController();
    const agent={session:{},ctx:{on},cancel:()=>abort.abort(),whenIdle:()=>task,followup:()=>{task=(async()=>{
      let result='OK';
      if(state.tools.has('fixture_tool')) result=await state.tools.get('fixture_tool').execute({value:'safe'},{callId:'one',signal:abort.signal});
      emit('session/event',agent.session,{type:'assistant/message',data:{message:{content:[{type:'reasoning',text:'hidden'},{type:'text',text:result}]}}});
      emit('session/event',agent.session,{type:'turn/end',data:{reason:{kind:'completed'}}});
    })();}};
    const agentCtx={tools:{restrict:r=>{state.restriction=r;},register:def=>state.tools.set(def.name,def)},systemPrompt:{suppressRuntimeContext:()=>{},section:section=>{state.instruction=section;}}};
    await options.setup(agentCtx,agent);
    return{agent,dispose:async()=>{state.disposed++;abort.abort();if(state.failDispose)throw new Error("dispose failed");}};
  }}};
  const deps={createUserMessage:m=>({...m,id:randomUUID(),role:'user'}),createAssistantMessage:m=>({...m,id:randomUUID(),role:'assistant'}),createSystemMessage:text=>({role:'system',content:[{type:'text',text}]}),admitEncodedImages:async()=>[]};
  const bridge=await startBridge(ctx,{port,cwd:root,tokenFile},deps);
  t.after(async()=>{await bridge.close();await rm(root,{recursive:true,force:true});});
  const url='http://127.0.0.1:'+port,headers={'Authorization':'Bearer '+token,'Content-Type':'application/json'};
  const request={id:'a'.repeat(32),provider:'fixture',model:'fixture',reasoningEffort:'high',instruction:'Literal {{variable}}',messages:[{role:'user',content:[{type:'text',text:'hello'}]}],tools:[]};
  const post=(path,input,h=headers)=>fetch(url+path,{method:'POST',headers:h,body:JSON.stringify(input)});
  return{state,url,headers,request,post};
}
test('token/origin checks, inherited tools excluded, visible text only and no request replay',async t=>{
  const f=await fixture(t);
  assert.equal((await f.post('/v1/runs',f.request,{'Content-Type':'application/json'})).status,401);
  assert.equal((await f.post('/v1/runs',f.request,{...f.headers,Origin:'http://localhost'})).status,401);
  const response=await f.post('/v1/runs',f.request);assert.equal(response.status,200);
  const frames=(await response.text()).trim().split('\n').map(JSON.parse);
  assert.deepEqual(frames.at(-1),{type:'done',text:'OK'});
  assert.equal(f.state.created,1);assert.equal(f.state.disposed,1);
  assert.deepEqual(f.state.restriction,{allow:[]});assert.equal(f.state.instruction.interpolate,false);assert.equal(f.state.instruction.complete,true);
  assert.equal((await f.post('/v1/runs',f.request)).status,409);assert.equal(f.state.created,1);
});
test('tool result belongs to the active run; duplicate result and late replay are rejected',async t=>{
  const f=await fixture(t);f.request.tools=[{name:'fixture_tool',description:'test',parameters:{type:'object'}}];
  const response=await f.post('/v1/runs',f.request);const reader=response.body.getReader();const decoder=new TextDecoder();let text='';
  while(!text.includes('"type":"tool"')){text+=decoder.decode((await reader.read()).value);}
  const resultPath='/v1/runs/'+f.request.id+'/tools/one';
  assert.equal((await f.post('/v1/runs/'+'c'.repeat(32)+'/tools/one',{value:'wrong',error:false})).status,409);
  assert.equal((await f.post(resultPath,{value:'returned',error:false})).status,200);
  assert.equal((await f.post(resultPath,{value:'replay',error:false})).status,409);
  while(true){const r=await reader.read();if(r.done)break;text+=decoder.decode(r.value);}
  assert.equal(JSON.parse(text.trim().split('\n').at(-1)).text,'returned');
});
test('cancel drains agent and prevents an earlier cancelled id from starting',async t=>{
  const f=await fixture(t);const cancelled='d'.repeat(32);
  assert.equal((await f.post('/v1/runs/'+cancelled+'/cancel',{})).status,200);
  assert.equal((await f.post('/v1/runs',{...f.request,id:cancelled})).status,409);
  f.request.tools=[{name:'fixture_tool',description:'test',parameters:{type:'object'}}];
  const response=await f.post('/v1/runs',f.request);const reader=response.body.getReader();let text='';
  while(!text.includes('"type":"tool"'))text+=new TextDecoder().decode((await reader.read()).value);
  assert.equal((await f.post('/v1/runs/'+f.request.id+'/cancel',{})).status,200);
  assert.equal(f.state.disposed,1);
  while(!(await reader.read()).done){}
});

test('unconfirmed disposal closes admission instead of reporting success',async t=>{
  const f=await fixture(t);f.state.failDispose=true;
  const response=await f.post('/v1/runs',f.request);
  const frames=(await response.text()).trim().split('\n').map(JSON.parse);
  assert.equal(frames.at(-1).type,'unsafe');
  assert.equal((await f.post('/v1/runs',{...f.request,id:'e'.repeat(32)})).status,503);
});
