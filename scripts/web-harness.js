'use strict';
// Loads the web console (internal/kb/web) into jsdom with a mock API, so
// tests can open pages and click through them without a server.
// Used by web-smoke.test.js.
const fs=require('node:fs');
const path=require('node:path');
const vm=require('node:vm');
const {JSDOM,VirtualConsole}=require('jsdom');

const webDir=path.join(__dirname,'..','internal','kb','web');

// A 2D canvas context that accepts every call (jsdom has no canvas).
function fakeCanvasContext(canvas){
  const noop=()=>{};
  return new Proxy({canvas},{
    get(target,key){
      if(key in target)return target[key];
      if(key==='measureText')return()=>({width:0});
      if(key==='createLinearGradient'||key==='createRadialGradient')return()=>({addColorStop:noop});
      if(key==='getImageData')return()=>({data:new Uint8ClampedArray(4)});
      return noop;
    },
    set(target,key,value){target[key]=value;return true;},
  });
}

// openConsole starts the console as a signed-in user.
//   respond({method, path, body}) returns the JSON `data` for a request, or
//   throws to answer with an error. Unknown requests should throw.
// The returned object exposes:
//   window, document
//   problems   uncaught errors, console errors and exception notices so far
//   notices    every error notice shown to the user
//   faults()   broken text on screen now: exception messages, 'undefined', 'NaN'…
//   requests   every API request made
//   settle()   waits until pending requests and timers have run
//   call(name, ...args)  calls a global function of the console (e.g. render)
//   get(name)  reads a global of the console (e.g. titles)
//   close()
async function openConsole(respond,{hash=''}={}){
  const problems=[],requests=[];
  const virtualConsole=new VirtualConsole();
  // jsdom cannot follow links or downloads; that is not a console bug.
  virtualConsole.on('jsdomError',e=>{if(!/^Not implemented: navigation/.test(e.message))problems.push('page error: '+(e.detail?.stack||e.stack||e.message));});
  virtualConsole.on('error',(...a)=>problems.push('console.error: '+a.map(String).join(' ')));
  const html=fs.readFileSync(path.join(webDir,'index.html'),'utf8');
  const dom=new JSDOM(html,{url:'http://localhost/'+hash,runScripts:'outside-only',pretendToBeVisual:true,virtualConsole});
  const {window}=dom,{document}=window;

  let pending=0;
  window.fetch=async(url,init={})=>{
    const method=init.method||'GET',p=String(url);
    let body;
    if(typeof init.body==='string'){try{body=JSON.parse(init.body);}catch{body=init.body;}}
    const request={method,path:p,body};
    requests.push(request);
    pending++;
    try{
      await new Promise(r=>setTimeout(r,0));
      let status=200,payload;
      try{payload={ok:true,data:await respond(request)};}
      catch(e){status=e.status||500;payload={ok:false,error:{code:e.code||'MOCK',message:e.message}};}
      return{ok:status<400,status,json:async()=>JSON.parse(JSON.stringify(payload))};
    }finally{pending--;}
  };
  window.confirm=()=>true;
  window.alert=()=>{};
  window.prompt=()=>'';
  window.scrollTo=()=>{};
  Object.defineProperty(window.navigator,'clipboard',{value:{writeText:async()=>{}},configurable:true});
  window.IntersectionObserver=class{observe(){}unobserve(){}disconnect(){}};
  window.ResizeObserver=class{observe(){}unobserve(){}disconnect(){}};
  window.URL.createObjectURL=()=>'blob:mock';
  window.URL.revokeObjectURL=()=>{};
  const dialogProto=window.HTMLDialogElement.prototype;
  dialogProto.showModal=function(){this.setAttribute('open','');};
  dialogProto.show=dialogProto.showModal;
  dialogProto.close=function(){if(!this.hasAttribute('open'))return;this.removeAttribute('open');this.dispatchEvent(new window.Event('close'));};
  window.HTMLCanvasElement.prototype.getContext=function(){return fakeCanvasContext(this);};
  window.addEventListener('error',e=>problems.push('uncaught: '+(e.error?.stack||e.message)));
  const onRejection=reason=>problems.push('unhandled rejection: '+(reason?.stack||reason));
  process.on('unhandledRejection',onRejection);

  // Scripts run in index.html order and share top-level let/const, like in a browser.
  const context=dom.getInternalVMContext();
  const sources=[...html.matchAll(/<script[^>]*\ssrc="\/([^"]+)"/g)].map(m=>m[1]);
  for(const name of sources)new vm.Script(fs.readFileSync(path.join(webDir,name),'utf8'),{filename:name}).runInContext(context);

  // The console reports a failed action as an error notice. Its own messages
  // (validation, server errors) are Korean; a notice without Hangul is a
  // JavaScript exception that reached the user, which is a bug.
  const notify=window.notify,notices=[];
  window.notify=(text,error=false)=>{
    if(error){notices.push(String(text));if(!/[가-힣]/.test(text)||/web-fixtures\.js/.test(text))problems.push('error notice: '+text);}
    return notify(text,error);
  };

  async function settle(){
    for(let quiet=0,i=0;quiet<3&&i<500;i++){
      await new Promise(r=>setTimeout(r,0));
      quiet=pending?0:quiet+1;
    }
    if(pending)problems.push('requests still pending after settle');
  }
  // Some pages print a failure where the content would be instead of raising a
  // notice, so the text on screen is checked too: exception messages, and
  // values that were printed without being there.
  const broken=/Cannot (read|set) properties|is not (a function|defined|iterable)|is not a constructor|\bundefined\b|\[object Object\]|\bNaN\b|Invalid Date|web-fixtures\.js/;
  function faults(){
    const out=[];
    for(const root of document.querySelectorAll('#page, dialog[open], #notice:not([hidden])')){
      const walker=document.createTreeWalker(root,window.NodeFilter.SHOW_TEXT);
      for(let n=walker.nextNode();n;n=walker.nextNode()){
        if(broken.test(n.nodeValue)&&!n.parentElement.closest('pre, code, textarea'))out.push('shown on screen: '+n.nodeValue.trim().slice(0,200));
      }
    }
    return out;
  }
  const get=name=>vm.runInContext(name,context);
  const call=(name,...args)=>get(name)(...args);
  await settle();
  return{window,document,problems,notices,faults,requests,settle,call,get,
    close(){process.off('unhandledRejection',onRejection);window.close();}};
}

module.exports={openConsole,webDir};
