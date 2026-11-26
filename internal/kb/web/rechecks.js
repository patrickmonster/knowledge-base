'use strict';

// 재확인 · 맥락 피드백 화면.
// 재확인: list_documents(stale_only) + list_recheck_assignments + get_recheck_digest, 담당 지정·확인은 assign_recheck_owner·confirm_recheck.
// 맥락 피드백: get_retrieval_metrics(검색 품질)와 list_task_runs(작업별 피드백 기록 시각·찾지 못한 지식). 이 카드는 조회 전용입니다.
// 쓰기 권한은 서버가 판정합니다(편집자 이상). 조회자에게는 동작 버튼을 보여 주지 않고, 우회하지 않습니다.
const rcKinds={brief:'기획',decision:'결정',history:'작업 이력',artifact:'산출물',reference:'참고',glossary:'용어집',wiki:'위키'};
const rcWriters=['owner','admin','editor'];
const rcNum=n=>Number(n||0).toLocaleString('ko-KR');
const rcPct=v=>v===null||v===undefined?'—':Math.round(v*100)+'%';
const rcDate=v=>v?new Date(v).toLocaleString():'—';
const rcDefaultDays=90;

async function rechecksPage(){
  const projects=[];
  for(let offset=0;offset<=100000;offset+=50){const batch=await tool('list_projects',{limit:50,offset});projects.push(...batch);if(batch.length<50)break;}
  const root=el('div',undefined,'recheck-page');
  if(!projects.length){root.append(el('p','먼저 프로젝트에 참여해 주세요.','empty'));return root;}
  const project=projectSelect(projects),current=projects.find(p=>p.id===project.value)||projects[0],canEdit=rcWriters.includes(current.role);
  root.append(el('p',`${current.name} · ${roleLabels[current.role]||current.role}`+(canEdit?'':' · 조회 전용: 담당 지정과 확인 기록은 편집자 이상만 할 수 있습니다.'),'small muted'));
  const recheck=rcRecheckCard(current,canEdit),feedback=rcFeedbackCard(current);
  root.append(recheck.card,feedback.card);
  await Promise.all([recheck.ready,feedback.ready]);
  return root;
}

function rcKpi(label,value,note){const b=el('div',undefined,'usage-kpi');b.append(el('span',label,'small muted'),el('strong',String(value)),el('span',note,'small muted'));return b;}
function rcKindPill(kind){return el('span',rcKinds[kind]||kind,'pill');}
function rcTitleCell(title,kind){const c=el('div',undefined,'document-title');c.append(el('strong',title));if(kind)c.append(rcKindPill(kind));return c;}

// ---- 재확인 ----

function rcRecheckCard(project,canEdit){
  const c=card('오래된 지식 재확인');
  c.append(el('p','active 상태의 reference·brief 문서가 기준 일수 넘게 수정되지 않으면 재확인 대상입니다. 담당자를 지정하면 마지막 수정과 마지막 확인 중 늦은 시각부터 확인 주기가 지나면 기한이 됩니다.','small muted'));
  const form=el('form',undefined,'recheck-controls'),days=field(form,'재확인 기준 (일, 1~3650)','stale_days','number',String(rcDefaultDays));
  days.min=1;days.max=3650;days.step=1;
  let summaryText='',generation=0;
  const body=el('div'),status=el('p','','small muted');status.setAttribute('role','status');
  formSubmit(form,'조회',()=>refresh());
  const copy=btn('요약 복사',async()=>{if(!summaryText)return;await navigator.clipboard.writeText(summaryText);notify('재확인 요약을 복사했습니다.');});
  form.append(copy);
  c.append(form,status,body);

  async function refresh(){
    const n=Number(days.value);
    if(!Number.isInteger(n)||n<1||n>3650)throw Error('재확인 기준은 1~3650일의 정수입니다.');
    const ticket=++generation;
    body.replaceChildren(el('p','불러오는 중…','muted'));
    const [stale,assignments,digest,members]=await Promise.all([
      tool('list_documents',{project_id:project.id,stale_only:true,stale_days:n,limit:100,sort:'updated_at',order:'asc'}),
      tool('list_recheck_assignments',{project_id:project.id,limit:100}),
      tool('get_recheck_digest',{project_id:project.id,include_unassigned:true,limit:100}),
      canEdit?tool('list_project_members',{project_id:project.id}):Promise.resolve([])
    ]);
    if(ticket!==generation)return;
    summaryText=digest.summary||'';copy.disabled=!summaryText;
    const owners=members.filter(m=>rcWriters.includes(m.role)&&!m.disabled),byDoc=new Map(assignments.map(a=>[a.document_id,a]));
    const byOwner=digest.by_owner||[],dueTotal=byOwner.reduce((s,g)=>s+g.total,0);
    status.textContent=`기준 ${n}일 · 생성 ${rcDate(digest.generated_at)}`;
    const kpis=el('div',undefined,'usage-quality');
    kpis.append(
      rcKpi('재확인 필요 문서',stale.length>=100?'100+':rcNum(stale.length),`${n}일 넘게 수정되지 않음`),
      rcKpi('기한 지난 담당 문서',rcNum(dueTotal),`담당자 ${rcNum(byOwner.length)}명`),
      rcKpi('담당자 없음',rcNum(digest.unassigned_total),`${rcDefaultDays}일 넘게 수정되지 않은 문서`),
      rcKpi('담당 지정',assignments.length>=100?'100+':rcNum(assignments.length),'프로젝트의 지정 건수')
    );
    const parts=[kpis];
    parts.push(el('h3','기한 지난 담당 문서'));
    const dueRows=[];
    for(const g of byOwner)for(const d of g.due||[])dueRows.push([
      g.owner_name,rcTitleCell(d.title,d.kind),
      rcDueCell(d.days_overdue,d.interval_days),
      rcDate(d.last_verified_at),d.note||'—',
      ...(canEdit?[rcActions({id:d.document_id,title:d.title},byDoc.get(d.document_id),true)]:[])
    ]);
    parts.push(table(['담당자','문서','기한','마지막 확인·수정','확인할 것',...(canEdit?['']:[])],dueRows));
    const omitted=byOwner.filter(g=>g.total>(g.due||[]).length).map(g=>`${g.owner_name} ${g.total-(g.due||[]).length}건`);
    if(omitted.length)parts.push(el('p','표시하지 않은 문서: '+omitted.join(' · ')+'. 요약 복사로 전체 현황을 받을 수 있습니다.','small muted'));
    parts.push(el('h3',`재확인 필요 문서 (기준 ${n}일)`));
    const staleRows=stale.map(d=>{
      const a=byDoc.get(d.id);
      const owner=el('div');
      if(a){owner.append(el('span',a.owner_name),el('span',` · ${rcNum(a.interval_days)}일마다`,'small muted'));const overdue=new Date(a.next_due_at)<new Date();const due=el('span',(overdue?'기한 지남 ':'다음 기한 ')+new Date(a.next_due_at).toLocaleDateString(),'pill'+(overdue?' stale':''));owner.append(el('br'),due);}
      else owner.append(el('span','담당자 없음','muted'));
      const last=el('div');
      if(a?.last_checked_at){last.append(el('span',rcDate(a.last_checked_at)));if(a.check_note)last.append(el('p',a.check_note,'small muted recheck-note'));}
      else last.append(el('span','확인 기록 없음','muted'));
      const updated=el('div');updated.append(el('span',rcDate(d.updated_at)),el('span',`${d.days_since_update}일 전`,'small muted document-age'));
      return [rcTitleCell(d.title,d.kind),owner,last,updated,...(canEdit?[rcActions({id:d.id,title:d.title},a,false)]:[])];
    });
    parts.push(table(['문서','담당자 · 주기','마지막 확인','마지막 수정',...(canEdit?['']:[])],staleRows));
    if(stale.length>=100)parts.push(el('p','가장 오래된 100건만 표시합니다. 기준 일수를 늘리면 더 오래된 문서만 볼 수 있습니다.','small muted'));
    if(assignments.length>=100)parts.push(el('p','담당 지정이 100건 이상이라 일부 문서의 담당자가 표시되지 않을 수 있습니다.','small muted'));
    if(!canEdit)parts.push(el('p','담당 지정과 확인 기록은 프로젝트 편집자·관리자·소유자만 할 수 있습니다.','small muted'));
    body.replaceChildren(...parts);

    function rcActions(doc,assignment,showConfirm){
      const box=el('div',undefined,'actions recheck-actions');
      if(assignment||showConfirm)box.append(btn('확인함',async()=>rcConfirmDialog(c,project,doc,refresh),'secondary'));
      box.append(btn(assignment?'담당 변경':'담당 지정',async()=>rcAssignDialog(c,project,doc,assignment,owners,refresh),'secondary'));
      return box;
    }
  }
  return {card:c,ready:refresh()};
}

function rcDueCell(overdue,interval){const c=el('div');c.append(el('span',`${rcNum(overdue)}일 지남`,'pill stale'),el('span',` 주기 ${rcNum(interval)}일`,'small muted'));return c;}

function rcDialog(host,title,doc){
  const dialog=el('dialog',undefined,'recheck-dialog');
  dialog.append(el('h2',title),el('p',doc.title,'small muted recheck-doc'));
  dialog.addEventListener('close',()=>dialog.remove(),{once:true});
  host.append(dialog);
  return dialog;
}
function rcCancel(dialog){return btn('취소',()=>dialog.close(),'secondary');}

// assign_recheck_owner: 담당자는 활성 소유자·관리자·편집자만 서버가 허용합니다.
function rcAssignDialog(host,project,doc,assignment,owners,refresh){
  if(!owners.length)throw Error('담당자로 지정할 수 있는 참여자(소유자·관리자·편집자)가 없습니다.');
  const dialog=rcDialog(host,assignment?'재확인 담당 변경':'재확인 담당 지정',doc),f=el('form');
  const owner=selectField(f,'담당자','owner_id',owners.map(m=>[m.user_id,`${m.name} (${roleLabels[m.role]||m.role})`]));
  const preferred=assignment?.owner_id||me.id;if(owners.some(m=>m.user_id===preferred))owner.value=preferred;
  const interval=field(f,'확인 주기 (일, 1~3650)','interval_days','number',String(assignment?.interval_days||rcDefaultDays));interval.min=1;interval.max=3650;interval.step=1;
  const noteLabel=el('label','확인할 것 (선택, 500자 이내)'),note=el('textarea');note.name='note';note.rows=3;note.maxLength=500;note.value=assignment?.note||'';noteLabel.append(note);f.append(noteLabel);
  formSubmit(f,'저장',async d=>{
    await tool('assign_recheck_owner',{project_id:project.id,document_id:doc.id,owner_id:d.owner_id,interval_days:Number(d.interval_days),note:d.note});
    notify('재확인 담당자를 저장했습니다.');dialog.close();await refresh();
  });
  f.append(rcCancel(dialog));
  if(assignment)f.append(btn('담당 해제',async()=>{
    if(!confirm('이 문서의 재확인 담당 지정을 해제할까요?'))return;
    await tool('assign_recheck_owner',{project_id:project.id,document_id:doc.id,remove:true});
    notify('담당 지정을 해제했습니다.');dialog.close();await refresh();
  },'danger'));
  dialog.append(f);dialog.showModal();
}

// confirm_recheck: 내용을 바꾸지 않고 "확인함"만 기록합니다. 실제로 확인한 뒤에만 누릅니다.
function rcConfirmDialog(host,project,doc,refresh){
  const dialog=rcDialog(host,'재확인 완료 기록',doc),f=el('form');
  f.append(el('p','문서가 지금도 맞는지 실제로 확인한 뒤에만 기록하세요. 내용이 달라졌다면 확인 대신 문서를 수정해야 합니다.','small'));
  const label=el('label','무엇을 어떻게 확인했나요? (1~1000자)'),note=el('textarea');note.name='note';note.rows=4;note.required=true;note.maxLength=1000;label.append(note);f.append(label);
  formSubmit(f,'확인함으로 기록',async d=>{
    await tool('confirm_recheck',{project_id:project.id,document_id:doc.id,note:d.note});
    notify('재확인을 기록했습니다.');dialog.close();await refresh();
  });
  f.append(rcCancel(dialog));
  dialog.append(f);dialog.showModal();
}

// ---- 맥락 피드백 ----

function rcFeedbackCard(project){
  const c=card('맥락 피드백 (검색 품질)');
  c.append(el('p','작업을 마칠 때 남긴 used_sources·misleading_sources·missing으로 start_task가 제공한 근거의 품질을 집계합니다. 피드백은 작업을 시작한 사용자만 finish_task나 record_context_feedback으로 기록하며, 이 화면은 조회 전용입니다.','small muted'));
  let days=30;try{days=Number(localStorage.getItem('kb.feedback.days'))||30;}catch{}
  const controls=el('div',undefined,'usage-controls'),range=el('div',undefined,'usage-range'),body=el('div');
  range.setAttribute('role','group');range.setAttribute('aria-label','조회 기간');
  for(const d of [7,30,90,365]){const b=el('button',d===365?'1년':d+'일','secondary');b.type='button';b.dataset.days=d;b.addEventListener('click',()=>{days=d;try{localStorage.setItem('kb.feedback.days',String(d));}catch{}refresh().catch(e=>notify(e.message,true));});range.append(b);}
  controls.append(range,btn('새로고침',()=>refresh()));
  c.append(controls,body);
  let generation=0;
  async function refresh(){
    const ticket=++generation;
    range.querySelectorAll('button').forEach(b=>b.setAttribute('aria-pressed',String(Number(b.dataset.days)===days)));
    body.replaceChildren(el('p','불러오는 중…','muted'));
    const [q,runs]=await Promise.all([tool('get_retrieval_metrics',{project_id:project.id,days}),rcFeedbackRuns(project.id,days)]);
    if(ticket!==generation)return;
    body.replaceChildren(...rcFeedbackBody(q,runs));
  }
  return {card:c,ready:refresh()};
}

// list_task_runs는 최근 시작 순이므로 최대 200건 안에서 기간 내 피드백이 있는 작업을 고릅니다.
async function rcFeedbackRuns(projectID,days){
  const since=Date.now()-days*86400000,out=[];
  for(let offset=0;offset<200;offset+=100){
    const batch=await tool('list_task_runs',{project_id:projectID,limit:100,offset});
    out.push(...batch);if(batch.length<100)break;
  }
  return out.filter(r=>r.feedback_at&&new Date(r.feedback_at).getTime()>=since).sort((a,b)=>new Date(b.feedback_at)-new Date(a.feedback_at)).slice(0,10);
}

const rcRunStatus={open:'진행 중',completed:'완료',abandoned:'중단'};

function rcFeedbackBody(q,runs){
  if(!q.tasks_with_feedback)return [el('p',`이 기간에 피드백이 기록된 작업이 없습니다. finish_task의 used_sources를 남기면 정확도·적중률이 집계됩니다. (시작된 작업 ${rcNum(q.tasks_started)}건)`,'muted')];
  const parts=[],kpis=el('div',undefined,'usage-quality');
  kpis.append(rcKpi('정확도',rcPct(q.precision),'제공한 출처 중 사용 비율'),rcKpi('포괄률',rcPct(q.coverage),'사용한 근거 중 제공됐던 비율'),rcKpi('적중률',rcPct(q.hit_rate),'제공 출처를 하나 이상 쓴 작업'),rcKpi('MRR',q.mean_reciprocal_rank??'—','가장 먼저 쓴 출처 순위의 역수 평균'));
  parts.push(kpis,el('p',`피드백 표본 ${rcNum(q.tasks_with_feedback)}건 / 시작 ${rcNum(q.tasks_started)}건 · 최근 ${rcNum(q.days)}일`,'small muted'));
  parts.push(el('h3','출처 활용도'),rcUtilization(q));
  parts.push(rcUsageTable('많이 쓴 문서',q.most_used,'사용된 문서가 없습니다.'));
  parts.push(rcUsageTable('제공됐지만 쓰이지 않은 문서 (2회 이상 제공)',q.offered_never_used,'해당하는 문서가 없습니다.'));
  parts.push(rcUsageTable('오해 소지로 표시된 문서',q.misleading_sources,'오해 소지로 표시된 문서가 없습니다.'));
  parts.push(el('h3','찾지 못한 지식 (최근 기록)'));
  if(q.recent_missing.length){const ul=el('ul',undefined,'recheck-missing');for(const m of q.recent_missing)ul.append(el('li',m));parts.push(ul);}
  else parts.push(el('p','기록된 항목이 없습니다.','muted'));
  parts.push(el('h3','최근 작업별 피드백'));
  parts.push(table(['작업','시작한 사람','상태','피드백 기록','찾지 못한 지식'],runs.map(r=>[r.title,r.created_by_name||'—',rcRunStatus[r.status]||r.status,rcDate(r.feedback_at),r.feedback_note||'—'])));
  parts.push(el('p','최근 시작한 작업 200건 안에서 피드백이 있는 작업을 최대 10건 보여 줍니다.','small muted'));
  if(q.guidance)parts.push(el('p',q.guidance,'small muted'));
  return parts;
}

// 사용·미사용·오해 소지 비율. 오해 소지는 제공 여부와 무관하게 집계되므로 미사용은 제공 건수에서 사용·오해 소지를 뺀 추정치입니다.
function rcUtilization(q){
  const used=q.used_total,mislead=q.misleading,unused=Math.max(0,q.offered-q.offered_used-q.misleading),total=used+mislead+unused,box=el('div');
  if(!total){box.append(el('p','집계할 출처가 없습니다.','muted'));return box;}
  const items=[['fb-used','사용',used],['fb-unused','미사용',unused],['fb-mislead','오해 소지',mislead]],pct=n=>Math.round(n/total*100)+'%';
  const bar=el('div',undefined,'feedback-bar');bar.setAttribute('role','img');bar.setAttribute('aria-label','출처 활용도: '+items.map(([,l,n])=>`${l} ${pct(n)}`).join(', '));
  for(const [cls,,n]of items){if(!n)continue;const s=el('span',undefined,cls);s.style.flex=String(n);bar.append(s);}
  const legend=el('ul',undefined,'feedback-legend');
  for(const [cls,label,n]of items){const li=el('li'),sw=el('span',undefined,'fb-swatch '+cls);li.append(sw,document.createTextNode(`${label} ${rcNum(n)}건 (${pct(n)})`));legend.append(li);}
  box.append(bar,legend,el('p',`제공 ${rcNum(q.offered)}건 중 ${rcNum(q.offered_used)}건 사용 · 제공되지 않았지만 쓴 근거 ${rcNum(q.used_not_offered)}건`,'small muted'));
  return box;
}

function rcUsageTable(title,rows,empty){
  const box=el('div');box.append(el('h3',title));
  if(!rows.length){box.append(el('p',empty,'muted'));return box;}
  box.append(table(['문서','종류','제공','사용','오해 소지'],rows.map(d=>[el('strong',d.title),rcKinds[d.kind]||d.kind,rcNum(d.offered),rcNum(d.used),rcNum(d.misleading)])));
  return box;
}
