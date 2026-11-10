'use strict';
const assert=require('assert').strict,vm=require('vm'),fs=require('fs'),crypto=require('crypto');
const {Blob}=require('buffer'),{TextEncoder}=require('util');
global.Blob=Blob;global.TextEncoder=TextEncoder;
const archive=require('./im-archive.js'),helpers=require('./im.js');
const js=fs.readFileSync(__dirname+'/im.js','utf8');
const digest=b=>crypto.createHash('sha256').update(b).digest('hex');
function nodes(){const map=new Map();return s=>{if(!map.has(s))map.set(s,{value:'',hidden:false,disabled:false,open:true,close(){this.open=false}});return map.get(s)}}
(async()=>{
  const bytes=crypto.randomBytes(600001),file=new Blob([bytes]);file.name='resume.bin';
  const $=nodes();let remote=Buffer.alloc(0),lost=false,finished=false,creates=0;
  const context={$,esc:helpers.escape,state:{current:{id:'room'},peer:false,upload:null},sessionEpoch:0,newId:()=> 'task',SHA256:helpers.SHA256,Map,Uint8Array,Math,Error,encodeURIComponent,bridge:null,modal:()=>{},toast:()=>{},IMHelpers:helpers,base64:b=>Buffer.from(b).toString('base64'),send:async()=>true,
    api:async(path,method,body)=>{
      if(path==='/attachments'&&method==='POST'){creates++;return{id:'file'}}
      if(path==='/attachments/file'&&!method)return{offset:remote.length,finished};
      if(path.endsWith('/chunks')){assert.equal(body.offset,remote.length);remote=Buffer.concat([remote,Buffer.from(body.dataBase64,'base64')]);if(!lost&&remote.length>262144){lost=true;throw new Error('response lost after server commit')}return{offset:remote.length}}
      if(path.endsWith('/finish')){assert.equal(body.sha256,digest(bytes));finished=true;return{}}
      throw new Error(path);
    }};
  vm.createContext(context);vm.runInContext(js.slice(js.indexOf('const uploadTasks='),js.indexOf('const attachmentCache=')),context);
  await context.upload(file);assert.equal(remote.length,524288);assert.equal(finished,false);
  await $('#retryUpload').onclick();
  // onclick starts an async upload; wait for its completion without depending on a timer.
  while(context.state.upload)await new Promise(r=>setImmediate(r));
  assert.equal(creates,1);assert.equal(finished,true);assert.deepEqual(remote,bytes);
  console.log('Upload: committed chunk with lost response resumes at server offset, one attachment, correct full hash.');

  let written=Buffer.alloc(0),position=0,closed=false,failed=false,picks=0,reads=[];const history=new Map();
  const writer={seek:async p=>{position=p},write:async b=>{const next=Buffer.alloc(Math.max(written.length,position+b.length));written.copy(next);Buffer.from(b).copy(next,position);written=next;position+=b.length},close:async()=>{closed=true},abort:async()=>{throw new Error('must retain writer on network loss')}};
  const dnodes=nodes(),dc={$:dnodes,esc:helpers.escape,sessionEpoch:0,state:{current:{id:'room'}},newId:()=> 'download-task',downloadTasks:new Map(),attachmentPrefix:()=>'/attachments',SHA256:helpers.SHA256,IMHelpers:helpers,Uint8Array,Math,Error,encodeURIComponent,bridge:null,modal:()=>{dnodes('#modal').open=true},toast:()=>{},unbase64:b=>new Uint8Array(Buffer.from(b,'base64')),window:{showSaveFilePicker:async()=>{picks++;return{createWritable:async()=>writer}}},api:async(path,method,body)=>{if(path==='/downloads'){if(body.status==='saved')assert(closed,'Must close real writer before recording saved');history.set(body.id,{...body});return{}}if(path==='/attachments/file')return{id:'file',name:'resume.bin',size:bytes.length,sha256:digest(bytes)};const offset=Number(new URL(path,'http://local').searchParams.get('offset'));reads.push(offset);if(offset===262144&&!failed){failed=true;throw new Error('offline')}return{dataBase64:bytes.subarray(offset,offset+262144).toString('base64')}}};
  vm.createContext(dc);vm.runInContext(js.slice(js.indexOf('const pendingDownloadRecords='),js.indexOf('async function filesPanel('))+js.slice(js.indexOf('async function download('),js.indexOf('const sendDraft=')),dc);
  await dc.download('file');assert.equal(written.length,262144);assert.equal(closed,false);
  assert.equal(history.size,1);assert.equal(history.get('download-task').status,'paused');assert.equal(history.get('download-task').received,262144);
  await dnodes('#resumeDownload').onclick();while(!closed)await new Promise(r=>setImmediate(r));
  assert.equal(picks,1);assert.deepEqual(written,bytes);assert.equal(dc.downloadTasks.size,0);assert.deepEqual(reads,[0,262144,262144,524288]);
  assert.equal(history.size,1);assert.equal(history.get('download-task').status,'saved');assert.equal(history.get('download-task').scope,'room:room');assert.equal(history.get('download-task').received,bytes.length);
  console.log('Download history: actual pause recorded, same task updated only after writer.close succeeds.');
  console.log('Download: offline transfer retains writer/hash and resumes from 256 KiB without rereading completed data.');

  const records=[],bnodes=nodes(),browserContext={...dc,$:bnodes,window:{},location:{origin:'http://local'},URL,document:{createElement:()=>({click(){},remove(){}}),body:{append(){}}},downloadTasks:new Map(),api:async(path,method,body)=>{if(path==='/downloads'){records.push(body);return{}}if(path.endsWith('download-ticket'))return{url:'/api/im/download/test-ticket'};return{id:'browser-file',name:'browser.bin',size:100};}};
  vm.createContext(browserContext);vm.runInContext(js.slice(js.indexOf('const pendingDownloadRecords='),js.indexOf('async function filesPanel('))+js.slice(js.indexOf('async function download('),js.indexOf('const sendDraft=')),browserContext);
  await browserContext.download('browser-file');assert.equal(records.length,1);assert.equal(records[0].status,'browser');assert.equal(records[0].received,0);
  const failures=[],nctx={...dc,state:{current:{id:'room'},context:{capabilities:{fileTransfer:{beginSave:true}}}},base64:b=>Buffer.from(b).toString('base64'),bridge:{fileTransfer:{beginSave:async()=>({handleId:'native'}),write:async()=>{},finish:async()=>{throw new Error('disk full')},cancel:async()=>{}}},downloadTasks:new Map(),api:async(path,method,body)=>{if(path==='/downloads'){failures.push(body);return{}}if(path.includes('/chunks'))return{dataBase64:bytes.toString('base64').slice(0,0)};return{id:'empty-file',name:'zero.bin',size:0}}};
  vm.createContext(nctx);vm.runInContext(js.slice(js.indexOf('const pendingDownloadRecords='),js.indexOf('async function filesPanel('))+js.slice(js.indexOf('async function download('),js.indexOf('const sendDraft=')),nctx);
  await assert.rejects(nctx.download('empty-file'),/disk full/);assert.equal(failures.length,1);assert.equal(failures[0].status,'failed');
  console.log('Download history: browser handoff is never saved; native finish failure is recorded as failed.');

  const a=new Blob([Buffer.from('hello folder')]);a.name='a.txt';a.webkitRelativePath='目录/a.txt';
  const b=new Blob([bytes]);b.name='b.bin';b.webkitRelativePath='目录/sub/b.bin';
  const zip=await archive.folder([a,b]),buffer=Buffer.from(await zip.arrayBuffer());
  fs.mkdirSync(__dirname+'/../dist/qa',{recursive:true});fs.writeFileSync(__dirname+'/../dist/qa/folder-test.zip',buffer);
  assert.equal(buffer.readUInt32LE(0),0x04034b50);assert.equal(buffer.readUInt16LE(4),45);
  const end=buffer.length-98;assert.equal(buffer.readUInt32LE(end),0x06064b50);assert.equal(buffer.readUInt32LE(end+24),2);
  await assert.rejects(archive.folder([{name:'../bad',size:0}]),/路径无效/);
  assert.equal((archive.crc(0xffffffff,Buffer.from('123456789'))^0xffffffff)>>>0,0xcbf43926);
  console.log('ZIP64: nested Unicode paths, CRC, central directory and path traversal checks passed.');
})().catch(e=>{console.error(e);process.exitCode=1});
