'use strict';
const assert=require('assert').strict;
const fs=require('fs');
const vm=require('vm');
const js=fs.readFileSync(__dirname+'/im.js','utf8');
const start=js.indexOf('function bindSettingsTabs(');
assert(start>=0,'Settings must bind clickable tabs');
function element(key){return {dataset:{settingsTab:key,settingsPanel:key},style:{},attrs:{},classList:{toggle(){}},setAttribute(k,v){this.attrs[k]=String(v)},focus(){this.focused=true}};}
const names=['appearance','storage','network','connection'];
const tabs=names.map(element),panels=names.map(element);
const root={querySelectorAll:selector=>selector==='[data-settings-tab]'?tabs:panels};
const context={root};vm.createContext(context);
vm.runInContext(js.slice(start,js.indexOf('async function share(',start))+';bindSettingsTabs(root);',context);
for(let i=0;i<tabs.length;i++){
  tabs[i].onclick();
  panels.forEach((panel,j)=>assert.equal(panel.hidden,i!==j,'Only selected settings content is visible'));
  tabs.forEach((tab,j)=>assert.equal(tab.attrs['aria-selected'],String(i===j)));
}
let prevented=false;
tabs[3].onkeydown({key:'ArrowRight',preventDefault(){prevented=true}});
assert(prevented);assert(tabs[0].focused);assert.equal(panels[0].hidden,false);
console.log('Settings tabs: all four panels switch correctly; keyboard wraps and updates selection.');
(async()=>{
  const addresses=['http://192.168.1.2:53318','http://10.0.0.2:53318'];
  const nodes={},rows=addresses.map((address,i)=>{
    const input={value:address,focus(){},select(){}},copy={},qrButton={setAttribute(){}},qr={hidden:true,innerHTML:'',hasChildNodes:()=>false};
    nodes[i]={input,copy,qrButton,qr};
    return {querySelector:selector=>({'input':input,'[data-copy-address]':copy,'[data-show-qr]':qrButton,'[data-address-qr]':qr}[selector])};
  });
  const copied=[],encoded=[];
  const ctx={bridge:true,rpc:async()=>({addresses,center:'http://172.16.0.2:53318'}),state:{hostStatus:{}},location:{origin:addresses[0]},esc:s=>s,modal:(title,html)=>{assert(html.includes('172.16.0.2'));},$:()=>({querySelectorAll:()=>rows}),navigator:{clipboard:{writeText:async text=>copied.push(text)}},window:{QRCode:function(el,options){encoded.push(options.text)}},toast(){},document:{execCommand:()=>false}};
  vm.createContext(ctx);vm.runInContext(js.slice(js.indexOf('async function share('),js.indexOf('async function settings(')),ctx);
  await ctx.share();
  for(let i=0;i<rows.length;i++){
    await nodes[i].copy.onclick();nodes[i].qrButton.onclick();
    assert.equal(nodes[i].qr.hidden,false);nodes[i].qrButton.onclick();assert.equal(nodes[i].qr.hidden,true);
  }
  assert.deepEqual(copied,addresses);assert.deepEqual(encoded,addresses);
  console.log('Share: each address copies its own URL and opens its own matching QR code.');
})().catch(error=>{console.error(error);process.exitCode=1});
