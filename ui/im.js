/* Shared browser / DBX IM client. No secrets are included in URLs. */
'use strict';
const IMHelpers = {
  escape: value => String(value ?? '').replace(/[&<>"']/g, c => ({
    '&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'
  }
  [c])),size: n => n < 1024 ? `${n} B` : n < 1048576 ? `${(n / 1024).toFixed(1)} KB` : `${(n / 1048576).toFixed(1)} MB`,merge: (remote, pending) => [...remote, ...pending.filter(p => !remote.some(m => m.id === p.id))],title: (c, users, me) => c.kind === 'dm' ? (users.find(u => c.members.includes(u.id) && u.id !== me)?.nickname || c.name || '私聊') : c.name || '群聊',
}
;
IMHelpers.fileType = name => {
  const extension=String(name||'').match(/[^.]+\.([a-z0-9]{1,8})$/i)?.[1].toLowerCase()||'';
  const types=[
    ['excel','X',['xls','xlsx','xlsm','xlsb','xlt','xltx','csv','ods']],
    ['word','W',['doc','docx','docm','dot','dotx','odt','rtf']],
    ['slides','P',['ppt','pptx','pptm','pps','ppsx','odp']],
    ['pdf','PDF',['pdf']],['archive','ZIP',['zip','rar','7z','tar','gz','bz2','xz']],
    ['image','IMG',['png','jpg','jpeg','gif','webp','svg','bmp','heic','avif']],
    ['audio','AUD',['mp3','wav','flac','m4a','aac','ogg']],
    ['video','VID',['mp4','mov','mkv','avi','webm','wmv']],
    ['text','TXT',['txt','md','log']],['code','</>',['js','ts','json','html','css','py','go','xml','yaml','yml']]
  ];
  const match=types.find(type=>type[2].includes(extension));
  return match?{tone:match[0],label:match[1]}:{tone:'generic',label:extension?extension.slice(0,5).toUpperCase():'FILE'};
};
IMHelpers.fileBadge = name => {
  const {tone,label}=IMHelpers.fileType(name);
  return `<span class="file-badge file-badge-${tone}" aria-hidden="true"><svg viewBox="0 0 48 56" fill="none"><path d="M9 2h22l12 12v35a5 5 0 0 1-5 5H9a5 5 0 0 1-5-5V7a5 5 0 0 1 5-5Z" fill="currentColor" fill-opacity=".12" stroke="currentColor" stroke-width="1.5"/><path d="M31 2v10a2 2 0 0 0 2 2h10" stroke="currentColor" stroke-width="1.5"/><rect x="0" y="23" width="40" height="24" rx="5" fill="currentColor"/><text x="20" y="39" text-anchor="middle" fill="#fff" font-family="Segoe UI,Arial,sans-serif" font-size="${label.length>3?10:label.length>1?12:18}" font-weight="700">${IMHelpers.escape(label)}</text></svg></span>`;
};
// Incremental SHA-256 keeps attachment hashing bounded to one 256 KiB chunk.
class SHA256 {
  constructor(){
    this.h=new Uint32Array([0x6a09e667,0xbb67ae85,0x3c6ef372,0xa54ff53a,0x510e527f,0x9b05688c,0x1f83d9ab,0x5be0cd19]);
    this.buffer=new Uint8Array(64);
    this.used=0;
    this.length=0;
  }
  update(bytes){
    this.length+=bytes.length;
    for(let i=0;
    i<bytes.length;
    ){
      const count=Math.min(64-this.used,bytes.length-i);
      this.buffer.set(bytes.subarray(i,i+count),this.used);
      this.used+=count;
      i+=count;
      if(this.used===64){
        this.block(this.buffer);
        this.used=0;
      }
    }
    return this;
  }
  block(bytes){
    const k=SHA256.k,w=new Uint32Array(64),r=(n,b)=>(n>>>b)|(n<<(32-b));
    for(let i=0;
    i<16;
    i++)w[i]=(bytes[i*4]<<24)|(bytes[i*4+1]<<16)|(bytes[i*4+2]<<8)|bytes[i*4+3];
    for(let i=16;
    i<64;
    i++){
      const a=w[i-15],b=w[i-2];
      w[i]=(w[i-16]+(r(a,7)^r(a,18)^(a>>>3))+w[i-7]+(r(b,17)^r(b,19)^(b>>>10)))>>>0;
    }
    let[a,b,c,d,e,f,g,h]=this.h;
    for(let i=0;
    i<64;
    i++){
      const t1=(h+(r(e,6)^r(e,11)^r(e,25))+((e&f)^(~e&g))+k[i]+w[i])>>>0,t2=((r(a,2)^r(a,13)^r(a,22))+((a&b)^(a&c)^(b&c)))>>>0;
      h=g;
      g=f;
      f=e;
      e=(d+t1)>>>0;
      d=c;
      c=b;
      b=a;
      a=(t1+t2)>>>0;
    }
    [a,b,c,d,e,f,g,h].forEach((n,i)=>this.h[i]=(this.h[i]+n)>>>0);
  }
  hex(){
    const bits=this.length*8,pad=new Uint8Array(this.used<56?64-this.used:128-this.used);
    pad[0]=128;
    const view=new DataView(pad.buffer);
    view.setUint32(pad.length-8,Math.floor(bits/4294967296));
    view.setUint32(pad.length-4,bits>>>0);
    this.update(pad);
    return [...this.h].map(n=>n.toString(16).padStart(8,'0')).join('');
  }
}
SHA256.k=new Uint32Array([0x428a2f98,0x71374491,0xb5c0fbcf,0xe9b5dba5,0x3956c25b,0x59f111f1,0x923f82a4,0xab1c5ed5,0xd807aa98,0x12835b01,0x243185be,0x550c7dc3,0x72be5d74,0x80deb1fe,0x9bdc06a7,0xc19bf174,0xe49b69c1,0xefbe4786,0x0fc19dc6,0x240ca1cc,0x2de92c6f,0x4a7484aa,0x5cb0a9dc,0x76f988da,0x983e5152,0xa831c66d,0xb00327c8,0xbf597fc7,0xc6e00bf3,0xd5a79147,0x06ca6351,0x14292967,0x27b70a85,0x2e1b2138,0x4d2c6dfc,0x53380d13,0x650a7354,0x766a0abb,0x81c2c92e,0x92722c85,0xa2bfe8a1,0xa81a664b,0xc24b8b70,0xc76c51a3,0xd192e819,0xd6990624,0xf40e3585,0x106aa070,0x19a4c116,0x1e376c08,0x2748774c,0x34b0bcb5,0x391c0cb3,0x4ed8aa4a,0x5b9cca4f,0x682e6ff3,0x748f82ee,0x78a5636f,0x84c87814,0x8cc70208,0x90befffa,0xa4506ceb,0xbef9a3f7,0xc67178f2]);
IMHelpers.SHA256=SHA256;
if (typeof module !== 'undefined') module.exports = IMHelpers;
function startIMClient() {
  const $ = s => document.querySelector(s), esc = IMHelpers.escape, bridge = window.dbxPlugin;
  let token = '';
  const memoryStore = Object.create(null);
  function safeStore(kind='local') { try { return kind==='session'?window.sessionStorage:window.localStorage } catch (_) { return {getItem:k=>memoryStore[k]??null,setItem:(k,v)=>{memoryStore[k]=String(v)},removeItem:k=>{delete memoryStore[k]}} } }
  let tokenScope=bridge?'local':location.origin;
  const state = {
    me:null,users:[],conversations:[],current:null,messages:[],pending:[],drafts:new Map(),seen:new Map(),unread:new Map(),previews:new Map(),query:'',initialized:true,busy:false,upload:null,peers:[],peer:false
  }
  ;
  function applyTheme(theme){let mode=theme;if(theme==='system'){try{mode=window.matchMedia&&window.matchMedia('(prefers-color-scheme:dark)').matches?'dark':'light'}catch(_){mode='light'}}document.documentElement.dataset.imTheme=mode;safeStore().setItem('lan-im-theme',theme)}
  applyTheme(safeStore().getItem('lan-im-theme')||'system');
  try{window.matchMedia('(prefers-color-scheme:dark)').addEventListener('change',()=>{if((safeStore().getItem('lan-im-theme')||'system')==='system')applyTheme('system')})}catch(_){}
  const urls = new Set();
  let timer, toastTimer, pollBusy = false,sessionEpoch=0;
  const withTimeout = (promise, ms, message) => Promise.race([
    promise,
    new Promise((_, reject) => setTimeout(() => reject(new Error(message)), ms))
  ]);
  function toast(message) {
    const notice=$('#toast'),dialog=$('#modal');
    // A modal occupies the browser top layer. z-index alone cannot cover it.
    try{notice.hidePopover?.()}catch(_){}
    const parent=dialog.open?dialog:document.body;
    if(notice.parentElement!==parent)parent.appendChild(notice);
    notice.textContent = message;
    notice.hidden = false;
    try{notice.showPopover?.()}catch(_){}
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => {try{notice.hidePopover?.()}catch(_){}notice.hidden=true;}, 4500);
  }
  function tokenKey(){return 'lan-im-guest-token:'+tokenScope;}
  function restoreToken(){token=safeStore(bridge?'session':'local').getItem(tokenKey())||'';}
  function saveToken(value) {
    token=value;
    const storage=safeStore(bridge?'session':'local');value?storage.setItem(tokenKey(),value):storage.removeItem(tokenKey());
  }
  async function rpc(name, body={
  }
  ) {
    if(!bridge||typeof bridge.invoke!=='function') throw new Error('DBX 宿主桥接不可用');
    return withTimeout(bridge.invoke(name,body,{
      timeoutMs:120000
    }),15000,'DBX 后端响应超时，请在连接中启动本地服务');
  }
  async function api(path, method='GET', body, options={
  }
  ) {
    const epoch=sessionEpoch;
    let status, data;
    if (bridge) {
      const response = await rpc('im/request',{
        method,path,token,body:body ?? null
      }
      );
      status=response.status;
      data=response.body;
      if(typeof data==='string'){
        try{
          data=JSON.parse(data);
        }
        catch(_){
          data={
            error:data
          }
          ;
        }
      }
    }
    else {
      const response=await fetch('/api/im'+path,{
        method,headers:{
          ...(token?{
            Authorization:'Bearer '+token
          }
          :{
          }
          ),...(body!==undefined?{
            'Content-Type':'application/json'
          }
          :{
          }
          )
        }
        ,body:body===undefined?undefined:JSON.stringify(body),signal:options.signal
      }
      );
      status=response.status;
      data=await response.json().catch(()=>({
        error:'服务返回了无效响应'
      }
      ));
    }
    if(epoch!==sessionEpoch)throw new Error('会话已切换');
    if(status<200||status>=300) {
      if(status===401&&state.me){
        logoutLocal();
        guestEntry().catch(e=>toast(e.message));
      }
      throw new Error(data?.error||`请求失败 (${status})`);
    }
    return data || {
    }
    ;
  }
  function avatar(user, extra='') {
    const image=user?.avatar;
    return `<span class="avatar ${extra}">${image&&/^data:image\/(png|jpeg|webp|gif);base64,/.test(image)?`<img src="${esc(image)}" alt="">`:esc((user?.nickname||user?.username||'聊').slice(0,1))}</span>`;
  }
  function modal(title,html,{
    close=true
  }
  ={
  }
  ) {
    $('#modalContent').innerHTML=`<div class="modal-head"><h2>${esc(title)}</h2>${close?'<button type="button" class="icon" id="closeModal" aria-label="关闭">×</button>':''}</div>${html}`;
    $('#modal').classList.toggle('settings-dialog',title==='设置中心');
    if(!$('#modal').open)$('#modal').showModal();
    if(close)$('#closeModal').onclick=()=>$('#modal').close();
    $('#modal').oncancel=e=>{
      if(!close)e.preventDefault();
    }
    ;
  }
  function formError(form,error) {
    let node=form.querySelector('.error');
    if(!node){
      node=document.createElement('p');
      node.className='error';
      form.append(node);
    }
    node.textContent=error.message||error;
  }
  function submit(form,fn) {
    const button=form.querySelector('[type=submit]');
    let busy=false;
    const execute=async e=>{
      e?.preventDefault();
      if(busy||!form.reportValidity())return;
      busy=true;
      if(button)button.disabled=true;
      try{
        await fn(Object.fromEntries(new FormData(form)));
      }
      catch(error){
        formError(form,error);
      }
      finally{
        busy=false;
        if(button)button.disabled=false;
      }
    }
    ;
    if(button){button.type='button';button.onclick=execute;}
    form.onsubmit=execute;
    form.onkeydown=e=>{
      if(e.key==='Enter'&&!e.isComposing&&e.keyCode!==229&&e.target.tagName==='INPUT')execute(e);
    };
  }
  async function guestEntry(){
    if(bridge&&!state.hostStatus?.center){const result=await api('/guest','POST',{});saveToken(result.token);await enter();return;}
    modal('给自己起个名字',`<div class="auth-brand"><div class="logo">邻</div><div><h2>欢迎来到邻间</h2><p class="muted">无需账号，取个昵称就能加入局域网大厅</p></div></div><form id="guestForm" class="form"><label>你的昵称<input name="nickname" maxlength="64" required placeholder="大家如何称呼你" autocomplete="nickname"></label><button type="submit" class="primary">加入大厅</button></form>${location.protocol==='http:'?'<p class="auth-note">当前连接未加密，请仅在可信局域网内使用。</p>':''}`,{close:false});
    submit($('#guestForm'),async values=>{const result=await api('/guest','POST',values);saveToken(result.token);await enter();});
  }
  function logoutLocal(){
    sessionEpoch++;
    saveToken('');
    state.me=null;
    state.current=null;
    state.users=[];state.conversations=[];state.messages=[];state.pending=[];state.drafts.clear();state.unread.clear();state.previews.clear();state.fingerprint='';
    attachmentEpoch++;
    for(const cached of attachmentCache.values())if(cached.url)URL.revokeObjectURL(cached.url);
    attachmentCache.clear();
    $('#messages').innerHTML='';
    if(state.upload)state.upload.cancel=true;
    clearInterval(timer);
    $('#chat').hidden=true;
    $('#empty').hidden=false;
    $('#conversations').innerHTML='';
    for(const url of urls)URL.revokeObjectURL(url);
    urls.clear();
  }
  async function enter(){
    const me=await api('/me');
    state.me=me.user||me;
    $('#modal').close();
    $('#nodes').hidden=!bridge||!!state.hostStatus?.center||!state.me.admin;
    $('#profile').innerHTML=avatar(state.me);
    await refresh();
    const lobby=state.conversations.find(c=>c.id==='lan-lobby');if(lobby)await select(lobby);await acceptChannelInvitation();
    clearInterval(timer);
    timer=setInterval(()=>refresh().catch(e=>{
      $('#connection').textContent='连接中断';
    }
    ),3500);
  }
  function title(c){
    return state.peer?c.name:IMHelpers.title(c,state.users,state.me.id);
  }
  function renderConversations(){
    const q=$('#conversationSearch').value.toLowerCase();
    $('#conversationCount').textContent=state.conversations.length;
    $('#conversations').innerHTML=state.conversations.filter(c=>IMHelpers.title(c,state.users,state.me.id).toLowerCase().includes(q)).sort((a,b)=>(a.id==='lan-lobby'?0:1)-(b.id==='lan-lobby'?0:1)).map((c,index,items)=>{
      const name=IMHelpers.title(c,state.users,state.me.id);
      const group=c.id==='lan-lobby'?'公共空间':'私聊与群聊';
      const previous=items[index-1];
      const heading=!previous||(previous.id==='lan-lobby'?'公共空间':'私聊与群聊')!==group?`<div class="section-label">${group}</div>`:'';
      return `${heading}<button class="conversation ${!state.peer&&state.current?.id===c.id?'selected':''}" data-conversation="${esc(c.id)}">${avatar({nickname:name})}<div class="details"><strong>${esc(name)}</strong><div class="preview">${esc(state.previews.get(c.id)|| ((c.kind==='group'||c.kind==='channel')?`${c.members.length} 位成员`:'点击开始对话'))}</div></div>${state.unread.get(c.id)?`<span class="badge">${state.unread.get(c.id)}</span>`:''}${c.id==='lan-lobby'?'':`<span class="remove-conversation" data-remove="${esc(c.id)}" title="移除会话" aria-label="移除会话"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 7h16M9 7V4h6v3m-9 0 1 13h8l1-13M10 11v6m4-6v6"/></svg></span>`}</button>`;
    }
    ).join('')||'<div class="list-empty">还没有会话<br>从通讯录开始交流吧</div>';
    $('#conversations').querySelectorAll('[data-conversation]').forEach(b=>b.onclick=()=>select(state.conversations.find(c=>c.id===b.dataset.conversation)));
    $('#conversations').querySelectorAll('[data-remove]').forEach(b=>b.onclick=async e=>{e.stopPropagation();const c=state.conversations.find(x=>x.id===b.dataset.remove);if(!c||c.id==='lan-lobby')return;modal('移除会话',`<div class="form"><p>确定移除“${esc(IMHelpers.title(c,state.users,state.me.id))}”吗？</p><p class="muted">只会从你的列表移除，不会删除其他成员的消息。</p><div class="actions"><button type="button" class="text" id="cancelRemove">取消</button><button type="button" class="primary danger-action" id="confirmRemove">移除会话</button></div></div>`);$('#cancelRemove').onclick=()=>$('#modal').close();$('#confirmRemove').onclick=async()=>{try{await api('/conversations/'+encodeURIComponent(b.dataset.remove),'DELETE');$('#modal').close();if(state.current?.id===b.dataset.remove){state.current=null;$('#chat').hidden=true;$('#empty').hidden=false;}await refresh();toast('会话已移除');}catch(err){toast(err.message);}};});
  }
  async function refresh(){
    const epoch=sessionEpoch;
    if(pollBusy||!state.me)return;
    const selectedAtRequest=state.current;
    pollBusy=true;
    try{
      const [users,conversations]=await Promise.all([api('/users'),api('/conversations')]);
      if(epoch!==sessionEpoch)return;
      state.users=users.users||[];
      const contactQuery=($('#contactSearch')?.value||'').toLowerCase();
      document.querySelectorAll('[data-contact-id]').forEach(row=>{
        const user=state.users.find(u=>u.id===row.dataset.contactId);
        row.hidden=!user?.online||!row.dataset.contact.toLowerCase().includes(contactQuery);
        if(!user?.online){const checkbox=row.querySelector('input[type=checkbox]');if(checkbox)checkbox.checked=false;}
      });
      const previous=state.conversations;
      state.conversations=conversations.conversations||[];
      // A response requested before selecting a newly created/joined group
      // cannot revoke that selection. The next poll uses a fresh snapshot.
      const selectionChanged=state.current!==selectedAtRequest;
      if(selectionChanged&&state.current&&!state.peer){
        state.conversations=state.conversations.filter(c=>c.id!==state.current.id);
        state.conversations.unshift(state.current);
      }
      for(const c of state.conversations){
        if(typeof c.unread==='number'){state.unread.set(c.id,c.unread);continue;}
        const old=previous.find(x=>x.id===c.id);
        if(old&&old.updatedAt!==c.updatedAt&&state.current?.id!==c.id)state.unread.set(c.id,(state.unread.get(c.id)||0)+1);
      }
      if(state.current&&!state.peer&&!selectionChanged){
        const updated=state.conversations.find(c=>c.id===state.current.id);
        if(!updated){await clearRevokedConversation();toast('此会话已移除、解散或访问权限已撤销');return;}
        const changed=JSON.stringify(state.current)!==JSON.stringify(updated);
        state.current=updated;
        if(changed){$('#chatTitle').textContent=title(updated);$('#chatAvatar').textContent=title(updated).slice(0,1);}
        updateChannelControls();if(changed&&state.channelTab==='members')renderChannelMembers();
      }
      renderConversations();
      if(state.current)await loadMessages();
      $('#connection').textContent='局域网已连接';
    }
    finally{
      pollBusy=false;
    }
  }
  async function select(c,peer=false){
    if(state.current)state.drafts.set((state.peer?'peer:':'')+state.current.id,$('#draft').value);
    if(!c)return;
    state.current=c;
    if(!peer)state.revokedConversations?.delete(c.id);
    state.peer=peer;
    state.channelTab='chat';
    state.query='';
    state.historyCount=200;state.olderMessages=[];
    $('#messageSearch').value='';
    $('#searchNotice').hidden=true;
    state.messages=[];
    $('#messages').innerHTML='';
    $('#draft').value=state.drafts.get((peer?'peer:':'')+c.id)||'';
    $('#chatTitle').textContent=title(c);
    $('#chatSubtitle').textContent=peer?'已信任节点 · 签名直连':c.kind==='group'?`${c.members.length} 位成员 · 群聊${c.id!=='lan-lobby'&&(state.me.admin||c.ownerId===state.me.id)?' · 点击标题管理':''}`:'局域网 · 私聊';
    $('#chatAvatar').textContent=title(c).slice(0,1);
    $('#composerHint').textContent=c.id==='lan-lobby'?'公共大厅：同一服务的游客均可查看消息与文件':peer?'可信节点直连：仅此节点的对话双方可见':'私密会话：仅当前会话成员可查看消息与文件';
    $('#empty').hidden=true;
    $('#chat').hidden=false;
    $('#attach').disabled=false;
    updateChannelControls();
    document.body.classList.add('chat-open');
    state.unread.delete(c.id);
    renderConversations();
    await loadMessages(true);
    if(state.current?.id!==c.id||state.peer!==peer)return;
    if(!peer)api(`/conversations/${encodeURIComponent(c.id)}/read`,'POST',{
    }
    ).catch(()=>{
    }
    );
    if(state.channelTab==='chat')$('#draft').focus();
  }
  function messagesPath(c=state.current,peer=state.peer){
    return peer?`/peers/${encodeURIComponent(c.id)}/messages`:`/conversations/${encodeURIComponent(c.id)}/messages`;
  }
  async function loadMessages(force=false){
    const epoch=sessionEpoch;
    const c=state.current,peer=state.peer,q=state.query;
    if(!c)return;
    const result=await api(messagesPath(c,peer)+(peer?'':('?limit='+(state.historyCount||200)))+(q?(peer?'?':'&')+'q='+encodeURIComponent(q):''));
    if(epoch!==sessionEpoch||state.current?.id!==c.id||state.peer!==peer||state.query!==q)return;
    const messages=[...(state.olderMessages||[]),...(result.messages||[])].filter((m,i,a)=>a.findIndex(x=>x.id===m.id)===i);
    state.more=state.olderMessages?.length?state.more:!!result.more;
    if(!peer&&!q&&document.hasFocus())api('/conversations/'+encodeURIComponent(c.id)+'/read','POST',{}).catch(()=>{});
    const fingerprint=JSON.stringify(messages);
    if(!force&&state.fingerprint===fingerprint)return;
    state.fingerprint=fingerprint;
    state.messages=messages;
    state.pending=state.pending.filter(p=>!messages.some(m=>m.id===p.id));
    if(messages.length&&!q)state.previews.set(c.id,messages[messages.length-1].text||'[文件]');
    if(!peer&&!q&&document.hasFocus())api('/conversations/'+encodeURIComponent(c.id)+'/read','POST',{}).catch(()=>{});
    renderMessages(force);
  }
  async function loadOlder(){
    if(!state.current||state.peer||!state.messages.length)return;
    const c=state.current;
    const result=await api(messagesPath(c,false)+'?limit=200&beforeId='+encodeURIComponent(state.messages[0].id)+(state.query?'&q='+encodeURIComponent(state.query):''));
    if(state.current?.id!==c.id)return;
    state.more=!!result.more;state.olderMessages=[...(result.messages||[]),...(state.olderMessages||[])];
    state.fingerprint='';await loadMessages();
  }
  function renderMessages(force=false){
    const box=$('#messages'),atBottom=box.scrollHeight-box.scrollTop-box.clientHeight<85,oldTop=box.scrollTop;
    box.innerHTML=(state.more?'<div style="text-align:center;margin-bottom:20px"><button class="text" id="olderMessages">加载更早的消息</button></div>':'')+IMHelpers.merge(state.messages,state.pending.filter(p=>p.conversationId===state.current.id)).map(m=>{
      const mine=m.senderId===state.me.id||(state.peer&&m.senderId===state.nodeId)||m.localMine,user=state.users.find(u=>u.id===m.senderId)||{
        nickname:state.peer?(mine?'我':title(state.current)):'成员'
      }
      ;
      return `<article class="message ${mine?'mine':''}">${avatar(user)}<div class="message-body"><div class="message-meta">${esc(user.nickname||user.username)} · ${esc(new Date(typeof m.createdAt==='number'&&m.createdAt<1e12?m.createdAt*1000:m.createdAt).toLocaleString('zh-CN',{month:'2-digit',day:'2-digit',hour:'2-digit',minute:'2-digit'}))}</div><div class="bubble">${m.text?esc(m.text):''}${m.attachmentId?`<button class="file-card" data-file="${esc(m.attachmentId)}">${IMHelpers.fileBadge('')}<div><strong>查看附件</strong><small>授权访问 · 点击下载或预览</small></div></button>`:''}</div>${m.status?`<div class="message-status ${m.status==='failed'?'failed':''}">${m.status==='failed'?`发送失败 <button class="text" data-retry="${esc(m.id)}">重试</button>`:'发送中…'}</div>`:''}</div></article>`;
    }
    ).join('')||'<div class="list-empty">在这里开启第一段交流</div>';
    if($('#olderMessages'))$('#olderMessages').onclick=()=>{loadOlder().catch(e=>toast(e.message));};
    box.querySelectorAll('[data-retry]').forEach(b=>b.onclick=()=>deliver(state.pending.find(m=>m.id===b.dataset.retry)));
    const filePrefix=attachmentPrefix();
    box.querySelectorAll('[data-file]').forEach(b=>b.onclick=()=>download(b.dataset.file,filePrefix).catch(e=>toast(e.message)));
    hydrateAttachments().catch(()=>{});
    if(force||atBottom)box.scrollTop=box.scrollHeight;
    else box.scrollTop=oldTop;
  }
  function newId(){
    return globalThis.crypto?.randomUUID?.()||Date.now().toString(36)+'-'+Math.random().toString(36).slice(2);
  }
  async function emojiPanel(){
    const epoch=sessionEpoch,common='😀 😃 😄 😁 😆 😂 🙂 😉 😊 😍 🥰 😎 🤔 😮 😢 😡 👍 👎 👏 🎉 ❤️ 🔥 ✅'.split(' ');
    const result=await api('/stickers');if(epoch!==sessionEpoch)return;const stickers=result.stickers||[];
    modal('表情',`<div class="form"><h3>Emoji</h3><div class="emoji-grid">${common.map(x=>`<button type="button" data-emoji="${x}">${x}</button>`).join('')}</div><h3>收藏的图片表情</h3><p class="muted">在聊天图片预览中点击“收藏为表情”。点击图片发送。</p><div class="sticker-grid">${stickers.map(f=>`<div class="sticker-item"><button type="button" data-sticker="${esc(f.id)}" title="发送 ${esc(f.name)}"><span>加载图片…</span></button><button type="button" class="text" data-remove-sticker="${esc(f.id)}">移除</button></div>`).join('')||'<p class="muted">还没有收藏图片</p>'}</div></div>`);
    $('#modalContent').querySelectorAll('[data-emoji]').forEach(b=>b.onclick=()=>{const draft=$('#draft'),start=draft.selectionStart??draft.value.length,end=draft.selectionEnd??start;draft.value=draft.value.slice(0,start)+b.dataset.emoji+draft.value.slice(end);draft.dispatchEvent(new Event('input'));$('#modal').close();draft.focus()});
    $('#modalContent').querySelectorAll('[data-remove-sticker]').forEach(b=>b.onclick=async()=>{try{await api('/stickers/'+encodeURIComponent(b.dataset.removeSticker),'DELETE');await emojiPanel()}catch(e){toast(e.message)}});
    for(const f of stickers){const button=$('#modalContent').querySelector('[data-sticker="'+f.id+'"]');if(!button)continue;try{const url=await imageURL(f,'/stickers');if(epoch!==sessionEpoch||!button.isConnected){URL.revokeObjectURL(url);continue;}button.innerHTML=`<img src="${esc(url)}" alt="${esc(f.name)}">`;button.onclick=async()=>{if(!state.current){toast('请先选择会话');return;}try{const blob=await (await fetch(url)).blob();blob.name=f.name;$('#modal').close();await upload(blob)}catch(e){toast(e.message)}};}catch(e){button.textContent='图片不可用';}}
  }
  const pendingDownloadRecords=new Map();
  function downloadScope(){return (state.peer?'peer:':'room:')+(state.current?.id||'');}
  async function recordDownload(job,status,error=''){
    if(job.epoch!==sessionEpoch)return;
    const record={id:job.recordId||(job.recordId=newId()),attachmentId:job.id,prefix:job.prefix,scope:job.scope,name:job.file.name,size:job.file.size,received:job.offset||0,status,error:String(error).slice(0,1000),at:Date.now()};
    pendingDownloadRecords.set(record.id,{epoch:job.epoch,record});
    try{await api('/downloads','POST',record);if(pendingDownloadRecords.get(record.id)?.record===record)pendingDownloadRecords.delete(record.id);}catch(_){}
  }
  async function downloadHistoryPanel(scope=downloadScope()){
    const epoch=sessionEpoch;
    for(const [id,p] of pendingDownloadRecords){if(p.epoch!==epoch){pendingDownloadRecords.delete(id);continue;}try{await api('/downloads','POST',p.record);if(pendingDownloadRecords.get(id)===p)pendingDownloadRecords.delete(id)}catch(_){}}
    const response=await api('/downloads');if(epoch!==sessionEpoch)return;
    const records=new Map((response.downloads||[]).map(r=>[r.id,r]));for(const p of pendingDownloadRecords.values())if(p.epoch===epoch)records.set(p.record.id,p.record);
    const items=[...records.values()].filter(r=>r.scope===scope).sort((a,b)=>b.at-a.at),labels={saved:'已保存',paused:'已暂停',failed:'下载失败',browser:'已交给浏览器 · 完成状态未知'};
    modal('下载记录',`<div class="form"><p class="muted">当前会话的实际下载记录。仅保存接口确认完成后标记为“已保存”。</p><div class="file-list">${items.map(r=>`<div class="file-row">${IMHelpers.fileBadge(r.name)}<div class="grow"><strong>${esc(r.name)}</strong><small>${esc(labels[r.status]||r.status)} · ${IMHelpers.size(r.received)} / ${IMHelpers.size(r.size)}</small><small>${esc(new Date(r.at).toLocaleString('zh-CN'))}${r.error?' · '+esc(r.error):''}</small></div></div>`).join('')||'<p class="list-empty">本会话暂无下载记录</p>'}</div></div>`);
  }
  async function filesPanel(inline=false){
    if(!state.peer){await conversationFileBrowser(false);return;}
    const epoch=sessionEpoch,c=state.current,peer=state.peer,prefix=attachmentPrefix(),scope=downloadScope();let before='',more=true;const messages=[];
    while(more){const result=await api(messagesPath(c,peer)+(peer?'':'?limit=500'+(before?'&beforeId='+encodeURIComponent(before):'')));const page=result.messages||[];messages.push(...page);more=!peer&&!!result.more&&page.length>0&&page[0].id!==before;before=page[0]?.id;}
    const files=[];for(const id of new Set(messages.map(m=>m.attachmentId).filter(Boolean))){try{const result=await api(prefix+'/'+encodeURIComponent(id));files.push(result.attachment||result)}catch(_){files.push({id,name:'附件暂不可用',unavailable:true})}}
    if(epoch!==sessionEpoch||state.current?.id!==c.id||state.peer!==peer)return;
    const fileHTML=`${inline?'<div class="channel-page-heading"><h3>私聊文件</h3><p class="muted">与此可信节点往来的文件，仅对话双方可查看。</p></div>':''}<div class="file-list">${files.map(f=>`<div class="file-row">${IMHelpers.fileBadge(f.name)}<div class="grow"><strong>${esc(f.name)}</strong><small>${f.unavailable?'':IMHelpers.size(f.size)}</small></div><button type="button" class="text" data-download-file="${esc(f.id)}" ${f.unavailable?'disabled':''}>下载</button></div>`).join('')||'<p class="list-empty">当前会话还没有文件</p>'}</div><button type="button" class="text" id="downloadHistory">查看下载记录</button>`;
    if(inline){if(state.channelTab!=='files')return;$('#channelSurface').innerHTML=fileHTML;}else modal('当前会话文件',fileHTML);
    const fileRoot=inline?$('#channelSurface'):$('#modalContent');
    fileRoot.querySelectorAll('[data-download-file]').forEach(b=>b.onclick=()=>download(b.dataset.downloadFile,prefix,null,scope).catch(e=>toast(e.message)));
    fileRoot.querySelector('#downloadHistory').onclick=()=>downloadHistoryPanel(scope).catch(e=>toast(e.message));
  }
  async function deliver(message){
    const epoch=sessionEpoch;
    if(!message)return false;
    message.status='sending';
    if(state.current?.id===message.conversationId)renderMessages();
    try{
      await api(messagesPath({
        id:message.conversationId
      }
      ,message.peer),'POST',{
        id:message.id,text:message.text,attachmentId:message.attachmentId||''
      }
      );
      message.status='';
      if(state.current?.id===message.conversationId)await loadMessages(true);
    }
    catch(e){
      if(epoch!==sessionEpoch)return false;
      message.status='failed';
      toast(e.message);
    }
    if(state.current?.id===message.conversationId)renderMessages();
    return message.status!=='failed';
  }
  function send(text,attachmentId='',conversation=state.current,peer=state.peer){
    const message={
      id:newId(),conversationId:conversation.id,senderId:state.me.id,text,attachmentId,createdAt:new Date().toISOString(),status:'sending',localMine:true,peer
    }
    ;
    state.pending.push(message);
    return deliver(message);
  }
  async function contacts(group=false){
    if(group){await channelEditor();return;}
    await refresh();
    modal('通讯录',`<form id="peopleForm" class="form"><input class="contact-search" id="contactSearch" type="search" placeholder="搜索昵称或简介" aria-label="搜索通讯录"><div class="member-list contact-list">${state.users.filter(u=>u.id!==state.me.id&&!u.disabled&&u.online).map(u=>`<div class="person" data-contact-id="${esc(u.id)}" data-contact="${esc((u.nickname||u.username)+' '+(u.bio||''))}">${avatar(u)}<div class="grow"><strong>${esc(u.nickname||u.username)}</strong><p>${esc(u.bio||'@'+u.username)}</p></div><button type="button" class="text" data-user="${esc(u.id)}">发消息</button></div>`).join('')||'<p class="muted">暂无其他在线访客，让同一局域网的朋友打开此页面即可加入。</p>'}</div></form>`);
    $('#contactSearch').oninput=e=>{const q=e.target.value.toLowerCase();$('#peopleForm').querySelectorAll('[data-contact]').forEach(x=>{const user=state.users.find(u=>u.id===x.dataset.contactId);x.hidden=!user?.online||!x.dataset.contact.toLowerCase().includes(q)});};
    $('#peopleForm').querySelectorAll('[data-user]').forEach(b=>b.onclick=async()=>{
      try{
        const result=await api('/conversations','POST',{
          kind:'dm',members:[b.dataset.user]
        }
        );
        $('#modal').close();
        await refresh();
        await select(result.conversation||result);
      }
      catch(e){
        toast(e.message);
      }
    }
    );
  }
  function channelManager(c){return !!c&&(state.me?.admin||c.ownerId===state.me?.id);}
  function channelWritable(c=state.current){return !!c&&((c.kind!=='group'&&c.kind!=='channel')||c.id==='lan-lobby'||(!c.archived&&(!c.restricted&&c.kind!=='channel'||c.ownerId===state.me?.id||(c.writers||[]).includes(state.me?.id))));}
  function updateChannelControls(){
    const c=state.current,isLobby=c?.id==='lan-lobby',isChannel=!state.peer&&(c?.kind==='group'||c?.kind==='channel')&&!isLobby,hasTabs=!!c,tab=hasTabs?(state.channelTab||'chat'):'chat';
    $('#channelTabs').hidden=!hasTabs;$('#manageChannel').hidden=!isChannel||!channelManager(c);
    $('#channelTabs').setAttribute('aria-label',isLobby?'大厅页面':isChannel?'群聊页面':'私聊页面');
    $('#channelTabs').querySelectorAll('[data-channel-tab=members]').forEach(b=>b.hidden=!isChannel&&!isLobby);
    $('#messages').hidden=tab!=='chat';$('#composer').hidden=tab!=='chat'||!channelWritable();
    $('#messageSearch').hidden=tab!=='chat';$('#channelSurface').hidden=tab==='chat';
    $('#channelTabs').querySelectorAll('[data-channel-tab]').forEach(b=>b.setAttribute('aria-pressed',b.dataset.channelTab===tab));
    $('#channelSurface').querySelectorAll('[data-space-upload]').forEach(b=>b.hidden=!channelWritable(c));
    if(isLobby){$('#chatSubtitle').textContent=`${c.members.length} 位成员 · 局域网公开大厅`;$('#composerHint').textContent='公共大厅：同一服务的访客均可查看消息与文件';}
    if(isChannel){$('#chatSubtitle').textContent=`${c.members.length} 位成员 · ${c.archived?'已归档':channelWritable(c)?(c.restricted?'可发布 · 消息与上传':'全员可发言上传'):'仅查看与下载'}${c.description?' · '+c.description:''}`;$('#composerHint').textContent='仅群成员可访问消息与文件';}
  }
  async function clearRevokedConversation(){
    const id=state.current?.id;attachmentEpoch++;
    state.revokedConversations=state.revokedConversations||new Set();state.revokedConversations.add(id);
    state.current=null;
    state.pending=state.pending.filter(m=>m.conversationId!==id);state.drafts.delete(id);state.previews.delete(id);state.unread.delete(id);state.seen.delete(id);state.olderMessages=[];state.messages=[];state.current=null;state.fingerprint='';
    for(const cached of attachmentCache.values())if(cached.url){URL.revokeObjectURL(cached.url);urls.delete(cached.url);}attachmentCache.clear();
    $('#messages').innerHTML='';$('#channelSurface').innerHTML='';$('#draft').value='';$('#chat').hidden=true;$('#empty').hidden=false;$('#modal').close();
    document.body.classList.remove('chat-open');renderConversations();
    await revokeConversationTransfers(id);
  }
  async function revokeConversationTransfers(id){
    const releases=[];
    for(const [key,job] of uploadTasks){
      if(job.peer||job.conversation.id!==id)continue;
      job.revoked=true;job.cancel=true;uploadTasks.delete(key);
      if(!job.active){
        if(job.file.handleId)releases.push(bridge.fileTransfer.cancel(job.file.handleId).catch(()=>{}));
        if(job.file.archiveId)releases.push(rpc('im/archive-release',{id:job.file.archiveId}).catch(()=>{}));
      }
    }
    for(const [key,job] of downloadTasks){
      if(job.scope!=='room:'+id)continue;
      job.revoked=true;downloadTasks.delete(key);
      if(!job.active){
        if(job.nativeHandle)releases.push(bridge.fileTransfer.cancel(job.nativeHandle.handleId).catch(()=>{}));
        if(job.writer)releases.push(job.writer.abort().catch(()=>{}));
      }
    }
    await Promise.allSettled(releases);
  }
  async function channelEditor(c=null){
    if(c&&!channelManager(c))throw new Error('仅群主或本机管理员可以管理');
    await refresh();
    const people=state.users.filter(u=>!u.disabled&&u.id!==(c?.ownerId||state.me.id));
    modal(c?'管理群聊':'新建群聊',`<form id="channelForm" class="form"><label>群聊名称<input name="name" maxlength="80" required value="${esc(c?.name||'')}" placeholder="例如：财务资料、项目协作"></label><label>群简介<textarea name="description" rows="2" maxlength="500">${esc(c?.description||'')}</textarea></label><label>发布权限<select name="restricted" id="groupPublishing"><option value="false">全员可发言上传</option><option value="true">仅群主与指定成员发布</option></select></label><p class="muted">群主始终可发布。限制发布时，可勾选指定成员的“允许发布”；其他成员仅查看和下载。新成员可查看全部群历史。</p><div class="channel-member-picker">${people.map(u=>`<div class="channel-member-choice"><label class="check"><input type="checkbox" data-channel-member value="${esc(u.id)}" ${c?.members.includes(u.id)?'checked':''}>${esc(u.nickname||u.username)} <small>${u.online?'在线':'离线'}</small></label><label class="check"><input type="checkbox" data-channel-writer value="${esc(u.id)}" ${(c?.writers||[]).includes(u.id)?'checked':''}>允许发布</label></div>`).join('')||'<p class="muted">暂无其他访客，可先创建群聊，再生成邀请。</p>'}</div>${c?`<label class="check"><input name="archived" type="checkbox" ${c.archived?'checked':''}>归档群聊（保留阅读，停止加入、消息和上传）</label>`:''}<button type="submit" class="primary">${c?'保存群聊':'创建群聊'}</button><button type="button" class="text" id="joinGroup">使用邀请加入群聊</button></form>${c?'<div class="actions channel-management"><button type="button" class="text" id="channelInvites">邀请管理</button><button type="button" class="text danger" id="dissolveChannel">解散群聊</button></div>':''}`);
    $('#groupPublishing').value=String(c?.restricted??(c?.kind==='channel'));
    const publishing=()=>$('#channelForm').querySelectorAll('[data-channel-writer]').forEach(b=>{b.disabled=$('#groupPublishing').value!=='true';});
    $('#groupPublishing').onchange=publishing;publishing();$('#joinGroup').onclick=()=>channelJoinForm();
    $('#channelForm').querySelectorAll('[data-channel-writer]').forEach(b=>b.onchange=()=>{if(b.checked)$('#channelForm').querySelector(`[data-channel-member][value="${b.value}"]`).checked=true;});
    $('#channelForm').querySelectorAll('[data-channel-member]').forEach(b=>b.onchange=()=>{if(!b.checked)$('#channelForm').querySelector(`[data-channel-writer][value="${b.value}"]`).checked=false;});
    submit($('#channelForm'),async values=>{
      const members=[...$('#channelForm').querySelectorAll('[data-channel-member]:checked')].map(x=>x.value),writers=[...$('#channelForm').querySelectorAll('[data-channel-writer]:checked')].map(x=>x.value);
      const result=await api(c?'/conversations/'+c.id:'/conversations',c?'PATCH':'POST',{kind:'group',restricted:values.restricted==='true',name:values.name,description:values.description,members,writers,archived:!!$('#channelForm').querySelector('[name=archived]')?.checked});
      $('#modal').close();await refresh();await select(result);toast('群聊已保存');
    });
    if(c){$('#channelInvites').onclick=()=>channelInvitations(c).catch(e=>toast(e.message));$('#dissolveChannel').onclick=()=>{
      modal('解散群聊',`<div class="form"><p>确认解散“${esc(c.name)}”？所有成员将失去消息和文件的服务端访问权限，邀请同时失效。</p><p class="muted">已下载的文件无法收回。服务器原始数据暂留，不立即释放磁盘空间。</p><div class="actions"><button type="button" class="text" id="cancelDissolve">取消</button><button type="button" class="primary danger-action" id="confirmDissolve">确认解散</button></div></div>`);
      $('#cancelDissolve').onclick=()=>channelEditor(c);$('#confirmDissolve').onclick=async()=>{try{await api('/conversations/'+c.id+'/dissolve','POST',{});$('#modal').close();await refresh();toast('群聊已解散');}catch(e){toast(e.message);}};
    };}
  }
  async function channelInvitations(c){
    const response=await api('/conversations/'+c.id+'/invites');
    modal('群聊邀请',`<div class="form"><p><strong>${esc(c.name)}</strong></p><p class="muted">默认不开启公开加入。仅在需要时创建邀请；持有链接的人可加入并阅读全部历史。加入后遵循群的发布权限。</p><form id="inviteChannelForm" class="form"><label>有效期<select name="hours"><option value="1">1 小时</option><option value="24" selected>24 小时</option><option value="168">7 天</option></select></label><label>可加入人数<input name="uses" type="number" min="1" max="100" value="1" required></label><button type="submit" class="primary" ${c.archived?'disabled':''}>生成邀请</button></form><div class="member-list">${(response.invites||[]).map(inv=>`<div class="person"><div class="grow"><strong>${inv.expires>Date.now()&&inv.uses>0?'有效邀请':'已失效'}</strong><p>剩余 ${inv.uses} 次 · ${esc(new Date(inv.expires).toLocaleString())}</p></div><button type="button" class="text" data-share-invite="${esc(inv.token)}">分享</button><button type="button" class="text danger" data-revoke-invite="${esc(inv.token)}">撤销</button></div>`).join('')||'<p class="muted">暂无邀请，当前仅指定成员可访问。</p>'}</div></div>`);
    submit($('#inviteChannelForm'),async values=>{const inv=await api('/conversations/'+c.id+'/invites','POST',{hours:Number(values.hours),uses:Number(values.uses)});await shareChannelInvite(c,inv.token);});
    $('#modalContent').querySelectorAll('[data-share-invite]').forEach(b=>b.onclick=()=>shareChannelInvite(c,b.dataset.shareInvite).catch(e=>toast(e.message)));
    $('#modalContent').querySelectorAll('[data-revoke-invite]').forEach(b=>b.onclick=async()=>{try{await api('/conversations/'+c.id+'/invites/'+b.dataset.revokeInvite,'DELETE');await channelInvitations(c);toast('邀请已撤销');}catch(e){toast(e.message);}});
  }
  async function shareChannelInvite(c,key){
    const status=bridge?await rpc('im/status'):null;
    const candidates=[...new Set(bridge?(status.center?[status.center]:status.addresses||[]):[location.origin])];
    const lan=candidates.filter(x=>!['127.0.0.1','localhost','[::1]'].includes(new URL(x).hostname));
    const addresses=lan.length?lan:candidates;
    if(!addresses.length)throw new Error('没有可分享的局域网地址');
    modal('分享群聊邀请',`<div class="form"><p>${esc(c.name)} · 链接持有人可加入，发布权限由群设置决定</p>${!lan.length?'<p class="muted">当前仅有本机地址，其他设备需使用可访问的局域网地址。</p>':''}<div class="share-addresses">${addresses.map((address,index)=>{const url=new URL(address);url.hash='groupInvite='+encodeURIComponent(key);return `<div class="share-address"><label>邀请地址 ${index+1}</label><div class="share-address-row"><input readonly aria-label="群聊邀请地址 ${index+1}" value="${esc(url.href)}"><button type="button" class="text" data-copy-invite>复制</button><button type="button" class="text" data-qr-invite aria-expanded="false">二维码</button></div><div class="share-qr" data-invite-qr hidden></div></div>`;}).join('')}</div><button type="button" class="text" id="backInvites">返回邀请管理</button></div>`);
    $('#backInvites').onclick=()=>channelInvitations(c).catch(e=>toast(e.message));
    $('#modalContent').querySelectorAll('.share-address').forEach(row=>{const input=row.querySelector('input'),qr=row.querySelector('[data-invite-qr]'),button=row.querySelector('[data-qr-invite]');row.querySelector('[data-copy-invite]').onclick=async()=>{try{await navigator.clipboard.writeText(input.value);toast('邀请地址已复制');}catch(_){input.focus();input.select();let copied=false;try{copied=document.execCommand('copy')}catch(_){}toast(copied?'邀请地址已复制':'已选中，请按 Ctrl+C 复制');}};button.onclick=()=>{if(qr.hidden&&!qr.hasChildNodes()){if(typeof window.QRCode!=='function'){toast('二维码组件不可用');return;}new window.QRCode(qr,{text:input.value,width:190,height:190});}qr.hidden=!qr.hidden;button.setAttribute('aria-expanded',!qr.hidden);};});
  }
  function channelJoinForm(value=''){
    modal('加入群聊',`<form id="joinChannelForm" class="form"><p class="muted">加入后可以查看群聊全部历史。请确认邀请来源，再加入。</p><label>邀请链接或邀请码<input name="invite" value="${esc(value)}" required autocomplete="off"></label><button type="submit" class="primary">确认加入</button></form>`);
    submit($('#joinChannelForm'),async values=>{let key=values.invite.trim();try{const url=new URL(key);key=(new URLSearchParams(url.hash.slice(1)).get('groupInvite')||new URLSearchParams(url.hash.slice(1)).get('channelInvite'))||'';}catch(_){}if(!/^[a-f0-9]{64}$/.test(key))throw new Error('邀请链接或邀请码无效');const c=await api('/conversations/join','POST',{invite:key});$('#modal').close();await refresh();await select(c);if(!bridge)history.replaceState(null,'',location.pathname+location.search);toast('已加入群聊');});
  }
  async function acceptChannelInvitation(){
    if(bridge)return;
    const key=new URLSearchParams(location.hash.slice(1)).get('groupInvite')||new URLSearchParams(location.hash.slice(1)).get('channelInvite');
    if(key)channelJoinForm(key);
  }
  async function channelTab(tab){
    const c=state.current,isLobby=c?.id==='lan-lobby';if(!c||!['chat','files','members'].includes(tab))return;
    if(tab==='members'&&(state.peer||(!isLobby&&!['group','channel'].includes(c.kind))))return;
    state.channelTab=tab;updateChannelControls();
    if(tab==='files'){if(state.peer)await filesPanel(true);else await conversationFileBrowser(true);}
    if(tab==='members')renderChannelMembers();
  }
  function renderChannelMembers(){
    const c=state.current;if(!c||state.channelTab!=='members')return;
    const lobby=c.id==='lan-lobby';
    $('#channelSurface').innerHTML=`<div class="channel-page-heading"><h3>${lobby?'大厅成员':'群成员'}</h3><p class="muted">${esc(lobby?'同一局域网服务中的访客':c.description||'仅成员可查看群聊内容')}</p></div>${c.members.map(id=>{const u=state.users.find(x=>x.id===id)||{nickname:'成员'};const role=lobby?(u.online?'在线':'离线'):id===c.ownerId?'群主':(!c.restricted&&c.kind!=='channel')||(c.writers||[]).includes(id)?'可发言 · 消息与上传':'只读 · 查看与下载';return `<div class="person">${avatar(u)}<div class="grow"><strong>${esc(u.nickname||u.username)}</strong><p>${role}</p></div></div>`;}).join('')||'<p class="list-empty">当前没有成员</p>'}`;
  }
  async function conversationFileBrowser(inline=false){
    const c=state.current,epoch=sessionEpoch,prefix=attachmentPrefix(),scope=downloadScope();
    const html=`<div class="form">${inline?`<div class="space-file-heading"><div><h3>${c.id==='lan-lobby'?'大厅文件':c.kind==='dm'?'私聊文件':'群文件'}</h3><p class="muted">${c.id==='lan-lobby'?'大厅成员共享的文件。':c.kind==='dm'?'双方在此私聊中发送的文件，仅对话双方可查看。':'本群发送的文件集中查看，支持搜索、分类与下载。'}</p></div><div class="actions"><button type="button" class="primary" data-space-upload="file" ${channelWritable(c)?'':'hidden'}>上传文件</button><button type="button" class="text" data-space-upload="folder" ${channelWritable(c)?'':'hidden'}>上传文件夹</button></div></div>`:''}<div class="file-filters"><input id="fileQuery" type="search" placeholder="搜索文件名称" aria-label="搜索文件名称"><select id="fileType" aria-label="文件类型"><option value="">全部类型</option><option value="document">文档</option><option value="image">图片</option></select><select id="fileSort" aria-label="文件排序"><option value="recent">最近共享</option><option value="name">文件名</option><option value="size">文件大小</option></select><button type="button" class="text" id="filterFiles">查询</button></div><div class="actions"><button type="button" class="text" id="downloadSelected">下载选中项</button><button type="button" class="text" id="downloadHistory">下载记录</button></div><p id="fileCount" class="muted"></p><div id="libraryFiles" class="file-list"></div><button type="button" class="text" id="moreFiles" hidden>加载更多</button></div>`;
    if(inline)$('#channelSurface').innerHTML=html;else modal('当前会话文件',html);
    const root=inline?$('#channelSurface'):$('#modalContent'),fileList=root.querySelector('#libraryFiles');let offset=0,request=0;const selected=new Set();
    const load=async(reset=false)=>{
      if(reset){offset=0;selected.clear();}const serial=++request;
      const params=new URLSearchParams({limit:'30',offset:String(offset),q:root.querySelector('#fileQuery').value,type:root.querySelector('#fileType').value,sort:root.querySelector('#fileSort').value});
      const response=await api('/conversations/'+c.id+'/files?'+params);
      if(serial!==request||epoch!==sessionEpoch||state.current?.id!==c.id||root.querySelector('#libraryFiles')!==fileList||(inline&&state.channelTab!=='files'))return;
      const rows=(response.files||[]).map(f=>`<div class="file-row"><input type="checkbox" data-library-select="${esc(f.id)}" aria-label="选择 ${esc(f.name)}">${IMHelpers.fileBadge(f.name)}<div class="grow"><strong>${esc(f.name)}</strong><small>${IMHelpers.size(f.size)} · ${esc(state.users.find(u=>u.id===f.senderId)?.nickname||'成员')} · ${esc(new Date(f.sharedAt).toLocaleString())}</small></div><button type="button" class="text" data-library-download="${esc(f.id)}">下载</button>${f.senderId===state.me?.id||channelManager(c)?'<button type="button" class="icon danger" data-library-delete="'+esc(f.id)+'" aria-label="删除文件" title="删除文件"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M3 6h18M8 6V4h8v2m3 0-1 14H6L5 6m4 4v7m6-7v7" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"/></svg></button>':''}</div>`).join('');
      const list=root.querySelector('#libraryFiles');if(reset)list.innerHTML=rows||'<p class="list-empty">没有符合条件的文件</p>';else list.insertAdjacentHTML('beforeend',rows);
      offset=response.nextOffset;root.querySelector('#moreFiles').hidden=!response.more;root.querySelector('#fileCount').textContent=`共 ${response.total} 个文件，已显示 ${offset} 个`;
      list.querySelectorAll('[data-library-select]').forEach(b=>b.onchange=()=>b.checked?selected.add(b.dataset.librarySelect):selected.delete(b.dataset.librarySelect));
      list.querySelectorAll('[data-library-download]').forEach(b=>b.onclick=()=>download(b.dataset.libraryDownload,prefix,null,scope).catch(e=>toast(e.message)));
      list.querySelectorAll('[data-library-delete]').forEach(b=>b.onclick=()=>{
        const file=(response.files||[]).find(item=>item.id===b.dataset.libraryDelete);
        if(!file||epoch!==sessionEpoch||state.current?.id!==c.id)return;
        modal('删除文件',`<div class="form"><p>确定从“${esc(c.name||'当前会话')}”删除文件“${esc(file.name)}”吗？</p><p class="muted">文件和对应的聊天消息会从所有会话成员的历史中移除，已下载的副本不会被删除。</p><div class="actions"><button type="button" class="text" id="cancelFileDelete">取消</button><button type="button" class="primary danger-action" id="confirmFileDelete">删除文件</button></div></div>`);
        $('#cancelFileDelete').onclick=()=>$('#modal').close();
        $('#confirmFileDelete').onclick=async()=>{try{await api('/conversations/'+encodeURIComponent(c.id)+'/files/'+encodeURIComponent(file.id),'DELETE');$('#modal').close();if(epoch!==sessionEpoch||state.current?.id!==c.id)return;await loadMessages(true);await load(true);toast('文件已删除');}catch(error){toast(error.message);}};
      });
    };
    root.querySelectorAll('[data-space-upload]').forEach(b=>b.onclick=()=>{if(state.current?.id===c.id&&channelWritable(c))$(b.dataset.spaceUpload==='folder'?'#attachFolder':'#attach').click();});
    root.querySelector('#filterFiles').onclick=()=>load(true).catch(e=>toast(e.message));root.querySelector('#moreFiles').onclick=()=>load().catch(e=>toast(e.message));
    root.querySelector('#downloadHistory').onclick=()=>downloadHistoryPanel(scope).catch(e=>toast(e.message));
    root.querySelector('#downloadSelected').onclick=async()=>{if(!selected.size){toast('请先选择文件');return;}for(const id of [...selected]){if(epoch!==sessionEpoch||state.current?.id!==c.id)break;try{await download(id,prefix,null,scope);}catch(e){toast(e.message);break;}}};
    await load(true);
  }

  async function profile(){
    modal('我的资料',`<form id="profileForm" class="form"><div class="person">${avatar(state.me)}<button type="button" class="text" id="changeAvatar">更换头像</button></div><label>昵称<input name="nickname" value="${esc(state.me.nickname)}" required maxlength="64"></label><label>个人简介<textarea name="bio" maxlength="280" rows="3">${esc(state.me.bio)}</textarea></label><p class="muted">你的昵称和头像会显示给局域网内的其他访客。</p><button type="submit" class="primary">保存资料</button></form>`);
    $('#changeAvatar').onclick=()=>$('#avatarInput').click();
    submit($('#profileForm'),async values=>{const result=await api('/me','PATCH',{nickname:state.me.nickname,bio:state.me.bio,avatar:state.me.avatar,...values});state.me=result.user||result;$('#profile').innerHTML=avatar(state.me);$('#modal').close();await refresh();toast('资料已更新');});
  }
  async function editGroup(){
    const c=state.current;
    if(!c||c.id==='lan-lobby'||state.peer||!['group','channel'].includes(c.kind)||!channelManager(c))return;
    await channelEditor(c);
  }
  $('#chatTitle').onclick=()=>editGroup().catch(e=>toast(e.message));
  function bindSettingsTabs(root){
    const tabs=[...root.querySelectorAll('[data-settings-tab]')];
    const panels=[...root.querySelectorAll('[data-settings-panel]')];
    const activate=tab=>{
      tabs.forEach(button=>{
        const selected=button===tab;
        button.setAttribute('aria-selected',selected);
        button.tabIndex=selected?0:-1;
        button.classList.toggle('active',selected);
      });
      panels.forEach(panel=>{
        const selected=panel.dataset.settingsPanel===tab.dataset.settingsTab;
        panel.hidden=!selected;
        panel.style.display=selected?'':'none';
      });
    };
    tabs.forEach((tab,index)=>{
      tab.onclick=()=>activate(tab);
      tab.onkeydown=event=>{
        let next;
        if(event.key==='ArrowRight')next=(index+1)%tabs.length;
        if(event.key==='ArrowLeft')next=(index+tabs.length-1)%tabs.length;
        if(event.key==='Home')next=0;
        if(event.key==='End')next=tabs.length-1;
        if(next===undefined)return;
        event.preventDefault();activate(tabs[next]);tabs[next].focus();
      };
    });
    if(tabs.length)activate(tabs[0]);
  }
  async function share(){
    const status=bridge?await rpc('im/status'):null;
    const addresses=bridge?(status.addresses||[]):[location.origin];
    const center=status?.center||state.hostStatus?.center;
    const all=[...new Set([center,...addresses].filter(Boolean))];
    if(!all.length)throw new Error('请先启动本地服务，再分享局域网访问地址');
    modal('邀请身边的朋友',`<div class="form"><p>同一局域网内打开下面任一地址，填写昵称即可加入公共大厅。</p><div class="share-addresses">${all.map((address,index)=>`<div class="share-address"><label for="share-url-${index}">${address===center?'已连接的服务':address.includes('127.0.0.1')?'本机回环地址 · 仅本机访问':'局域网访问地址'}</label><div class="share-address-row"><input id="share-url-${index}" value="${esc(address)}" readonly aria-label="访问地址 ${index+1}"><button type="button" class="text" data-copy-address>复制</button><button type="button" class="text" data-show-qr aria-expanded="false" aria-controls="share-qr-${index}">二维码</button></div><div id="share-qr-${index}" class="share-qr" data-address-qr hidden></div></div>`).join('')}</div><p class="muted">选择与朋友处于同一网段的地址，复制或展示二维码即可分享。</p></div>`);
    $('#modalContent').querySelectorAll('.share-address').forEach(row=>{
      const input=row.querySelector('input'),copy=row.querySelector('[data-copy-address]'),button=row.querySelector('[data-show-qr]'),qr=row.querySelector('[data-address-qr]');
      copy.onclick=async()=>{
        try{await navigator.clipboard.writeText(input.value);toast('访问地址已复制');}
        catch(_){input.focus();input.select();let copied=false;try{copied=document.execCommand('copy')}catch(_){}toast(copied?'访问地址已复制':'已选中地址，请按 Ctrl+C 复制');}
      };
      button.onclick=()=>{
        if(qr.hidden){
          if(typeof window.QRCode!=='function'){toast('二维码组件未加载，请先复制地址');return;}
          if(!qr.hasChildNodes())try{new window.QRCode(qr,{text:input.value,width:190,height:190,correctLevel:window.QRCode.CorrectLevel?.M});}catch(_){qr.innerHTML='';toast('二维码生成失败，请复制地址');return;}
        }
        qr.hidden=!qr.hidden;button.setAttribute('aria-expanded',!qr.hidden);
      };
    });
  }
  async function settings(fromAuth=false){
    let status={running:false,addresses:[],interfaces:[],center:'',dataDir:'',filesDir:''};
    if(bridge)try{status=await rpc('im/status')}catch(e){toast(e.message)}
    if(!bridge){status.running=true;status.addresses=[location.origin];status.interfaces=[{name:'当前浏览器连接',addresses:[location.origin]}]}
    modal('设置中心',`<div class="settings-tabs" role="tablist" aria-label="设置分区">${[['appearance','外观'],['storage','存储'],['network','网络'],['connection','连接']].map(([key,label])=>`<button type="button" role="tab" id="settings-tab-${key}" data-settings-tab="${key}" aria-controls="settings-panel-${key}" aria-selected="false">${label}</button>`).join('')}</div><div class="settings-panels"><section role="tabpanel" id="settings-panel-appearance" aria-labelledby="settings-tab-appearance" tabindex="0" data-settings-panel="appearance" class="form"><h3>外观</h3><p class="muted">调整邻间在当前设备上的显示方式。</p><label>主题<select id="themeChoice"><option value="system">跟随系统</option><option value="light">浅色</option><option value="dark">深色</select></label><label>字体大小<select id="fontChoice"><option value="normal">标准</option><option value="large">大号</option></select></label><label class="check"><input id="compactChoice" type="checkbox">紧凑布局</label></section><section role="tabpanel" id="settings-panel-storage" aria-labelledby="settings-tab-storage" tabindex="0" data-settings-panel="storage" class="form" hidden><h3>存储</h3><p class="muted">消息数据库和接收文件分别存放。迁移会保留原目录。</p><label>消息存放地址<input id="dataDir" value="${esc(status.dataDir||'')}" placeholder="本机数据目录"></label><label>文件存放地址<input id="filesDir" value="${esc(status.filesDir||'')}" placeholder="接收文件目录"></label><button type="button" id="saveStorage" class="primary">迁移存储位置</button></section><section role="tabpanel" id="settings-panel-network" aria-labelledby="settings-tab-network" tabindex="0" data-settings-panel="network" class="form" hidden><h3>网络</h3><p class="muted">服务监听所有启用的网卡和 Wi‑Fi，局域网内可使用下面任一地址访问。</p><div class="network-list">${(status.interfaces||[]).map(i=>`<div class="network-item"><strong>${esc(i.name)}</strong>${(i.addresses||[]).map(a=>`<code>${esc(a)}</code>`).join('')}</div>`).join('')||'<p class="muted">服务尚未启动或未发现可用网卡。</p>'}</div></section><section role="tabpanel" id="settings-panel-connection" aria-labelledby="settings-tab-connection" tabindex="0" data-settings-panel="connection" class="form" hidden><h3>连接</h3><p><strong>本地服务</strong> · ${status.running?'运行中':'正在启动'}</p><form id="centerForm" class="form"><label>连接到已有服务<input name="url" type="url" placeholder="http://192.168.1.10:53318" value="${esc(status.center||'')}"></label><p class="muted">用于加入另一台电脑已启动的局域网服务；留空则使用本机服务。</p><button type="submit" class="primary">连接已有服务</button><button type="button" class="text" id="localCenter">使用本机服务</button></form></section></div>`);
    const theme=safeStore().getItem('lan-im-theme')||'system',font=safeStore().getItem('lan-im-font')||'normal';$('#themeChoice').value=theme;$('#themeChoice').onchange=e=>applyTheme(e.target.value);$('#fontChoice').value=font;document.documentElement.dataset.font=font;$('#compactChoice').checked=safeStore().getItem('lan-im-compact')==='1';document.documentElement.classList.toggle('compact',$('#compactChoice').checked);
    $('#fontChoice').onchange=e=>{safeStore().setItem('lan-im-font',e.target.value);document.documentElement.dataset.font=e.target.value};$('#compactChoice').onchange=e=>{safeStore().setItem('lan-im-compact',e.target.checked?'1':'0');document.documentElement.classList.toggle('compact',e.target.checked)};
    ['dataDir','filesDir'].forEach(id=>{const input=$('#'+id);if(!input)return;const wrap=document.createElement('div');wrap.className='path-control';input.parentNode.insertBefore(wrap,input);wrap.appendChild(input);const button=document.createElement('button');button.type='button';button.className='text';button.textContent='选择文件夹';button.disabled=!bridge;button.onclick=async()=>{button.disabled=true;try{const result=await withTimeout(bridge.invoke('im/pick-directory',{}, {timeoutMs:125000}),125000,'选择文件夹超时');if(result.path)input.value=result.path}catch(e){toast('选择文件夹失败：'+e.message)}finally{button.disabled=false}};wrap.appendChild(button)});
    bindSettingsTabs($('#modalContent'));
    $('#saveStorage').onclick=async()=>{if(!bridge){toast('浏览器模式不能修改本机存储目录，请在 DBX 插件中设置');return}try{await rpc('im/storage',{dataDir:$('#dataDir').value,filesDir:$('#filesDir').value});toast('存储位置已更新')}catch(e){toast(e.message)}};
    const connect=async url=>{
      saveToken('');
      await rpc('im/connect',{
        url
      }
      );
      logoutLocal();
      await initialize();
    }
    ;
    if(bridge){submit($('#centerForm'),v=>connect(v.url));$('#localCenter').onclick=()=>connect('').catch(e=>toast(e.message));}else{$('#centerForm').querySelector('button[type=submit]').disabled=true;$('#localCenter').disabled=true;}
    if($('#backAuth'))$('#backAuth').onclick=()=>initialize();
  }
  async function nodes(){
    try{
      const result=await api('/peers');
      state.peers=result.peers||[];
      state.nodeId=result.nodeId||state.nodeId;
      modal('附近的可信节点',`<p class="muted">通过其他可信方式核对完整节点 ID 后再信任。双方均需确认信任。</p><div>${state.peers.map(p=>`<div class="person">${avatar({nickname:p.name||p.id})}<div class="grow"><strong>${esc(p.name||'附近节点')}</strong><p>${esc(p.id)}</p><p>${esc(p.address||'')} · ${p.trusted?'已信任':'等待信任确认'}</p></div><button class="text" data-peer="${esc(p.id)}">${p.trusted?'发消息':'核对并信任'}</button>${p.trusted?`<button class="text danger" data-untrust="${esc(p.id)}">撤销信任</button>`:''}</div>`).join('')||'<div class="list-empty">尚未发现附近节点<br>请确保同一局域网的 DBX 节点已启动</div>'}</div>`);
      $('#modalContent').querySelectorAll('[data-untrust]').forEach(button=>button.onclick=()=>{
        const peer=state.peers.find(p=>p.id===button.dataset.untrust);
        modal('撤销节点信任',`<div class="form"><p>撤销对 ${esc(peer.name||'此节点')} 的信任？</p><div class="invite">${esc(peer.id)}</div><p class="muted">撤销后将拒绝该节点的新消息和文件。再次通信需要重新核对并信任。</p><div class="actions"><button id="cancelUntrust" class="text">取消</button><button id="confirmUntrust" class="primary">确认撤销</button></div></div>`);
        $('#cancelUntrust').onclick=()=>nodes();
        $('#confirmUntrust').onclick=async()=>{try{
          await api('/peers/trust','POST',{id:peer.id,trusted:false});
          if(state.peer&&state.current?.id===peer.id){state.current=null;state.peer=false;$('#chat').hidden=true;$('#empty').hidden=false;$('#messages').innerHTML='';}
          toast('已撤销节点信任');await nodes();
        }catch(error){toast(error.message);}};
      });
      $('#modalContent').querySelectorAll('[data-peer]').forEach(b=>b.onclick=async()=>{
        const p=state.peers.find(p=>p.id===b.dataset.peer);
        if(p.trusted){
          $('#modal').close();
          await select({
            id:p.id,name:p.name||p.id.slice(0,12)
          }
          ,true);
        }
        else{
          modal('确认节点身份',`<div class="form"><p>请与对方核对以下完整节点 ID：</p><div class="invite">${esc(p.id)}</div><p class="muted">名称和 IP 地址不能证明节点身份。仅在核对一致后信任。</p><button class="primary" id="trustPeer">已核对，信任此节点</button></div>`);
          $('#trustPeer').onclick=async()=>{
            try{
              await api('/peers/trust','POST',{
                id:p.id
              }
              );
              await nodes();
            }
            catch(e){
              toast(e.message);
            }
          }
          ;
        }
      }
      );
    }
    catch(e){
      toast(e.message);
    }
  }
  function base64(bytes){
    let value='';
    for(let i=0;
    i<bytes.length;
    i+=8192)value+=String.fromCharCode(...bytes.subarray(i,i+8192));
    return btoa(value);
  }
  function unbase64(value){
    return Uint8Array.from(atob(value),c=>c.charCodeAt(0));
  }
  const uploadTasks=new Map(),downloadTasks=new Map();
  async function upload(file,resume=null){
    if(!state.current||state.upload||(state.folderPreparing&&!file.archiveId))return;
    const epoch=sessionEpoch;
    const conversation=resume?.conversation||state.current,peer=resume?resume.peer:state.peer,prefix=peer?`/peers/${encodeURIComponent(conversation.id)}/attachments`:'/attachments',job=resume||{cancel:false,id:null,offset:0,file,conversation,peer,epoch,key:newId(),done:false};
      if(job.epoch!==epoch)throw new Error('身份已切换，不能继续原任务');
    job.cancel=false;job.active=true;uploadTasks.set(job.key,job);
    ;
    state.upload=job;
    $('#transfer').hidden=false;
    $('#attach').disabled=true;
    $('#transfer span').textContent=file.name;
    $('#transfer progress').value=0;
    try{
      if(!job.id){const result=await api(prefix,'POST',{
        conversationId:conversation.id,name:file.name,size:file.size,mime:file.type||file.contentType||'application/octet-stream'
      }
      );
      job.id=(result.attachment||result).id;
      }
      if(!peer){const result=await api(prefix+'/'+encodeURIComponent(job.id));const meta=result.attachment||result;job.offset=meta.offset||0;job.finished=meta.finished;}
      const start=job.offset||0;
      const hash=new SHA256();
      for(let offset=0;
      offset<file.size;
      offset+=job.lastLength){
        if(job.cancel||epoch!==sessionEpoch)throw new Error('上传已暂停');
        const bytes=file.archiveId ? unbase64((await rpc('im/archive-read',{id:file.archiveId,offset})).dataBase64) : file.handleId ? unbase64((await bridge.fileTransfer.read(file.handleId,offset,262144)).dataBase64) : new Uint8Array(await file.slice(offset,offset+262144).arrayBuffer());
        if(!bytes.length||bytes.length>262144)throw new Error('无效的文件块');
        job.lastLength=bytes.length;
        hash.update(bytes);
        if(offset+bytes.length>start)await api(`${prefix}/${encodeURIComponent(job.id)}/chunks`,'PUT',{
          offset:Math.max(offset,start),dataBase64:base64(bytes.subarray(Math.max(0,start-offset)))
        }
        );
        job.offset=offset+bytes.length;
        $('#transfer progress').value=Math.round((offset+bytes.length)/file.size*100);
      }
      if(job.cancel||job.revoked)throw new Error('上传已取消');
      if(!job.finished)await api(`${prefix}/${encodeURIComponent(job.id)}/finish`,'POST',{
        sha256:hash.hex()
      }
      );
      job.finished=true;
      if(await send('',job.id,conversation,peer)){toast('文件已发送');job.done=true;uploadTasks.delete(job.key);if(!peer&&state.current?.id===conversation.id&&state.channelTab==='files')await conversationFileBrowser(true).catch(e=>toast('文件已上传，列表刷新失败：'+e.message));}
    }
    catch(e){
      if(epoch!==sessionEpoch||job.revoked)return;
      toast(e.message);
      {
        modal('上传已暂停',`<div class="form"><p class="error">${esc(e.message)}</p><p>${esc(file.name)} · 已确认 ${IMHelpers.size(job.offset||0)}</p><button type="button" class="primary" id="retryUpload">继续上传</button></div>`);
        $('#retryUpload').onclick=()=>{
          $('#modal').close();
          upload(file,job);
        }
        ;
      }
    }
    finally{
      job.active=false;
      if(state.upload===job)state.upload=null;
      if((job.done||job.revoked||epoch!==sessionEpoch)&&file.handleId)await bridge.fileTransfer.cancel(file.handleId).catch(()=>{});
      if((job.done||job.revoked||epoch!==sessionEpoch)&&file.archiveId)await rpc('im/archive-release',{id:file.archiveId}).catch(()=>{});
      if(epoch!==sessionEpoch)return;
      $('#transfer').hidden=true;
      $('#attach').disabled=false;
      $('#fileInput').value='';
    }
    return job.done;
  }
  const attachmentCache=new Map();
  let attachmentEpoch=0;
  function attachmentPrefix(){return state.peer?`/peers/${encodeURIComponent(state.current.id)}/attachments`:'/attachments';}
  async function hydrateAttachments(){
    const prefix=attachmentPrefix(),conversationId=state.current.id,epoch=attachmentEpoch;
    for(const button of $('#messages').querySelectorAll('[data-file]')){
      const id=button.dataset.file,key=prefix+'/'+id;
      try{
        let cached=attachmentCache.get(key);
        if(!cached){const result=await api(prefix+'/'+encodeURIComponent(id));if(epoch!==attachmentEpoch)return;cached={file:result.attachment||result};attachmentCache.set(key,cached);}
        if(epoch!==attachmentEpoch||state.current?.id!==conversationId||!button.isConnected)continue;
        const file=cached.file,image=/^image\/(png|jpeg|webp|gif)$/.test(file.mime);
        if(image&&file.size<=4*1024*1024&&!cached.url){cached.url=await imageURL(file,prefix,epoch);
          const previews=[...attachmentCache.values()].filter(x=>x.url);
          if(previews.length>16){URL.revokeObjectURL(previews[0].url);urls.delete(previews[0].url);delete previews[0].url;}
        }
        if(epoch!==attachmentEpoch||!button.isConnected)continue;
        button.innerHTML=`${cached.url?'':IMHelpers.fileBadge(file.name)}<div>${cached.url?`<img class="file-preview" src="${cached.url}" alt="${esc(file.name)}">`:''}<strong>${esc(file.name)}</strong><small>${IMHelpers.size(file.size)} · ${image?'查看图片':'下载文件'}</small></div>`;
        button.onclick=()=> (image?previewImage(file,prefix,cached):download(id,prefix)).catch(e=>toast(e.message));
      }catch(_){button.title='附件暂不可用，点击重试';}
    }
  }
  async function imageURL(file,prefix,epoch=attachmentEpoch){
    if(file.size>16*1024*1024)throw new Error('图片预览上限为 16 MB，请直接保存文件');
    const chunks=[],hash=new SHA256();
    for(let offset=0;offset<file.size;){const part=await api(`${prefix}/${encodeURIComponent(file.id)}/chunks?offset=${offset}&length=262144`);if(epoch!==attachmentEpoch)throw new Error('会话已结束');const bytes=unbase64(part.dataBase64);if(!bytes.length||bytes.length>262144)throw new Error('无效图片数据');hash.update(bytes);chunks.push(bytes);offset+=bytes.length;}
    if(file.sha256&&hash.hex()!==file.sha256)throw new Error('图片校验失败');
    if(epoch!==attachmentEpoch)throw new Error('会话已结束');
    const url=URL.createObjectURL(new Blob(chunks,{type:file.mime}));urls.add(url);return url;
  }
  async function previewImage(file,prefix,cached){
    const epoch=attachmentEpoch;
    if(file.size>16*1024*1024){await download(file.id,prefix);return;}
    if(!cached.url)cached.url=await imageURL(file,prefix,epoch);
    if(epoch!==attachmentEpoch)return;
    modal('图片预览',`<div class="form"><img src="${cached.url}" alt="${esc(file.name)}" style="max-width:100%;max-height:60vh;object-fit:contain"><p>${esc(file.name)} · ${IMHelpers.size(file.size)}</p><div class="actions"><button type="button" class="text" id="favoriteImage">收藏为表情</button><button type="button" class="primary" id="savePreview">保存图片</button></div></div>`);
    $('#favoriteImage').onclick=async e=>{const button=e.currentTarget;button.disabled=true;let id;try{const f=await api('/stickers','POST',{name:file.name,size:file.size,mime:file.mime});id=f.id;for(let offset=0;offset<file.size;){const part=await api(`${prefix}/${encodeURIComponent(file.id)}/chunks?offset=${offset}&length=262144`);const n=unbase64(part.dataBase64).length;if(!n)throw new Error('图片数据不完整');await api('/stickers/'+id+'/chunks','PUT',{offset,dataBase64:part.dataBase64});offset+=n}await api('/stickers/'+id+'/finish','POST',{});toast('已收藏图片，可在表情面板发送')}catch(err){if(id)await api('/stickers/'+id,'DELETE').catch(()=>{});toast(err.message)}finally{button.disabled=false}};
    $('#savePreview').onclick=()=>download(file.id,prefix).catch(e=>toast(e.message));
  }
  async function download(id,prefix=attachmentPrefix(),resume=null,scope=downloadScope()){
    const epoch=sessionEpoch;
    const result=await api(prefix+'/'+encodeURIComponent(id)),file=result.attachment||result;
    if(scope.startsWith('room:')&&state.revokedConversations?.has(scope.slice(5)))throw new Error('群聊访问权限已撤销');
    const key=epoch+':'+prefix+'/'+id;
    if(!resume&&downloadTasks.has(key))resume=downloadTasks.get(key);
    if(resume?.active){toast('该文件正在下载');return;}
    if(resume&&resume.epoch!==epoch)throw new Error('身份已切换');
    const nativeSave=!!bridge?.fileTransfer?.beginSave&&!!(state.context?.capabilities?.fileTransfer||bridge?.capabilities?.fileTransfer)?.beginSave;
    let writer=resume?.writer||null,nativeHandle=resume?.nativeHandle||null;
    if(!resume&&nativeSave){
      nativeHandle=await bridge.fileTransfer.beginSave({name:file.name,contentType:file.mime,size:file.size});
      if(!nativeHandle)return;
    }
    else if(!resume&&window.showSaveFilePicker){
      try{
        const handle=await window.showSaveFilePicker({
          suggestedName:file.name
        }
        );
        writer=await handle.createWritable();
      }
      catch(e){
        if(e.name==='AbortError')return;
        throw e;
      }
    }
    if(!writer&&!nativeHandle){
      if(bridge)throw new Error('此宿主尚未提供文件保存能力，请更新 DBX 后重试');
      const ticket=await api(prefix+'/'+encodeURIComponent(id)+'/download-ticket','POST',{});
      const url=new URL(ticket.url,location.origin);
      if(url.origin!==location.origin||!url.pathname.startsWith('/api/im/'))throw new Error('无效下载地址');
      const link=document.createElement('a');link.href=url.href;link.download=file.name;link.rel='noreferrer';document.body.append(link);link.click();link.remove();await recordDownload({id,prefix,file,scope,epoch,offset:0},'browser');toast('已交给浏览器保存文件，完成状态请查看浏览器下载列表');return;
    }
    if(scope.startsWith('room:')&&state.revokedConversations?.has(scope.slice(5))){
      if(nativeHandle)await bridge.fileTransfer.cancel(nativeHandle.handleId).catch(()=>{});
      if(writer)await writer.abort().catch(()=>{});
      throw new Error('群聊访问权限已撤销');
    }
    const job=resume||{key,id,prefix,file,writer,nativeHandle,hash:new SHA256(),offset:0,epoch,scope};
    job.active=true;downloadTasks.set(key,job);
    const hash=job.hash;
    let offset=job.offset,cancel=false;
    modal('接收文件',`<div class="form"><p>${esc(file.name)} · ${IMHelpers.size(file.size)}</p><progress id="downloadProgress" max="100" value="0" style="width:100%"></progress><button class="text" id="cancelDownload">取消下载</button></div>`);
    $('#cancelDownload').onclick=()=>{
      cancel=true;
    }
    ;
    try{
      while(offset<file.size){
        if(job.revoked||epoch!==sessionEpoch||cancel||!$('#modal').open)throw new Error('下载已取消');
        const part=await api(`${prefix}/${encodeURIComponent(id)}/chunks?offset=${offset}&length=262144`);
        if(job.revoked)throw new Error('群聊访问权限已撤销');
        const bytes=unbase64(part.dataBase64);
        if(!bytes.length||bytes.length>262144)throw new Error('无效的文件块');
        if(nativeHandle){
          const chunkBytes=Math.min(262144,Math.max(1,nativeHandle.chunkBytes||262144));
          for(let inner=0;inner<bytes.length;inner+=chunkBytes)await bridge.fileTransfer.write(nativeHandle.handleId,offset+inner,base64(bytes.subarray(inner,inner+chunkBytes)));
        }
        else if(writer){await writer.seek(offset);await writer.write(bytes);}

        hash.update(bytes);
        offset+=bytes.length;
        job.offset=offset;
        const progress=$('#downloadProgress');
        if(progress)progress.value=offset/file.size*100;
      }
      if(file.sha256&&hash.hex()!==file.sha256)throw new Error('文件校验失败，请重新下载');
      if(job.revoked||epoch!==sessionEpoch)throw new Error('群聊访问权限已撤销');
      job.saving=true;
      if(nativeHandle){await bridge.fileTransfer.finish(nativeHandle.handleId);await recordDownload(job,'saved');downloadTasks.delete(key);$('#modal').close();toast('文件已保存并通过校验');return;}
      if(writer){
        await writer.close();
        await recordDownload(job,'saved');
        downloadTasks.delete(key);
        $('#modal').close();
        toast('文件已保存并通过校验');
        return;
      }
    }
    catch(e){
      const fatal=job.saving||/校验失败/.test(e.message);
      await recordDownload(job,fatal?'failed':'paused',e.message);
      if(job.revoked||epoch!==sessionEpoch||fatal){downloadTasks.delete(key);if(nativeHandle)await bridge.fileTransfer.cancel(nativeHandle.handleId).catch(()=>{});if(writer)await writer.abort().catch(()=>{});throw e;}
      modal('下载已暂停',`<div class="form"><p>${esc(file.name)}</p><p class="muted">${esc(e.message)} · 已接收 ${IMHelpers.size(job.offset)}</p><button type="button" class="primary" id="resumeDownload">继续下载</button></div>`);
      $('#resumeDownload').onclick=()=>download(id,prefix,job).catch(err=>toast(err.message));
    } finally {
      job.active=false;
    }
  }
  const sendDraft=e=>{
    e.preventDefault();
    const text=$('#draft').value.trim();
    if(!text||!state.current)return;
    $('#draft').value='';
    state.drafts.delete((state.peer?'peer:':'')+state.current.id);
    send(text);
  }
  ;
  $('#send').type='button';
  $('#send').onclick=sendDraft;
  $('#composer').onsubmit=sendDraft;
  let composing=false;
  $('#draft').addEventListener('compositionstart',()=>composing=true);
  $('#draft').addEventListener('compositionend',()=>composing=false);
  $('#draft').addEventListener('keydown',e=>{
    if(e.key==='Enter'&&!e.shiftKey&&!e.isComposing&&!composing&&e.keyCode!==229){
      e.preventDefault();
      sendDraft(e);
    }
  }
  );
  $('#draft').addEventListener('input',()=>{
    if(state.current)state.drafts.set((state.peer?'peer:':'')+state.current.id,$('#draft').value);
  }
  );
  let searchTimer;
  $('#messageSearch').oninput=()=>{
    clearTimeout(searchTimer);
    searchTimer=setTimeout(()=>{
      state.query=$('#messageSearch').value.trim();
      state.olderMessages=[];
      $('#searchNotice').hidden=!state.query;
      $('#searchNotice').textContent=state.query?'正在查看搜索结果；清空搜索以返回完整会话':'';
      loadMessages(true).catch(e=>toast(e.message));
    }
    ,350);
  }
  ;
  $('#conversationSearch').oninput=renderConversations;
  $('#manageChannel').onclick=()=>channelEditor(state.current).catch(e=>toast(e.message));
  $('#channelTabs').querySelectorAll('[data-channel-tab]').forEach(button=>button.onclick=()=>channelTab(button.dataset.channelTab).catch(e=>toast(e.message)));
  $('#contacts').onclick=$('#startChat').onclick=()=>contacts().catch(e=>toast(e.message));
  $('#newChat').onclick=()=>contacts(true).catch(e=>toast(e.message));
  $('#profile').onclick=()=>state.me&&profile();
  $('#share').onclick=()=>share().catch(e=>toast(e.message));
  $('#settings').onclick=()=>settings().catch(e=>toast(e.message));
  $('#nodes').onclick=nodes;
  $('#navChats').onclick=()=>document.body.classList.remove('chat-open');
  $('#back').onclick=()=>document.body.classList.remove('chat-open');
  $('#attach').onclick=async()=>{try{const capability=state.context?.capabilities?.fileTransfer||bridge?.capabilities?.fileTransfer;if(bridge?.fileTransfer?.pick&&capability?.pick){const result=await bridge.fileTransfer.pick({multiple:false});if(result?.files?.[0])await upload(result.files[0]);}else $('#fileInput').click();}catch(e){toast(e.message);}};
  $('#attachFolder').onclick=async()=>{
    if(!state.current||state.upload){toast('请先选择会话，并等待当前上传结束');return;}
    if(!bridge){$('#folderInput').click();return;}
    const epoch=sessionEpoch,conversationId=state.current.id,peer=state.peer;let id;
    state.folderPreparing=true;$('#attachFolder').disabled=true;$('#attach').disabled=true;
    try{
      const picked=await withTimeout(bridge.invoke('im/archive-pick',{}, {timeoutMs:125000}),125000,'选择文件夹超时');if(picked.cancelled)return;id=picked.id;toast('正在打包文件夹，完成后自动上传…');
      let result;for(;;){result=await rpc('im/archive-status',{id});if(result.ready)break;await new Promise(r=>setTimeout(r,500));}
      if(result.error)throw new Error(result.error);if(epoch!==sessionEpoch||state.current?.id!==conversationId||state.peer!==peer)throw new Error('会话已切换，请重新选择文件夹');
      await upload({archiveId:id,name:result.name,size:result.size,type:'application/zip'});id=null;
    }catch(err){toast(err.message);if(id)await rpc('im/archive-release',{id}).catch(()=>{});}
    finally{state.folderPreparing=false;$('#attachFolder').disabled=false;$('#attach').disabled=false;}
  };
  $('#emoji').onclick=()=>emojiPanel().catch(e=>toast(e.message));
  $('#chatFiles').onclick=()=>{if(state.current)channelTab('files').catch(e=>toast(e.message));};
  $('#transferTasks').onclick=()=>{
    const uploads=[...uploadTasks.values()].filter(j=>j.epoch===sessionEpoch),downloads=[...downloadTasks.values()].filter(j=>j.epoch===sessionEpoch);
    modal('传输任务',`<div class="form"><p class="muted">暂停或网络中断后可继续当前窗口中的任务。关闭插件窗口后，需要重新选择文件。</p>${uploads.map(j=>`<div class="file-row"><div class="grow"><strong>上传 · ${esc(j.file.name)}</strong><small>${IMHelpers.size(j.offset||0)} / ${IMHelpers.size(j.file.size)}</small></div><button type="button" class="text" data-resume-upload="${j.key}" ${j.active?'disabled':''}>${j.active?'传输中':'继续'}</button></div>`).join('')}${downloads.map((j,i)=>`<div class="file-row"><div class="grow"><strong>下载 · ${esc(j.file.name)}</strong><small>${IMHelpers.size(j.offset)} / ${IMHelpers.size(j.file.size)}</small></div><button type="button" class="text" data-resume-download="${i}" ${j.active?'disabled':''}>${j.active?'传输中':'继续'}</button></div>`).join('')}${!uploads.length&&!downloads.length?'<p class="list-empty">暂无未完成的传输</p>':''}</div>`);
    $('#modalContent').querySelectorAll('[data-resume-upload]').forEach(b=>b.onclick=()=>{const job=uploadTasks.get(b.dataset.resumeUpload);$('#modal').close();upload(job.file,job).catch(e=>toast(e.message))});
    $('#modalContent').querySelectorAll('[data-resume-download]').forEach(b=>b.onclick=()=>{const job=downloads[Number(b.dataset.resumeDownload)];download(job.id,job.prefix,job).catch(e=>toast(e.message))});
  };
  $('#fileInput').onchange=e=>{
    if(e.target.files[0])upload(e.target.files[0]);
  }
  $('#folderInput').onchange=async e=>{if(!state.current||state.upload)return;const epoch=sessionEpoch,conversationId=state.current.id,peer=state.peer;$('#attachFolder').disabled=true;try{toast('正在整理文件夹…');const archive=await IMArchive.folder([...e.target.files]);if(epoch!==sessionEpoch||state.current?.id!==conversationId||state.peer!==peer)throw new Error('会话已切换，请重新选择文件夹');await upload(archive)}catch(err){toast(err.message)}finally{$('#attachFolder').disabled=false;e.target.value=''}};
  ;
  $('#cancelUpload').onclick=()=>{
    if(state.upload)state.upload.cancel=true;
  }
  ;
  $('#avatarInput').onchange=async e=>{
    const file=e.target.files[0];
    if(!file)return;
    try{
      if(!/^image\/(png|jpeg|webp)$/.test(file.type))throw new Error('请选择 PNG、JPEG 或 WebP 图片');
      if(file.size>100*1024)throw new Error('头像不能超过 100 KB');
      const data=await new Promise((resolve,reject)=>{
        const reader=new FileReader();
        reader.onload=()=>resolve(reader.result);
        reader.onerror=reject;
        reader.readAsDataURL(file);
      }
      );
      const result=await api('/me','PATCH',{
        nickname:state.me.nickname,bio:state.me.bio,avatar:data
      }
      );
      state.me=result.user||{
        ...state.me,avatar:data
      }
      ;
      $('#profile').innerHTML=avatar(state.me);
      profile();
    }
    catch(error){
      toast(error.message);
    }
    e.target.value='';
  }
  ;
  window.addEventListener('focus',()=>{if(state.me&&state.current)loadMessages().catch(()=>{});});
  window.addEventListener('beforeunload',()=>{
    for(const url of urls)URL.revokeObjectURL(url);
  }
  );
  async function initialize(){
    try{
      if(bridge){
        if(!bridge.ready) throw new Error('DBX 宿主未提供 ready 桥接');
        state.context=await withTimeout(bridge.ready,8000,'DBX 宿主桥接初始化超时，请重新打开工作台');
        const fileCaps=state.context?.capabilities?.fileTransfer||bridge.capabilities?.fileTransfer;
        if(!state.dropRegistered&&fileCaps?.drop&&bridge.fileTransfer?.onDrop){
          bridge.fileTransfer.onDrop(async(files,info)=>{
            if(!state.me||!state.current){toast('请先加入大厅并选择会话');return;}
            if(info?.truncated)toast('部分拖入文件超出宿主数量限制');
            for(const file of files||[])await upload(file);
          });
          state.dropRegistered=true;
        }
        try{
          state.hostStatus=await rpc('im/status');
          if(!state.hostStatus.running&&!state.hostStatus.center)state.hostStatus=await rpc('im/start',{port:53318});
        }
        catch(error){
          state.hostStatus=state.hostStatus||{};
          toast('本地服务未启动：'+error.message+'。可在连接中重试。');
        }
      }
      const status=await api('/status');
      state.initialized=status.initialized;
      tokenScope=bridge?(state.hostStatus?.center||'local'):location.origin;
      restoreToken();
      if(token&&(!bridge||state.hostStatus?.center)){
        try{
          await enter();
          return;
        }
        catch(_){
          saveToken('');
        }
      }
      await guestEntry();
    }
    catch(e){
      $('#connection').textContent='服务不可用';
      modal('连接协作空间',`<div class="form"><p class="error">${esc(e.message)}</p><p class="muted">${bridge?'请在连接中启动本地服务，或连接局域网中心。':'请确认服务正在运行，然后重新连接。'}</p><button id="retryConnection" class="primary">${bridge?'打开连接':'重新连接'}</button></div>`,{
        close:false
      }
      );
      $('#retryConnection').onclick=()=>bridge?settings(true):initialize();
    }
  }
  initialize();
}
if (typeof document !== 'undefined') {
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', startIMClient, { once: true });
  } else {
    startIMClient();
  }
}
