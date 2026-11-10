/* ZIP64 store archives: source File objects remain Blob parts; no whole-folder arrayBuffer. */
'use strict';
const IMArchive = (() => {
  const table=Array.from({length:256},(_,n)=>{for(let i=0;i<8;i++)n=n&1?0xedb88320^(n>>>1):n>>>1;return n>>>0;});
  const crc=(value,bytes)=>{for(const b of bytes)value=table[(value^b)&255]^(value>>>8);return value>>>0;};
  function header(size){const bytes=new Uint8Array(size),v=new DataView(bytes.buffer);return{bytes,u16:(o,n)=>v.setUint16(o,n,true),u32:(o,n)=>v.setUint32(o,n,true),u64:(o,n)=>{v.setUint32(o,n>>>0,true);v.setUint32(o+4,Math.floor(n/4294967296),true)}};}
  async function folder(files,progress=()=>{}){
    if(!files.length)throw new Error('文件夹中没有可传输的文件');
    const local=[],central=[];let offset=0,total=0,done=0;
    for(const file of files)total+=file.size;
    for(const file of files){
      const path=file.webkitRelativePath||file.name;
      if(path.startsWith('/')||path.includes('\\')||path.split('/').some(p=>!p||p==='.'||p==='..'))throw new Error('文件夹路径无效');
      const name=new TextEncoder().encode(path);if(name.length>65535)throw new Error('文件路径过长');
      let sum=0xffffffff;
      for(let pos=0;pos<file.size;pos+=262144){const bytes=new Uint8Array(await file.slice(pos,pos+262144).arrayBuffer());sum=crc(sum,bytes);done+=bytes.length;progress(done,total);}
      sum=(sum^0xffffffff)>>>0;
      const h=header(50);h.u32(0,0x04034b50);h.u16(4,45);h.u16(6,0x800);h.u32(14,sum);h.u32(18,0xffffffff);h.u32(22,0xffffffff);h.u16(26,name.length);h.u16(28,20);h.u16(30,1);h.u16(32,16);h.u64(34,file.size);h.u64(42,file.size);
      // Filename precedes the ZIP64 extra field.
      local.push(h.bytes.slice(0,30),name,h.bytes.slice(30),file);
      const c=header(74);c.u32(0,0x02014b50);c.u16(4,45);c.u16(6,45);c.u16(8,0x800);c.u32(16,sum);c.u32(20,0xffffffff);c.u32(24,0xffffffff);c.u16(28,name.length);c.u16(30,28);c.u32(42,0xffffffff);c.u16(46,1);c.u16(48,24);c.u64(50,file.size);c.u64(58,file.size);c.u64(66,offset);
      central.push(c.bytes.slice(0,46),name,c.bytes.slice(46));offset+=50+name.length+file.size;
    }
    const directory=new Blob(central),end=header(98);end.u32(0,0x06064b50);end.u64(4,44);end.u16(12,45);end.u16(14,45);end.u64(24,files.length);end.u64(32,files.length);end.u64(40,directory.size);end.u64(48,offset);end.u32(56,0x07064b50);end.u64(64,offset+directory.size);end.u32(72,1);end.u32(76,0x06054b50);end.u16(84,0xffff);end.u16(86,0xffff);end.u32(88,0xffffffff);end.u32(92,0xffffffff);
    const blob=new Blob([...local,directory,end.bytes],{type:'application/zip'});blob.name=(files[0].webkitRelativePath?.split('/')[0]||'文件夹')+'.zip';return blob;
  }
  return{folder,crc};
})();
if(typeof module!=='undefined')module.exports=IMArchive;
