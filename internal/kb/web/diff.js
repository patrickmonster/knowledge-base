'use strict';

// Line diff (Myers O(ND)) for document revision comparison. Pure functions are
// exported for Node tests; the browser uses the globals.

const diffMaxEdits=3000;

function lineDiff(a,b) {
  let start=0;
  while (start<a.length&&start<b.length&&a[start]===b[start]) start++;
  let endA=a.length, endB=b.length;
  while (endA>start&&endB>start&&a[endA-1]===b[endB-1]) { endA--; endB--; }
  const out=[];
  for (let i=0;i<start;i++) out.push({op:' ',text:a[i]});
  // A loop, not push(...array): spreading a huge array exceeds the engine's argument limit.
  for (const op of myersDiff(a.slice(start,endA),b.slice(start,endB))) out.push(op);
  for (let i=endA;i<a.length;i++) out.push({op:' ',text:a[i]});
  return out;
}

function myersDiff(A,B) {
  const N=A.length, M=B.length;
  if (!N) return B.map(text=>({op:'+',text}));
  if (!M) return A.map(text=>({op:'-',text}));
  const max=N+M, off=max+1, v=new Int32Array(2*max+3), hist=[];
  let done=-1;
  for (let d=0; d<=max&&d<=diffMaxEdits; d++) {
    for (let k=-d; k<=d; k+=2) {
      let x=(k===-d||(k!==d&&v[off+k-1]<v[off+k+1]))?v[off+k+1]:v[off+k-1]+1;
      let y=x-k;
      while (x<N&&y<M&&A[x]===B[y]) { x++; y++; }
      v[off+k]=x;
      if (x>=N&&y>=M) { done=d; break; }
    }
    // hist[d] holds v for k in [-d, d] after step d.
    hist.push(v.slice(off-d,off+d+1));
    if (done>=0) break;
  }
  if (done<0) return [...A.map(text=>({op:'-',text})),...B.map(text=>({op:'+',text}))];
  const at=(d,k)=>hist[d][k+d];
  const ops=[];
  let x=N, y=M;
  for (let d=done; d>0; d--) {
    const k=x-y;
    const down=k===-d||(k!==d&&at(d-1,k-1)<at(d-1,k+1));
    const prevK=down?k+1:k-1, prevX=at(d-1,prevK), prevY=prevX-prevK;
    const startX=down?prevX:prevX+1;
    while (x>startX) { ops.push({op:' ',text:A[x-1]}); x--; y--; }
    if (down) ops.push({op:'+',text:B[y-1]}); else ops.push({op:'-',text:A[x-1]});
    x=prevX; y=prevY;
  }
  while (x>0) { ops.push({op:' ',text:A[x-1]}); x--; y--; }
  return ops.reverse();
}

// diffHunks groups lines into hunks with `context` unchanged lines around edits.
function diffHunks(lines,context=3) {
  const keep=new Array(lines.length).fill(false);
  lines.forEach((l,i)=>{ if (l.op!==' ') for (let j=Math.max(0,i-context); j<=Math.min(lines.length-1,i+context); j++) keep[j]=true; });
  const hunks=[];
  let current=null, oldNo=0, newNo=0;
  lines.forEach((l,i)=>{
    if (l.op!=='+') oldNo++;
    if (l.op!=='-') newNo++;
    if (!keep[i]) { current=null; return; }
    if (!current) { current={lines:[],skipped:0}; hunks.push(current); }
    current.lines.push({...l,old:l.op==='+'?null:oldNo,new:l.op==='-'?null:newNo});
  });
  return hunks;
}

function diffStats(lines) {
  return {added:lines.filter(l=>l.op==='+').length,removed:lines.filter(l=>l.op==='-').length};
}

if (typeof module!=='undefined') module.exports={lineDiff,diffHunks,diffStats};

// --- browser view -------------------------------------------------------------

function splitLines(text) { return (text||'').replace(/\r\n/g,'\n').split('\n'); }

function renderDiff(before,after) {
  const root=el('div',undefined,'diff-view');
  const meta=[];
  const tags=d=>(d.tags||[]).join(', ');
  for (const [label,get] of [['제목',d=>d.title],['종류',d=>d.kind],['상태',d=>d.status],['태그',tags],['출처 URL',d=>d.source_url||'']]) {
    if (get(before)!==get(after)) meta.push([label,get(before)||'—',get(after)||'—']);
  }
  if (meta.length) root.append(table(['항목','이전','이후'],meta));
  const lines=lineDiff(splitLines(before.content),splitLines(after.content));
  const stats=diffStats(lines);
  root.append(el('p',`본문 +${stats.added} −${stats.removed} 줄`,'small muted'));
  const hunks=diffHunks(lines);
  if (!hunks.length) { root.append(el('p','본문 변경이 없습니다.','small muted')); return root; }
  const box=el('div',undefined,'diff');
  hunks.forEach((h,i)=>{
    if (i>0) box.append(el('div','⋯','diff-gap'));
    for (const l of h.lines) {
      const row=el('div',undefined,'diff-line '+({'+':'add','-':'del',' ':'ctx'}[l.op]));
      row.append(el('span',l.old??'','diff-no'),el('span',l.new??'','diff-no'),el('span',l.op,'diff-op'),el('span',l.text,'diff-text'));
      box.append(row);
    }
  });
  root.append(box);
  return root;
}

// versionCompare renders revision pickers for one document and the diff between them.
function versionCompare(d,person) {
  const root=el('div',undefined,'version-compare');
  const status=el('p','버전 기록을 불러오는 중…','small muted');
  root.append(status);
  (async()=>{
    const history=await tool('get_document_history',{project_id:d.project_id,document_id:d.id});
    if (history.length<2) { status.textContent='비교할 이전 버전이 없습니다.'; return; }
    const options=history.map(h=>[String(h.revision),`버전 ${h.revision} · ${person(h.actor_id)} · ${new Date(h.created_at).toLocaleString()}`]);
    const form=el('div',undefined,'vector-controls');
    const from=selectField(form,'이전','from',options), to=selectField(form,'이후','to',options);
    from.value=String(history[1].revision);to.value=String(history[0].revision);
    const reasons=el('div'), result=el('div');
    const compare=btn('비교',async()=>{
      const a=Number(from.value), b=Number(to.value);
      if (a===b) throw Error('서로 다른 두 버전을 고르세요.');
      const [older,newer]=a<b?[a,b]:[b,a];
      result.replaceChildren(el('p','불러오는 중…','muted'));
      const [x,y]=await Promise.all([older,newer].map(revision=>tool('get_document',{project_id:d.project_id,document_id:d.id,revision})));
      const steps=history.filter(h=>h.revision>older&&h.revision<=newer).reverse();
      reasons.replaceChildren(el('p',`변경 사유 (버전 ${older} → ${newer})`,'small'),...steps.map(h=>el('p',`· 버전 ${h.revision}: ${h.reason} — ${person(h.actor_id)}`,'small muted')));
      result.replaceChildren(renderDiff(x,y));
    },'');
    form.append(compare);
    status.replaceWith(form);
    root.append(reasons,result);
    compare.click();
  })().catch(e=>{status.textContent=e.message;});
  return root;
}
