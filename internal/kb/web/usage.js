'use strict';

// 사용 통계 대시보드: get_usage_stats(일별 추이·상위 작업·사용자·문서) + get_retrieval_metrics(검색 품질).
const usageKinds={brief:'기획',decision:'결정',history:'작업 이력',artifact:'산출물',reference:'참고',glossary:'용어집'};
const usageStatuses={active:'active',draft:'draft',archived:'보관'};
const usageNumber=n=>Number(n||0).toLocaleString('ko-KR');
const usagePercent=v=>v===null||v===undefined?'—':Math.round(v*100)+'%';
function usageBytes(n){n=Number(n||0);const units=['B','KB','MB','GB'];let i=0;while(n>=1024&&i<units.length-1){n/=1024;i++;}return (i?n.toFixed(1):n)+' '+units[i];}

async function usagePage(){
  const projects=[];
  for(let offset=0;offset<=100000;offset+=50){const batch=await tool('list_projects',{limit:50,offset});projects.push(...batch);if(batch.length<50)break;}
  const root=el('div',undefined,'usage-page');
  if(!projects.length){root.append(el('p','먼저 프로젝트에 참여해 주세요.','empty'));return root;}
  const project=projectSelect(projects);
  const controls=el('div',undefined,'usage-controls'),range=el('div',undefined,'usage-range');range.setAttribute('role','group');range.setAttribute('aria-label','조회 기간');
  let days=30;try{days=Number(localStorage.getItem('kb.usage.days'))||30;}catch{}
  const summary=el('p','','small muted');summary.setAttribute('role','status');
  for(const d of [7,30,90,365]){const b=el('button',d===365?'1년':d+'일','secondary');b.type='button';b.dataset.days=d;b.addEventListener('click',()=>{days=d;try{localStorage.setItem('kb.usage.days',String(d));}catch{}refresh().catch(e=>notify(e.message,true));});range.append(b);}
  controls.append(range,btn('새로고침',()=>refresh()));
  const body=el('div');
  root.append(controls,summary,body);
  let generation=0;
  async function refresh(){
    const ticket=++generation,current=projects.find(p=>p.id===project.value)||projects[0];
    range.querySelectorAll('button').forEach(b=>b.setAttribute('aria-pressed',String(Number(b.dataset.days)===days)));
    body.replaceChildren(el('p','불러오는 중…','muted'));
    const [stats,quality]=await Promise.all([tool('get_usage_stats',{project_id:current.id,days}),tool('get_retrieval_metrics',{project_id:current.id,days}).catch(()=>null)]);
    if(ticket!==generation)return;
    summary.textContent=`${current.name} · ${stats.from} ~ ${stats.to} (${stats.timezone}) · 통계를 조회한 이 요청은 집계에서 빠집니다.`;
    body.replaceChildren(usageTiles(stats),usageTrends(stats),usageKindGrowth(stats),usageQuality(quality),usageSearchMisses(stats),usageSearchLatency(stats),usageTables(stats));
  }
  await refresh();
  return root;
}

function usageTiles(s){
  const t=s.totals,inv=s.inventory,grid=el('div',undefined,'usage-tiles');
  const tile=(label,value,note)=>{const c=card();c.classList.add('metric','usage-tile');c.append(el('p',label,'muted'),el('div',value,'value'));if(note)c.append(el('p',note,'small muted usage-note'));return c;};
  grid.append(
    tile('도구 호출',usageNumber(t.calls),t.denied||t.errors?`거부 ${usageNumber(t.denied)} · 오류 ${usageNumber(t.errors)}`:'거부·오류 없음'),
    tile('활성 사용자',usageNumber(t.active_users),`참여자 ${usageNumber(inv.members)}명 중`),
    tile('검색',usageNumber(t.searches),`결과 0건 ${usageNumber(t.search_misses)}회 · 문서 열람 ${usageNumber(t.document_reads)}회`),
    tile('문서 작성·수정',usageNumber(t.document_writes),`새 문서 ${usageNumber(t.documents_new)}건`),
    tile('작업 시작',usageNumber(t.tasks_started),`완료 ${usageNumber(t.tasks_completed)} · 중단 ${usageNumber(t.tasks_abandoned)} · 열림 ${usageNumber(inv.open_tasks)}`),
    tile('보유 지식',usageNumber(inv.documents),`첨부 ${usageNumber(inv.attachments)}개 · ${usageBytes(inv.attachment_bytes)}`)
  );
  return grid;
}

// 일별 추이: 한 측정값당 한 차트(작은 다중 차트), 단일 계열이라 범례 없이 제목이 이름을 대신한다.
function usageTrends(s){
  const c=card('일별 추이'),grid=el('div',undefined,'usage-charts');
  for(const [key,label]of [['calls','도구 호출'],['active_users','활성 사용자'],['searches','검색'],['document_writes','문서 작성·수정'],['tasks_started','작업 시작']])grid.append(usageBarChart(s.daily,key,label));
  const details=el('details',undefined,'usage-table-view');details.append(el('summary','표로 보기'));
  details.append(table(['날짜','도구 호출','활성 사용자','검색','문서 작성·수정','작업 시작'],[...s.daily].reverse().map(d=>[d.date,usageNumber(d.calls),usageNumber(d.active_users),usageNumber(d.searches),usageNumber(d.document_writes),usageNumber(d.tasks_started)])));
  c.append(grid,details);
  return c;
}

// 90일을 넘으면 막대가 너무 촘촘해져 최근 날짜부터 7일씩 묶는다. 활성 사용자는 합이 아니라 일 최대값으로 묶는다.
function usageBuckets(daily,key){
  if(daily.length<=90)return {weekly:false,items:daily.map(d=>({label:d.date,value:d[key]||0,first:d.date,last:d.date}))};
  const items=[];
  for(let end=daily.length;end>0;end-=7){const part=daily.slice(Math.max(0,end-7),end),values=part.map(d=>d[key]||0);
    items.unshift({label:part[0].date+' ~ '+part.at(-1).date,value:key==='active_users'?Math.max(...values):values.reduce((a,b)=>a+b,0),first:part[0].date,last:part.at(-1).date});}
  return {weekly:true,items};
}

function usageBarChart(daily,key,label){
  const NS='http://www.w3.org/2000/svg',W=320,H=130,top=18,bottom=20,left=4,right=4;
  const wrap=el('figure',undefined,'usage-chart'),total=daily.reduce((n,d)=>n+(d[key]||0),0),{weekly,items}=usageBuckets(daily,key);
  const cap=el('figcaption');cap.append(el('strong',label+(weekly?' · 주별':'')),el('span',key==='active_users'?'일 최대 '+usageNumber(Math.max(0,...daily.map(d=>d.active_users||0)))+'명':'합계 '+usageNumber(total),'small muted'));
  daily=items.map(i=>({date:i.label,first:i.first,last:i.last,[key]:i.value}));
  wrap.append(cap);
  const max=Math.max(1,...daily.map(d=>d[key]||0)),n=daily.length,plotW=W-left-right,plotH=H-top-bottom,slot=plotW/n,gap=n>60?0.5:2,barW=Math.max(1,slot-gap);
  const svg=document.createElementNS(NS,'svg');svg.setAttribute('viewBox',`0 0 ${W} ${H}`);svg.setAttribute('role','img');svg.setAttribute('aria-label',`${label} 일별 막대 차트, 최댓값 ${max}`);
  const mk=(tag,attrs)=>{const e=document.createElementNS(NS,tag);for(const k in attrs)e.setAttribute(k,attrs[k]);return e;};
  svg.append(mk('line',{x1:left,x2:W-right,y1:top+plotH+.5,y2:top+plotH+.5,class:'usage-axis'}));
  const tip=el('div','','usage-tip');tip.hidden=true;
  const r=Math.min(4,barW/2);
  daily.forEach((d,i)=>{
    const v=d[key]||0,h=v?Math.max(2,v/max*plotH):0,x=left+i*slot+gap/2,y=top+plotH-h;
    if(h){const rr=Math.min(r,h);svg.append(mk('path',{class:'usage-bar',d:`M${x},${top+plotH}V${y+rr}Q${x},${y} ${x+rr},${y}H${x+barW-rr}Q${x+barW},${y} ${x+barW},${y+rr}V${top+plotH}Z`}));}
    const hit=mk('rect',{x:left+i*slot,y:top,width:slot,height:plotH,class:'usage-hit'});
    hit.addEventListener('pointerenter',()=>{tip.textContent=`${d.date} · ${usageNumber(v)}`;tip.hidden=false;const pct=(left+i*slot+slot/2)/W*100;tip.style.left=Math.min(85,Math.max(15,pct))+'%';hit.classList.add('active');});
    hit.addEventListener('pointerleave',()=>{tip.hidden=true;hit.classList.remove('active');});
    svg.append(hit);
  });
  const lbl=(x,text,anchor)=>{const t=mk('text',{x,y:H-4,'text-anchor':anchor,class:'usage-tick'});t.textContent=text;svg.append(t);};
  lbl(left,daily[0]?.first.slice(5)||'','start');lbl(W-right,daily.at(-1)?.last.slice(5)||'','end');
  const maxLabel=mk('text',{x:W-right,y:top-6,'text-anchor':'end',class:'usage-tick'});maxLabel.textContent='최대 '+usageNumber(max);svg.append(maxLabel);
  const plot=el('div',undefined,'usage-plot');plot.append(svg,tip);wrap.append(plot);
  return wrap;
}

// 종류별 증가 추이: 종류마다 기간 시작·추가·현재 수와 누적 추이 스파크라인(종류별 단일 계열).
function usageKindGrowth(s){
  const c=card('문서 종류별 증가 추이');
  if(!s.kind_growth?.length){c.append(el('p','아직 문서가 없습니다.','muted'));return c;}
  c.append(table(['종류','기간 시작','추가','현재','누적 추이'],s.kind_growth.map(g=>[usageKinds[g.kind]||g.kind,usageNumber(g.start),g.added?'+'+usageNumber(g.added):'0',usageNumber(g.total),usageSparkline(g.cumulative,s.daily,usageKinds[g.kind]||g.kind)])));
  c.append(el('p','문서의 현재 종류 기준이며 보관(archived) 문서도 포함합니다.','small muted'));
  return c;
}

function usageSparkline(values,daily,label){
  const NS='http://www.w3.org/2000/svg',W=160,H=32,pad=3,min=Math.min(...values),max=Math.max(...values),span=Math.max(1,max-min);
  const svg=document.createElementNS(NS,'svg');svg.setAttribute('viewBox',`0 0 ${W} ${H}`);svg.setAttribute('class','usage-spark');svg.setAttribute('role','img');
  svg.setAttribute('aria-label',`${label} 누적 ${values[0]}건에서 ${values.at(-1)}건`);
  const x=i=>pad+(values.length<2?0:i*(W-2*pad)/(values.length-1)),y=v=>H-pad-(v-min)/span*(H-2*pad);
  const path=document.createElementNS(NS,'path');path.setAttribute('d',values.map((v,i)=>(i?'L':'M')+x(i).toFixed(1)+','+y(v).toFixed(1)).join(''));path.setAttribute('class','usage-spark-line');svg.append(path);
  const end=document.createElementNS(NS,'circle');end.setAttribute('cx',x(values.length-1));end.setAttribute('cy',y(values.at(-1)));end.setAttribute('r',2.5);end.setAttribute('class','usage-spark-dot');svg.append(end);
  const title=document.createElementNS(NS,'title');title.textContent=`${daily[0]?.date} ${usageNumber(values[0])}건 → ${daily.at(-1)?.date} ${usageNumber(values.at(-1))}건`;svg.append(title);
  return svg;
}

// 결과 0건 검색어: 쓰지 않은 지식이나 용어집 동의어 후보.
function usageSearchMisses(s){
  const c=card('결과 0건 검색어');
  c.append(el('p','search_knowledge에서 결과가 없던 검색어입니다. 공백·대소문자를 정리해 묶고, 검색한 사용자는 저장하지 않으며 최대 365일 보관합니다. 자주 나오면 문서를 쓰거나 용어집 동의어를 추가하세요.','small muted'));
  c.append(table(['검색어','횟수','마지막 검색'],(s.search_misses||[]).map(m=>[m.query,usageNumber(m.count),new Date(m.last_at).toLocaleString()])));
  return c;
}

// 검색 시간: 정확(전수) 벡터 검색이 느려지는 시점을 보고 HNSW 도입을 판단한다.
function usageSearchLatency(s){
  const c=card('검색 시간');
  c.append(el('p','search_knowledge와 작업 시작 맥락 검색의 단계별 시간(ms)입니다. 벡터 p95가 커지고 비교한 청크 수가 늘면 HNSW 인덱스 도입을 검토하세요. 검색어·사용자는 저장하지 않으며 결과 0건 검색어와 같은 기간 보관합니다.','small muted'));
  const ms=v=>v==null?'—':usageNumber(v);
  c.append(table(['검색 방식','횟수','p50','p95','최대','임베딩 평균','벡터 p95','키워드 평균','비교 청크(최대)','마지막 검색'],(s.search_latency||[]).map(l=>[l.retrieval,usageNumber(l.searches),ms(l.p50_ms),ms(l.p95_ms),ms(l.max_ms),ms(l.avg_embed_ms),ms(l.p95_vector_ms),ms(l.avg_lexical_ms),usageNumber(l.max_scanned_chunks),new Date(l.last_at).toLocaleString()])));
  return c;
}

function usageQuality(q){
  const c=card('검색 품질 (맥락 피드백)');
  if(!q){c.append(el('p','검색 품질 지표를 불러오지 못했습니다.','muted'));return c;}
  if(!q.tasks_with_feedback){c.append(el('p',`이 기간에 피드백이 기록된 작업이 없습니다. finish_task의 used_sources를 남기면 정확도·적중률이 집계됩니다. (시작된 작업 ${usageNumber(q.tasks_started)}건)`,'muted'));return c;}
  const row=el('div',undefined,'usage-quality');
  for(const [label,value,note]of [['정확도',usagePercent(q.precision),'제공한 출처 중 사용 비율'],['포괄률',usagePercent(q.coverage),'사용한 근거 중 제공됐던 비율'],['적중률',usagePercent(q.hit_rate),'제공 출처를 하나 이상 쓴 작업'],['MRR',q.mean_reciprocal_rank??'—','가장 먼저 쓴 출처 순위의 역수 평균']]){const b=el('div',undefined,'usage-kpi');b.append(el('span',label,'small muted'),el('strong',String(value)),el('span',note,'small muted'));row.append(b);}
  c.append(row,el('p',`피드백 표본 ${usageNumber(q.tasks_with_feedback)}건 / 시작 ${usageNumber(q.tasks_started)}건`,'small muted'));
  return c;
}

function usageTables(s){
  const wrap=el('div',undefined,'usage-tables'),inv=s.inventory;
  const actions=card('많이 쓴 도구'),max=Math.max(1,...s.top_actions.map(a=>a.count));
  actions.append(table(['도구','호출'],s.top_actions.map(a=>{const cell=el('div',undefined,'usage-inline');const bar=el('span',undefined,'usage-inline-bar');bar.style.width=(a.count/max*100)+'%';cell.append(bar,el('span',usageNumber(a.count)));return[el('code',a.key),cell];})));
  const users=card('활동 사용자');
  users.append(table(['사용자','호출','작성·수정','작업','마지막 활동'],s.top_users.map(u=>[u.name,usageNumber(u.calls),usageNumber(u.document_writes),usageNumber(u.tasks_started),u.last_active_at?new Date(u.last_active_at).toLocaleString():'—'])));
  const docs=card('많이 열람한 문서');
  docs.append(table(['문서','종류','상태','열람'],s.top_documents.map(d=>[d.title,usageKinds[d.kind]||d.kind,usageStatuses[d.status]||d.status,usageNumber(d.reads)])));
  const stock=card('현재 보유 현황'),emb=inv.embeddings_total?Math.round(inv.embeddings_ready/inv.embeddings_total*100)+'%':'—';
  stock.append(table(['항목','값'],[
    ['문서 종류별',inv.by_kind.map(k=>(usageKinds[k.key]||k.key)+' '+usageNumber(k.count)).join(' · ')||'—'],
    ['문서 상태별',inv.by_status.map(k=>(usageStatuses[k.key]||k.key)+' '+usageNumber(k.count)).join(' · ')||'—'],
    ['열린 이슈·질의',usageNumber(inv.open_issues)+'건 (기간 내 등록 '+usageNumber(s.totals.issues_opened)+'건)'],
    ['임베딩',`${usageNumber(inv.embeddings_ready)} / ${usageNumber(inv.embeddings_total)} 준비 (${emb})`+(inv.embeddings_failed?` · 실패 ${usageNumber(inv.embeddings_failed)}`:'')],
    ['참여자',usageNumber(inv.members)+'명']
  ]));
  wrap.append(actions,users,docs,stock);
  const note=el('p','호출·검색·열람·활성 사용자는 접근 기록 기준이라 감사 로그 보관 기간(AUDIT_RETENTION_DAYS) 안의 기록만 집계됩니다. 문서 작성·수정은 버전 기록 기준입니다.','small muted usage-footnote');
  const root=el('div');root.append(wrap,note);return root;
}
