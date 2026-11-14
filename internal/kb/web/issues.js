'use strict';

const issueStatusLabels={open:'답변 대기',answered:'답변됨',resolved:'해결',closed:'닫힘'};
const issueKindLabels={question:'질의',issue:'이슈'};

async function issuesPage() {
  const root=el('div'), projects=[];
  for (let offset=0; offset<=100000; offset+=50) {
    const batch=await tool('list_projects',{limit:50,offset});
    projects.push(...batch); if(batch.length<50)break;
  }
  const controls=card('이슈 · 질의');
  controls.append(el('p','공통 작업처럼 담당자가 따로 있는 일은 바로 결정하지 말고 담당자에게 질의를 남깁니다. 작업을 시작할 때(start_task) 내게 배정된 질의, 내가 남긴 질의의 답변, 관련 이슈가 함께 표시됩니다.','muted'));
  controls.append(el('p','"작업 보류"로 표시한 질의는 답변 전까지 관련 작업을 진행하지 않도록 AI에게 안내됩니다. 해결된 질의응답은 참고 문서로 저장해 지식 검색에 포함할 수 있습니다.','small muted'));
  root.append(controls);
  if (!projects.length) { controls.append(el('p','먼저 프로젝트에 참여해 주세요.','empty')); return root; }
  const filters=el('div',undefined,'vector-controls');
  const project=projectSelect(projects);
  const scope=selectField(filters,'보기','scope',[['all','전체'],['assigned','내게 배정'],['asked','내가 남긴']]);
  const status=selectField(filters,'상태','status',[['active','진행 중 (대기·답변됨)'],['open','답변 대기'],['answered','답변됨'],['resolved','해결'],['closed','닫힘'],['all','모든 상태']]);
  const query=selectQuery(filters);
  controls.append(filters);
  const summary=el('p','','small muted');summary.setAttribute('role','status');controls.append(summary);
  const createCard=card('새 질의 · 이슈'), areaCard=card('공통 작업 영역과 담당자'), notifyCard=el('div'), list=el('div');
  root.append(list,createCard,areaCard,notifyCard);
  let generation=0, members=[], areas=[];
  const current=()=>projects.find(p=>p.id===project.value);
  const canWrite=()=>['owner','admin','editor'].includes(current().role);

  async function loadProject() {
    const ticket=++generation, p=current();
    [members,areas]=await Promise.all([tool('list_project_members',{project_id:p.id}),tool('list_work_areas',{project_id:p.id})]);
    if (ticket!==generation) return;
    renderCreate(); renderAreas(); await refresh();
    const notifications=await notificationCard(p);
    if (ticket===generation||current().id===p.id) notifyCard.replaceChildren(notifications);
  }
  async function refresh() {
    const ticket=++generation, p=current();
    list.replaceChildren(el('p','불러오는 중…','muted'));
    const input={project_id:p.id,status:status.value,limit:50};
    if (scope.value==='assigned') input.assignee_id=me.id;
    if (scope.value==='asked') input.created_by=me.id;
    if (query.value.trim()) input.query=query.value.trim();
    const issues=await tool('list_issues',input);
    if (ticket!==generation) return;
    const blocking=issues.filter(i=>i.blocking&&i.status==='open').length;
    summary.textContent=`${issues.length}건${issues.length===50?' 이상':''}`+(blocking?` · 작업 보류 질의 ${blocking}건`:'')+(canWrite()?'':' · 조회 권한만 있어 답변·변경할 수 없습니다.');
    list.replaceChildren(...(issues.length?issues.map(issueCard):[el('p','조건에 맞는 질의·이슈가 없습니다.','empty')]));
  }

  function issueCard(i) {
    const c=card(), head=el('div',undefined,'issue-head');
    head.append(el('span',issueKindLabels[i.kind],'pill'),el('span',issueStatusLabels[i.status],'pill issue-'+i.status));
    if (i.blocking&&i.status==='open') head.append(el('span','작업 보류','pill error'));
    head.append(el('h2',i.title));
    c.append(head);
    const meta=[i.area_name&&('영역 '+i.area_name),'담당 '+(i.assignee_name||'미지정'),'작성 '+i.created_by_name,new Date(i.updated_at).toLocaleString()];
    if (i.comment_count) meta.push('댓글 '+i.comment_count);
    c.append(el('p',meta.filter(Boolean).join(' · '),'small muted'));
    if (i.tags?.length) c.append(el('p',i.tags.join(' · '),'small'));
    c.append(renderMarkdown(i.body,'vector-content'));
    if (i.answer) {
      const answer=el('div',undefined,'issue-answer');
      answer.append(el('p',`답변 · ${i.answered_by_name||''} · ${i.answered_at?new Date(i.answered_at).toLocaleString():''}`,'small muted'),renderMarkdown(i.answer,'vector-content'));
      c.append(answer);
    }
    if (i.resolution) c.append(el('p','해결 메모: '+i.resolution,'small'));
    if (i.knowledge_document_id) c.append(el('p','지식 문서로 저장됨 · '+i.knowledge_document_id,'small muted'));
    const thread=el('details'), threadBody=el('div');
    thread.append(el('summary','댓글'+(i.comment_count?` ${i.comment_count}개`:'')),threadBody);
    thread.addEventListener('toggle',async()=>{
      if (!thread.open||threadBody.childElementCount) return;
      try {
        const full=await tool('get_issue',{project_id:i.project_id,issue_id:i.id});
        threadBody.replaceChildren(...full.comments.map(cm=>{const n=el('div',undefined,'issue-comment');n.append(el('p',`${cm.author_name} · ${new Date(cm.created_at).toLocaleString()}`,'small muted'),renderMarkdown(cm.body,'vector-content'));return n;}));
        if (!full.comments.length) threadBody.append(el('p','댓글이 없습니다.','small muted'));
      } catch(e) { threadBody.replaceChildren(el('p',e.message,'muted')); }
    });
    c.append(thread);
    if (!canWrite()) return c;

    const text=el('textarea');text.rows=3;text.maxLength=20000;text.placeholder=i.status==='open'||i.status==='answered'?'답변 또는 댓글':'댓글';
    const label=el('label','답변 · 댓글');label.append(text);
    const actions=el('div',undefined,'actions');
    const need=()=>{const v=text.value.trim();if(!v){text.focus();throw Error('내용을 입력해 주세요.');}return v;};
    if (i.status==='open'||i.status==='answered') actions.append(btn(i.answer?'답변 수정':'답변 등록',async()=>{await tool('answer_issue',{project_id:i.project_id,issue_id:i.id,answer:need()});notify('답변을 등록했습니다.');await refresh();},''));
    actions.append(btn('댓글',async()=>{await tool('comment_issue',{project_id:i.project_id,issue_id:i.id,body:need()});notify('댓글을 남겼습니다.');await refresh();}));
    if (i.status==='open'||i.status==='answered') {
      actions.append(btn('해결',async()=>{
        const publish=!!i.answer&&confirm('질의응답을 참고 문서(active)로 저장해 지식 검색에 포함할까요?\n취소를 누르면 저장 없이 해결만 처리합니다.');
        const resolution=text.value.trim();
        await tool('update_issue',{project_id:i.project_id,issue_id:i.id,status:'resolved',publish_answer:publish,...(resolution?{resolution}:{})});
        notify(publish?'해결하고 지식 문서로 저장했습니다.':'해결 처리했습니다.');await refresh();
      }));
      actions.append(btn('닫기',async()=>{if(!confirm('이 항목을 닫을까요? (취소·불필요)'))return;await tool('update_issue',{project_id:i.project_id,issue_id:i.id,status:'closed'});notify('닫았습니다.');await refresh();},'danger'));
    } else {
      actions.append(btn('다시 열기',async()=>{await tool('update_issue',{project_id:i.project_id,issue_id:i.id,status:'open'});notify('다시 열었습니다.');await refresh();}));
      if (i.status==='resolved'&&i.answer) actions.append(btn(i.knowledge_document_id?'지식 문서 갱신':'지식 문서로 저장',async()=>{await tool('update_issue',{project_id:i.project_id,issue_id:i.id,publish_answer:true});notify('지식 문서에 저장했습니다.');await refresh();}));
    }
    if (i.status==='open') actions.append(btn(i.blocking?'보류 해제':'작업 보류로 표시',async()=>{await tool('update_issue',{project_id:i.project_id,issue_id:i.id,blocking:!i.blocking});await refresh();}));
    c.append(label,actions);
    return c;
  }

  function memberOptions(empty) {
    return [...(empty?[['',empty]]:[]),...members.filter(m=>m.role!=='viewer'&&!m.disabled).map(m=>[m.user_id,`${m.name} (${m.role})`])];
  }
  function renderCreate() {
    createCard.replaceChildren(el('h2','새 질의 · 이슈'));
    if (!canWrite()) { createCard.append(el('p','편집자 이상만 질의를 남길 수 있습니다.','small muted')); return; }
    const f=el('form');
    selectField(f,'종류','kind',[['question','질의 — 담당자에게 확인할 내용'],['issue','이슈 — 문제·결함·리스크']]);
    const t=field(f,'제목','title');t.maxLength=200;
    const bl=el('label','내용 (배경, 확인할 점, 검토한 선택지)'),body=el('textarea');body.name='body';body.rows=5;body.required=true;body.maxLength=20000;bl.append(body);f.append(bl);
    selectField(f,'공통 작업 영역 (선택하면 담당자가 자동 지정)','area_id',[['','없음'],...areas.map(a=>[a.id,`${a.name} — ${a.owner_name}`])]);
    selectField(f,'담당자 (비우면 영역 담당자)','assignee_id',memberOptions('자동 / 미지정'));
    const tags=field(f,'태그 (쉼표로 구분, 선택)','tags');tags.required=false;
    const bk=el('label','답변 전까지 관련 작업 보류'),cb=el('input');cb.type='checkbox';cb.name='blocking';cb.value='true';cb.style.width='auto';bk.classList.add('inline-check');bk.prepend(cb);f.append(bk);
    formSubmit(f,'등록',async d=>{
      const input={project_id:current().id,kind:d.kind,title:d.title,body:d.body,blocking:d.blocking==='true'};
      if (d.area_id) input.area_id=d.area_id;
      if (d.assignee_id) input.assignee_id=d.assignee_id;
      const tagList=(d.tags||'').split(',').map(s=>s.trim()).filter(Boolean);if(tagList.length)input.tags=tagList;
      await tool('create_issue',input);f.reset();notify('등록했습니다.');await loadProject();
    });
    createCard.append(f);
  }
  function renderAreas() {
    areaCard.replaceChildren(el('h2','공통 작업 영역과 담당자'));
    areaCard.append(el('p','작업 내용에 영역 이름이 포함되면 작업 시작 시 담당자가 표시됩니다. 담당자가 아닌 사람(또는 AI)은 결정 전에 질의를 남깁니다.','small muted'));
    const p=current();
    const areaOwnerCell=a=>{if(a.owner_active)return a.owner_name;const n=el('div');n.append(el('div',a.owner_name),el('span','담당자 확인 필요','pill stale'));n.title='담당자가 프로젝트를 떠났거나 조회자로 바뀌었거나 비활성화되었습니다. 담당자를 바꾸거나 영역을 정리하세요.';return n;};
    const inactive=areas.filter(a=>!a.owner_active).length;
    if (inactive) areaCard.append(el('p',`담당자 확인이 필요한 영역 ${inactive}개: 담당자를 바꾸거나 더 쓰지 않는 영역은 삭제하세요.`,'vector-warning'));
    areaCard.append(table(['영역','설명','담당자','진행 중',''],areas.map(a=>[a.name,a.description||'—',areaOwnerCell(a),a.open_issues,canDeleteArea(a)?areaDelete(a):''])));
    if (!canWrite()) return;
    const f=el('form');
    const pick=selectField(f,'편집할 영역','area_id',[['','새 영역 추가'],...areas.map(a=>[a.id,a.name])]);
    const name=field(f,'영역 이름','name');name.maxLength=100;
    const desc=field(f,'설명 (선택)','description');desc.required=false;desc.maxLength=2000;
    const owner=selectField(f,'담당자','owner_id',memberOptions());
    pick.addEventListener('change',()=>{const a=areas.find(x=>x.id===pick.value);name.value=a?.name||'';desc.value=a?.description||'';if(a)owner.value=a.owner_id;});
    formSubmit(f,'영역 저장',async d=>{
      const input={project_id:p.id,name:d.name,description:d.description||'',owner_id:d.owner_id};
      if (d.area_id) input.area_id=d.area_id;
      await tool('set_work_area',input);notify('영역을 저장했습니다.');await loadProject();
    });
    areaCard.append(f);
  }
  function canDeleteArea(a) { return canWrite()&&(current().role==='owner'||a.owner_id===me.id); }
  function areaDelete(a) {
    const box=el('div',undefined,'actions'), others=areas.filter(x=>x.id!==a.id);
    const move=el('select');move.setAttribute('aria-label',a.name+' 질의를 옮길 영역');
    move.append(new Option('질의는 영역 없이 두기',''),...others.map(x=>new Option('→ '+x.name,x.id)));
    box.append(move,btn('삭제',async()=>{
      const target=others.find(x=>x.id===move.value);
      if (!confirm(`영역 "${a.name}"을 삭제할까요?
이 영역의 질의·이슈는 삭제되지 않고 ${target?`"${target.name}" 영역으로 옮겨집니다`:'영역 없이 남습니다'} (담당자는 그대로).`)) return;
      const r=await tool('delete_work_area',{project_id:current().id,area_id:a.id,...(target?{move_to_area_id:target.id}:{})});
      notify(`영역을 삭제했습니다. 옮긴 질의 ${r.moved_issues}건, 영역 없이 남은 질의 ${r.detached_issues}건.`);await loadProject();
    },'danger'));
    return box;
  }
  function selectQuery(form) {
    const l=el('label','검색어'),i=el('input');i.type='search';i.placeholder='제목·내용·답변';i.maxLength=500;l.append(i);form.append(l);
    let timer;i.addEventListener('input',()=>{clearTimeout(timer);timer=setTimeout(()=>refresh().catch(e=>notify(e.message,true)),300);});
    return i;
  }
  project.addEventListener('change',()=>loadProject().catch(e=>notify(e.message,true)));
  for (const s of [scope,status]) s.addEventListener('change',()=>refresh().catch(e=>notify(e.message,true)));
  await loadProject();
  return root;
}
