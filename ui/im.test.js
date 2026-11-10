'use strict';
const assert = require('assert').strict;
const crypto = require('crypto');
const fs = require('fs');
const helpers = require('./im.js');
for(const [name,label,tone] of [
  ['预算.XLSX','X','excel'],['报表.xls','X','excel'],['文稿.docx','W','word'],
  ['通知.DOC','W','word'],['演示.pptx','P','slides'],['合同.pdf','PDF','pdf'],
  ['资料.zip','ZIP','archive'],['照片.png','IMG','image'],['notes.txt','TXT','text'],
  ['unknown.xyz','XYZ','generic'],['README','FILE','generic'],['.gitignore','FILE','generic']
]){
  const type=helpers.fileType(name);assert.equal(type.label,label);assert.equal(type.tone,tone);
  const badge=helpers.fileBadge(name);assert(badge.includes('<svg'));assert(badge.includes(`>${label}</text>`));
}
assert(!helpers.fileBadge('file.<img onerror=bad>').includes('<img'),'File icons must not inject filename HTML');
for (const length of [0, 1, 55, 56, 63, 64, 65, 262144, 524333]) {
  const bytes = crypto.randomBytes(length), hash = new helpers.SHA256();
  for (let offset = 0; offset < length; offset += 7919) hash.update(bytes.subarray(offset, offset + 7919));
  assert.equal(hash.hex(), crypto.createHash('sha256').update(bytes).digest('hex'), `hash length ${length}`);
}
assert.equal(helpers.escape('<img src="x" onerror=\'bad\'>&'), '&lt;img src=&quot;x&quot; onerror=&#39;bad&#39;&gt;&amp;');
assert.deepEqual(helpers.merge([{ id: 'accepted' }], [{ id: 'accepted' }, { id: 'failed', status: 'failed' }]), [{ id: 'accepted' }, { id: 'failed', status: 'failed' }]);
assert.equal(helpers.title({kind:'dm',members:['me','other']},[{id:'other',nickname:'同事'}],'me'),'同事');
assert.equal(helpers.title({kind:'group',name:'设计组'},[],'me'),'设计组');
const html=fs.readFileSync(__dirname+'/index.html','utf8'),js=fs.readFileSync(__dirname+'/im.js','utf8');
for (const match of js.matchAll(/\$\('#([A-Za-z][A-Za-z0-9]*)'\)/g)) {
  assert(html.includes(`id="${match[1]}"`) || js.includes(`id="${match[1]}"`),`Missing DOM target ${match[1]}`);
}
assert(!/\b(?:alert|confirm|prompt)\s*\(/.test(js),'Native blocking modal');
assert(!/[?&](?:token|authorization)=/i.test(js),'Token URL');
console.log('UI helpers: SHA-256 chunk boundaries, escaping, idempotent retry merge, titles, DOM targets and no token URLs passed.');
const vm = require('vm');
{
  const list={innerHTML:'',querySelectorAll:()=>[]};
  const context={$:s=>s==='#conversationSearch'?{value:''}:s==='#conversationCount'?{}:list,
    state:{conversations:[{id:'lan-lobby',name:'局域网大厅',kind:'group',members:[]},{id:'regular',name:'普通会话',kind:'group',members:[]}],users:[],me:{id:'me'},previews:new Map(),unread:new Map()},
    IMHelpers:helpers,esc:helpers.escape,avatar:()=>''};
  vm.createContext(context);
  vm.runInContext(js.slice(js.indexOf('function renderConversations('),js.indexOf('async function refresh('))+';renderConversations();',context);
  assert(!list.innerHTML.includes('data-remove="lan-lobby"'),'Lobby must have no remove action');
  assert(list.innerHTML.includes('data-remove="regular"'),'Ordinary conversations retain remove action');
  console.log('Conversations: fixed lobby has no remove action; ordinary conversations remain removable.');
}
// Sandbox blocks native form submission: primary actions must work through clicks.
{
  const source=js.slice(js.indexOf('function submit(form,fn)'),js.indexOf('async function guestEntry('));
  const button={disabled:false,type:'submit'}, form={querySelector:()=>button,reportValidity:()=>true};
  let calls=0;
  const context={FormData:class {constructor(){return [['username','admin']][Symbol.iterator]();}},Object,formError:()=>{},form,fn:()=>{calls++;}};
  vm.createContext(context);vm.runInContext(source+';submit(form,fn);',context);
  assert.equal(button.type,'button','Sandbox action must not use native submit');
  assert.equal(typeof button.onclick,'function','Direct click handler required');
  button.onclick({preventDefault(){}});
  assert.equal(calls,1,'Click must invoke action without submit event');
}
(async () => {
  {
    const source=js.slice(js.indexOf('async function guestEntry('),js.indexOf('function logoutLocal('));
    const calls=[],context={bridge:{},state:{hostStatus:{}},api:async(path,method,body)=>{calls.push({path,method,body});return {token:'guest-session'};},saveToken:value=>calls.push(value),enter:async()=>calls.push('entered')};
    vm.createContext(context);vm.runInContext(source,context);await context.guestEntry();
    let submitted;
    const browserCalls=[],browserContext={bridge:null,state:{},location:{protocol:'https:'},modal:(title,html)=>{assert.equal(title,'给自己起个名字');assert(html.includes('guestForm'));},$:()=>({}),submit:(form,fn)=>{submitted=fn;},api:async(path,method,body)=>{browserCalls.push({path,body});return{token:'visitor'};},saveToken:value=>browserCalls.push(value),enter:async()=>browserCalls.push('entered')};
    vm.createContext(browserContext);vm.runInContext(source,browserContext);await browserContext.guestEntry();await submitted({nickname:'访客小明'});assert.equal(browserCalls[0].body.nickname,'访客小明');assert.equal(browserCalls[2],'entered');
    let remoteForm;
    const remoteContext={...browserContext,bridge:{},state:{hostStatus:{center:'http://192.168.1.20:53318'}},submit:(form,fn)=>{remoteForm=fn;}};
    vm.createContext(remoteContext);vm.runInContext(source,remoteContext);await remoteContext.guestEntry();assert.equal(typeof remoteForm,'function','Remote DBX guest asks nickname instead of local identity');
    assert(js.includes("token&&(!bridge||state.hostStatus?.center)"),'Remote DBX restores saved guest');
    assert(js.includes("'lan-im-guest-token:'+tokenScope"),'Guest token scoped by service');
    assert(js.includes('公共大厅：同一服务的游客均可查看消息与文件'),'Public lobby privacy disclosure');
    assert(html.indexOf('vendor/qrcode.min.js')<html.indexOf('src="im.js"'),'QR script loads before IM');
    assert.equal(calls[0].path,'/guest');assert.equal(calls[0].method,'POST');assert.deepEqual(Object.keys(calls[0].body),[]);assert.equal(calls[2],'entered');
    assert(!/type="password"|\/login|\/register|\/sessions|\/invites/.test(js),'Guest UI must not contain account flows');
    assert(js.includes("c.id==='lan-lobby'"),'Guest startup must select public lobby');
    assert(!js.includes('128 MB'),'Downloads must not impose arbitrary memory-size rejection');
  }
  const source = js.slice(js.indexOf('async function api('), js.indexOf('function avatar('));
  for (const status of [200, 401]) {
    let resolve;
    const response = new Promise(r => { resolve = r; });
    const context = { bridge: {}, sessionEpoch: 0, token: 'old', rpc: () => response, state: {me:{id:'new'}}, logoutLocal: () => { throw new Error('stale response logged out current user'); }, guestEntry: async () => {}, Error };
    vm.createContext(context);
    vm.runInContext(source, context);
    const pending = context.api('/me');
    context.sessionEpoch++;
    resolve({status, body:{user:{id:'old'}}});
    await assert.rejects(pending, /会话已切换/);
  }
  console.log('Session epoch: stale successful and unauthorized responses rejected without modifying current login.');
})().catch(error => { console.error(error); process.exitCode=1; });
