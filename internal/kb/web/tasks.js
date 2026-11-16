'use strict';

// 진행 중 작업: task runs started with start_task, their state, linked pull
// requests and issues, and the starting context snapshot. Open tasks older
// than staleTaskDays are flagged so they get finished or closed.
const taskStatusLabels={open:'진행 중',completed:'완료',abandoned:'중단'};
const taskOutcomeLabels={completed:'완료',partial:'부분 완료',blocked:'진행 불가',cancelled:'취소'};
const taskPRStateLabels={unknown:'확인 전',open:'열림',closed:'닫힘',merged:'병합',deployed:'배포'};
const staleTaskDays=7;

async function tasksPage() {
  const root=el('div',undefined,'task-page');
  if (!projectList.length) { root.append(el('p','먼저 프로젝트에 참여해 주세요.','empty')); return root; }
  const project=projectSelect(projectList);
  const current=()=>projectList.find(p=>p.id===project.value);
  const controls=card();
  controls.append(el('p','AI가 start_task로 시작하고 아직 finish_task로 마치지 않은 작업이 "진행 중"입니다. 7일 넘게 열린 작업은 다른 곳에서 끝났는지 확인한 뒤 완료 기록을 남기거나 중단 처리하세요.','muted'));
  const filters=el('div',undefined,'vector-controls');
  const status=selectField(filters,'상태','status',[['open','진행 중'],['completed','완료'],['abandoned','중단'],['','전체']]);
  const mineLabel=el('label',undefined,'inline-check'), mine=el('input');
  mine.type='checkbox';mineLabel.append(mine,document.createTextNode('내가 시작한 작업만'));filters.append(mineLabel);
  const summary=el('p','','small muted');summary.setAttribute('role','status');
  const links=el('div',undefined,'actions');
  controls.append(filters,summary,links);
  const list=el('div',undefined,'task-list'), more=el('div',undefined,'actions');
  root.append(controls,list,more);
  let generation=0, offset=0, shown=0;
  const pageSize=50;

  async function refresh() {
    const ticket=++generation, p=current();
    offset=0;shown=0;
    list.replaceChildren(el('p','불러오는 중…','muted'));more.replaceChildren();summary.textContent='';
    const [batch]=await Promise.all([load(p),renderLinks(p,ticket)]);
    if (ticket!==generation) return;
    list.replaceChildren();
    append(p,batch);
    if (p.id===currentProjectID) refreshTaskBadge();
  }
  async function load(p) {
    const batch=await tool('list_task_runs',{project_id:p.id,status:status.value,mine:mine.checked,limit:pageSize,offset});
    offset+=batch.length;
    return batch;
  }
  function append(p,batch) {
    shown+=batch.length;
    const stale=[...list.querySelectorAll('.task-card.stale')].length+batch.filter(isStale).length;
    summary.textContent=`${taskStatusLabels[status.value]||'전체'} ${shown}건${batch.length===pageSize?' 이상':''}`+(stale?` · 7일 넘게 열린 작업 ${stale}건`:'');
    if (!shown) { list.replaceChildren(el('p',status.value==='open'?'진행 중인 작업이 없습니다.':'표시할 작업이 없습니다.','empty')); return; }
    list.append(...batch.map(r=>taskCard(p,r)));
    more.replaceChildren();
    if (batch.length===pageSize) more.append(btn('더 불러오기',async()=>{const ticket=generation,next=await load(p);if(ticket===generation)append(p,next);}));
  }
  async function renderLinks(p,ticket) {
    const items=[];
    try {
      const drafts=await tool('count_documents',{project_id:p.id,status:'draft'});
      if (drafts.count) items.push(btn(`검토 대기 draft ${drafts.count}건 보기`,()=>render('reviews')));
    } catch {}
    if (p.owner_id===me.id) items.push(btn('오래된 작업 정리',()=>cleanupDialog(root,p,refresh)));
    if (ticket===generation) links.replaceChildren(...items);
  }
  status.addEventListener('change',()=>refresh().catch(e=>notify(e.message,true)));
  mine.addEventListener('change',()=>refresh().catch(e=>notify(e.message,true)));
  project.addEventListener('change',()=>refresh().catch(e=>notify(e.message,true)));
  await refresh();
  return root;

  function taskCard(p,r) {
    const c=card();c.classList.add('task-card');
    if (isStale(r)) c.classList.add('stale');
    const head=el('div',undefined,'issue-head');
    head.append(el('span',taskStatusLabels[r.status]||r.status,'pill task-'+r.status));
    if (isStale(r)) head.append(el('span',`${r.age_days}일째 열림`,'pill stale'));
    if (r.completion?.outcome && r.completion.outcome!=='completed') head.append(el('span',taskOutcomeLabels[r.completion.outcome]||r.completion.outcome,'pill'));
    head.append(el('h2',r.title));
    c.append(head);
    const when=r.status==='open'?`시작 ${new Date(r.started_at).toLocaleString()} (${ageLabel(r.age_days)})`
      :r.status==='completed'?`완료 ${new Date(r.completed_at).toLocaleString()}`:`중단 ${r.closed_at?new Date(r.closed_at).toLocaleString():''}`;
    c.append(el('p',`${r.created_by_name||'알 수 없음'} · ${when}`,'small muted'));
    c.append(el('p',r.task,'task-text'));
    if (r.branch||r.worktree||r.paths?.length) {
      const scope=[r.branch&&`브랜치 ${r.branch}`,r.worktree&&`worktree ${r.worktree}`,r.paths?.length&&`경로 ${r.paths.join(', ')}`].filter(Boolean);
      c.append(el('p',scope.join(' · '),'small muted task-scope'));
    }
    if (r.status==='abandoned'&&r.closed_reason) c.append(el('p',`중단 사유: ${r.closed_reason}`+(r.closed_by_name?` (${r.closed_by_name})`:''),'small'));
    const facts=el('div',undefined,'task-facts');
    for (const pr of r.pull_requests||[]) {
      const a=el('a',`${pr.repository}#${pr.number} · ${taskPRStateLabels[pr.state]||pr.state}`,'pill task-pr task-pr-'+pr.state);
      a.href=pr.url;a.target='_blank';a.rel='noopener noreferrer';facts.append(a);
    }
    if (r.todo) {
      const b=btn(`할 일 · ${r.todo.title} (${todoStatusLabel[r.todo.status]||r.todo.status})`,()=>render('todos'),'secondary task-todo');
      b.title='할 일 보드로 이동';facts.append(b);
    }
    if (r.open_issues) {
      const b=btn(`열린 질의 ${r.open_issues}건`,()=>render('issues'),'secondary task-issues');
      facts.append(b);
    }
    for (const d of r.completion?.documents||[]) facts.append(el('span',`${d.kind==='history'?'작업 이력':d.kind}: ${d.title}`,'pill'));
    if (facts.childElementCount) c.append(facts);
    const actions=el('div',undefined,'actions');
    actions.append(btn('시작 자료 보기',()=>taskDialog(c,p,r)));
    if (r.status==='open'&&(r.created_by===me.id||['owner','admin'].includes(p.role))&&['owner','admin','editor'].includes(p.role))
      actions.append(btn(r.todo?'할 일 변경':'할 일 연결',()=>todoLinkForm(c,p,r,refresh)));
    if (r.status==='open'&&(r.created_by===me.id||p.owner_id===me.id)&&['owner','admin','editor'].includes(p.role)) {
      const form=el('div',undefined,'task-close');form.hidden=true;
      const label=el('label','중단 사유 (필수, 작업에 기록되며 다시 완료할 수 없음)'),reason=el('textarea');
      reason.rows=2;reason.maxLength=1000;label.append(reason);
      const confirmRow=el('div',undefined,'actions');
      confirmRow.append(btn('중단 확정',async()=>{
        const text=reason.value.trim();
        if (!text) { reason.focus(); throw Error('중단 사유를 입력해 주세요.'); }
        await tool('close_task_run',{project_id:p.id,task_id:r.id,reason:text});
        notify('작업을 중단 처리했습니다.');
        await refresh();
      },'danger'),btn('취소',()=>{form.hidden=true;reason.value='';}));
      form.append(label,confirmRow);
      actions.append(btn('중단 처리',()=>{form.hidden=false;reason.focus();},'danger'));
      c.append(actions,form);
      return c;
    }
    c.append(actions);
    return c;
  }
}

// Inline picker that links an open task to a not-completed to-do (link_task_todo).
async function todoLinkForm(cardEl,p,r,after){
  cardEl.querySelector('.task-todo-form')?.remove();
  const form=el('div',undefined,'task-todo-form');
  const todos=(await tool('list_todos',{project_id:p.id})).filter(t=>t.status!=='completed'||t.id===r.todo?.id);
  const label=el('label','이 작업이 수행하는 할 일 (연결하면 시작 전·아이디어 할 일은 진행 중으로, 작업이 완료되면 완료로 이동)');
  const select=el('select');select.append(new Option('연결 안 함',''));
  for(const t of todos)select.append(new Option(`${t.title} · ${todoPriorityLabel[t.priority]} · ${todoStatusLabel[t.status]}`,t.id));
  select.value=r.todo?.id||'';label.append(select);
  const row=el('div',undefined,'actions');
  row.append(btn('저장',async()=>{
    const out=await tool('link_task_todo',{project_id:p.id,task_id:r.id,todo_id:select.value});
    notify(out.todo?`“${out.todo.title}”에 연결했습니다.`:'할 일 연결을 해제했습니다.');
    await after();
  }),btn('취소',()=>form.remove()));
  form.append(label,row);
  if(!todos.length)form.prepend(el('p','연결할 수 있는 할 일이 없습니다. 할 일 보드에서 먼저 추가하세요.','small muted'));
  cardEl.append(form);select.focus();
}

function isStale(r){return r.status==='open'&&r.age_days>=staleTaskDays;}
function ageLabel(days){return days<1?'오늘':`${days}일 전`;}

// Shows the snapshot start_task stored: rules, sources, issues and completion.
function taskDialog(parent,p,r) {
  const dialog=el('dialog',undefined,'review-dialog'), content=el('div',undefined,'review-dialog-body');
  content.append(el('p','불러오는 중…','muted'));
  const close=btn('닫기',()=>dialog.close(),'secondary');
  dialog.append(el('h2',r.title),el('p',`${r.created_by_name||''} · 요청 키 ${r.request_key}`,'small muted'),content,el('div',undefined,'actions review-dialog-actions'));
  dialog.lastElementChild.append(close);
  dialog.addEventListener('close',()=>dialog.remove(),{once:true});
  dialog.addEventListener('click',e=>{if(e.target!==dialog)return;const b=dialog.getBoundingClientRect();if(e.clientX<b.left||e.clientX>b.right||e.clientY<b.top||e.clientY>b.bottom)dialog.close();});
  parent.append(dialog);dialog.showModal();close.focus();
  tool('get_task_run',{project_id:p.id,task_id:r.id}).then(run=>content.replaceChildren(...taskDetail(run))).catch(e=>content.replaceChildren(el('p',e.message,'muted')));
}

function taskDetail(run) {
  const out=[], ctx=run.context||{};
  out.push(el('h3','요청 내용'),el('p',run.task));
  out.push(el('p','아래는 작업을 시작할 때 저장한 자료입니다. 지금의 지식과 다를 수 있습니다.','small muted'));
  const rules=[...(ctx.rules?.project_rules||[]),...(ctx.rules?.personal_rules||[])];
  if (rules.length) { out.push(el('h3',`적용된 규칙 ${rules.length}건`)); const ul=el('ul'); rules.forEach(x=>ul.append(el('li',`${x.scope==='personal'?'개별':'소유자'} · ${x.title}`))); out.push(ul); }
  const sources=ctx.sources||[];
  out.push(el('h3',`참고 자료 ${sources.length}건`+(ctx.retrieval?` · ${ctx.retrieval}`:'')));
  if (sources.length) { const ul=el('ul'); sources.forEach(s=>ul.append(el('li',`${s.title} (버전 ${s.revision})`))); out.push(ul); }
  if (ctx.warning) out.push(el('p',ctx.warning,'small'));
  const concurrent=ctx.concurrent_runs||[];
  if (concurrent.length) {
    out.push(el('h3',`시작 시 같은 곳에서 진행 중이던 작업 ${concurrent.length}건`),el('p','같은 브랜치·worktree 또는 겹치는 경로의 다른 열린 작업입니다. 차단하지 않는 경고입니다.','small muted'));
    const ul=el('ul');
    concurrent.forEach(x=>ul.append(el('li',`${x.title} · ${x.created_by_name||'알 수 없음'} · ${new Date(x.started_at).toLocaleString()}${x.stale?' · 7일 넘게 열림(stale)':''} · `+(x.overlaps||[]).map(o=>o.kind==='path'?`경로 ${o.value}${o.theirs&&o.theirs!==o.value?` ↔ ${o.theirs}`:''}`:`${o.kind==='branch'?'브랜치':'worktree'} ${o.value}`).join(', '))));
    out.push(ul);
  }
  const issues=ctx.issues;
  if (issues) {
    const n=(issues.assigned_to_you?.length||0)+(issues.your_questions?.length||0)+(issues.related?.length||0);
    out.push(el('p',`시작 시 질의·이슈: 배정 ${issues.assigned_to_you?.length||0} · 내가 남긴 ${issues.your_questions?.length||0} · 관련 ${issues.related?.length||0}`+(issues.blocking_open?` · 보류 ${issues.blocking_open}`:''),n?'small':'small muted'));
  }
  const done=run.completion;
  if (done) {
    out.push(el('h3',`완료 기록 · ${taskOutcomeLabels[done.outcome]||done.outcome}`));
    const ul=el('ul');
    (done.documents||[]).forEach(d=>ul.append(el('li',`${d.kind} · ${d.title} (버전 ${d.revision})`)));
    (done.pull_requests||[]).forEach(u=>{const li=el('li'),a=el('a',u);a.href=u;a.target='_blank';a.rel='noopener noreferrer';li.append(a);ul.append(li);});
    out.push(ul);
  }
  return out;
}

// Owner-only: preview open tasks older than N days, then close them with a reason.
function cleanupDialog(parent,p,after) {
  const dialog=el('dialog',undefined,'review-dialog'), body=el('div',undefined,'review-dialog-body');
  const form=el('form');
  const days=field(form,'며칠 넘게 열린 작업','older_than_days','number',String(staleTaskDays));days.min='1';days.max='365';
  const close=btn('닫기',()=>dialog.close(),'secondary');
  dialog.append(el('h2','오래된 작업 정리'),el('p','먼저 미리보기로 대상을 확인합니다. 다른 곳에서 끝난 작업은 시작한 사람이 finish_task로 기록하는 게 좋습니다.','small muted'),form,body,el('div',undefined,'actions review-dialog-actions'));
  dialog.lastElementChild.append(close);
  dialog.addEventListener('close',()=>dialog.remove(),{once:true});
  formSubmit(form,'미리보기',async d=>{
    const n=Number(d.older_than_days);
    const preview=await tool('cleanup_stale_task_runs',{project_id:p.id,older_than_days:n});
    body.replaceChildren(el('p',`대상 ${preview.total_open_stale}건`+(preview.total_open_stale>preview.tasks.length?` (한 번에 ${preview.tasks.length}건까지 정리)`:''),'small'));
    if (!preview.tasks.length) return;
    body.append(table(['작업','시작한 사람','경과'],preview.tasks.map(t=>[t.title,t.created_by_name,`${t.age_days}일`])));
    const label=el('label','정리 사유 (필수, 각 작업에 기록)'),reason=el('textarea');reason.rows=2;reason.maxLength=1000;label.append(reason);
    body.append(label,btn(`${preview.tasks.length}건 중단 처리`,async()=>{
      const text=reason.value.trim();
      if (!text) { reason.focus(); throw Error('정리 사유를 입력해 주세요.'); }
      if (!confirm(`${preview.tasks.length}건을 중단 처리할까요? 다시 완료할 수 없습니다.`)) return;
      const result=await tool('cleanup_stale_task_runs',{project_id:p.id,older_than_days:n,apply:true,reason:text});
      notify(`${result.closed}건을 중단 처리했습니다.`);
      dialog.close();
      await after();
    },'danger'));
  });
  parent.append(dialog);dialog.showModal();
}

// Sidebar badge: open tasks in the current project.
let taskBadgeTicket=0;
async function refreshTaskBadge(){const b=$('#task-badge'),ticket=++taskBadgeTicket,id=currentProjectID;if(!b)return;if(!id||!me){b.hidden=true;return;}try{const runs=await tool('list_task_runs',{project_id:id,status:'open',limit:100});if(ticket!==taskBadgeTicket)return;const n=runs.length;b.textContent=n>99?'99+':String(n);b.hidden=!n;b.parentElement.title=n?`진행 중 작업 ${n>=100?'100건 이상':n+'건'}`:'';}catch{if(ticket===taskBadgeTicket)b.hidden=true;}}
