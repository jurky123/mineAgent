import { boot, apiGet, apiPost } from './shell.js';
import { h, toast, confirmDialog } from './ds.js';
const $=id=>document.getElementById(id);
async function refresh(){
  const {groups}=await apiGet('/api/textcrypt/admin');$('groups').replaceChildren();
  if(!groups.length)$('groups').appendChild(h('p',null,'尚无程序批次。'));
  for(const group of groups){
    const card=h('article','card download-card');card.appendChild(h('h2',null,group.label||group.id));
    card.appendChild(h('p',null,'批次 '+group.id+' · '+(group.enabled?'可用':'已停用')));
    if(group.enabled){const stop=h('button','btn','停用此批程序');stop.onclick=async()=>{if(!await confirmDialog({title:'停用程序批次',body:'此批次的所有程序将无法再次领取密钥，已经留存的密钥和明文不受影响。',confirmText:'停用',danger:true}))return;try{await apiPost('/api/textcrypt/admin/revoke',{group:group.id});await refresh();}catch(e){toast(e.message,{warn:true});}};card.appendChild(stop);}
    if(!group.days.length)card.appendChild(h('p','field-hint','尚未生成每日密钥。'));
    for(const day of group.days){const row=h('div','download-actions');row.style.marginTop='12px';row.appendChild(h('span',null,day+'（北京时间）'));const remove=h('button','btn','清除此日密钥');remove.onclick=async()=>{if(!await confirmDialog({title:'清除 '+day+' 的密钥',body:'此操作无法通过本页面恢复。若清除当天密钥，今天将不能继续加解密；不会自动重新生成。备份中的副本不受影响。',confirmText:'清除',danger:true}))return;try{await apiPost('/api/textcrypt/admin/delete-key',{group:group.id,day});await refresh();}catch(e){toast(e.message,{warn:true});}};row.appendChild(remove);card.appendChild(row);}
    $('groups').appendChild(card);
  }
}
$('refresh').onclick=()=>refresh().catch(e=>toast(e.message,{warn:true}));
boot({active:'textcrypt'}).then(async user=>{if(!user?.admin){$('permission').textContent='需要管理员权限';return;}$('permission').textContent='';$('key-admin').hidden=false;await refresh();}).catch(e=>toast(e.message,{warn:true}));
