'use strict';

const issueStatusLabels={open:'답변 대기',answered:'답변됨',resolved:'해결',closed:'닫힘'};
const issueKindLabels={question:'질의',issue:'이슈'};
const issueImageTypes=['image/png','image/jpeg','image/gif','image/webp'];
const issueImageMaxBytes=10*1024*1024, issueImageMaxCount=5;

// Picks up to five images from a file chooser or pasted screenshots and previews them before upload.
function issueImagePicker(pasteTarget,pasteLabel='내용 칸') {
  const box=el('div',undefined,'issue-image-picker'), files=[];
  const input=el('input');input.type='file';input.multiple=true;input.accept=issueImageTypes.join(',');input.hidden=true;
  const choose=btn('사진 선택',()=>input.click());
  const previews=el('div',undefined,'issue-images');
  const hint=el('p',`PNG·JPG·GIF·WEBP, 장당 10MB, 최대 ${issueImageMaxCount}장. ${pasteLabel}에 스크린샷을 붙여 넣어도 됩니다.`,'small muted');
  const head=el('div',undefined,'actions');head.append(choose,hint);
  box.append(el('span','사진 첨부 (선택)','field-title'),head,input,previews);
  function add(list) {
    for (const f of list) {
      if (!issueImageTypes.includes(f.type)) { notify(`${f.name||'붙여 넣은 파일'}: 이미지(PNG·JPG·GIF·WEBP)만 첨부할 수 있습니다.`,true); continue; }
      if (f.size<1||f.size>issueImageMaxBytes) { notify(`${f.name||'붙여 넣은 이미지'}: 10MB 이하 이미지만 첨부할 수 있습니다.`,true); continue; }
      if (files.length>=issueImageMaxCount) { notify(`사진은 최대 ${issueImageMaxCount}장까지 첨부할 수 있습니다.`,true); break; }
      // Pasted screenshots are often named image.png; give each a distinct name.
      const ext=f.type.split('/')[1].replace('jpeg','jpg');
      files.push(f.name&&f.name!=='image.'+ext?f:new File([f],`screenshot-${Date.now()}-${files.length+1}.${ext}`,{type:f.type}));
    }
    draw();
  }
  function draw() {
    for (const img of previews.querySelectorAll('img')) URL.revokeObjectURL(img.src);
    previews.replaceChildren(...files.map((f,i)=>{
      const fig=el('figure',undefined,'issue-image'), img=el('img');img.src=URL.createObjectURL(f);img.alt=f.name;
      const remove=el('button','×','issue-image-remove');remove.type='button';remove.title='첨부 취소';remove.setAttribute('aria-label',f.name+' 첨부 취소');
      remove.addEventListener('click',()=>{files.splice(i,1);draw();});
      fig.append(img,el('figcaption',f.name),remove);return fig;
    }));
  }
  input.addEventListener('change',()=>{add([...input.files]);input.value='';});
  pasteTarget?.addEventListener('paste',e=>{
    const pasted=[...(e.clipboardData?.files||[])].filter(f=>f.type.startsWith('image/'));
    if (!pasted.length) return;
    e.preventDefault();add(pasted);
  });
  return {node:box,files:()=>[...files],clear(){files.length=0;draw();}};
}

// Uploads images to an existing issue one by one; returns the names that failed.
async function uploadIssueImages(projectID,issueID,files,target={}) {
  const failed=[];
  for (const f of files) {
    try {
      const prepared=await tool('prepare_attachment_upload',{project_id:projectID,issue_id:issueID,filename:f.name,size_bytes:f.size,...target});
      const response=await fetch(prepared.upload_path,{method:'PUT',headers:{'Content-Type':'application/octet-stream','X-CSRF-Token':csrf},body:f});
      const body=await response.json().catch(()=>({}));
      if (!response.ok) throw Error(body.error?.message||'업로드 실패');
    } catch(e) { failed.push(`${f.name} (${e.message})`); }
  }
  return failed;
}

function issueImageURL(f) { return `/api/attachments/${encodeURIComponent(f.id)}/image?project_id=${encodeURIComponent(f.project_id)}`; }

// Inline picker that ties a question or issue to a to-do (link_issue_todo).
async function issueTodoForm(cardEl,i,after) {
  cardEl.querySelector('.task-todo-form')?.remove();
  const form=el('div',undefined,'task-todo-form');
  const todos=(await tool('list_todos',{project_id:i.project_id})).filter(t=>t.status!=='completed'||t.id===i.todo?.id);
  const label=el('label','이 질의·이슈가 속한 할 일 (보드 카드에 질의·보류 수가 표시되고, 할 일과 연결된 AI 작업을 시작할 때 함께 안내됩니다)');
  const select=el('select');select.append(new Option('연결 안 함',''));
  for (const t of todos) select.append(new Option(`${t.title} · ${todoPriorityLabel[t.priority]} · ${todoStatusLabel[t.status]}`,t.id));
  select.value=i.todo?.id||'';label.append(select);
  const row=el('div',undefined,'actions');
  row.append(btn('저장',async()=>{
    const out=await tool('link_issue_todo',{project_id:i.project_id,issue_id:i.id,todo_id:select.value});
    notify(out.todo?`“${out.todo.title}”에 연결했습니다.`:'할 일 연결을 해제했습니다.');
    await after();
  }),btn('취소',()=>form.remove()));
  form.append(label,row);
  if (!todos.length) form.prepend(el('p','연결할 수 있는 할 일이 없습니다. 할 일 보드에서 먼저 추가하세요.','small muted'));
  cardEl.append(form);select.focus();
}

// Remembers the selected tab (list | github | areas | notify) while the page re-renders.
let issuesTab='list';

async function issuesPage() {
  // Set when another page (a task) asks for its linked questions; cleared by any filter change.
  let focus=takePageFocus('issues');
  if (focus) issuesTab='list';
  const root=el('div'), projects=[];
  for (let offset=0; offset<=100000; offset+=50) {
    const batch=await tool('list_projects',{limit:50,offset});
    projects.push(...batch); if(batch.length<50)break;
  }
  const top=el('section',undefined,'card issues-card');
  root.append(top);
  if (!projects.length) { top.append(el('p','먼저 프로젝트에 참여해 주세요.','empty')); return root; }
  const project=projectSelect(projects);
  let generation=0, members=[], areas=[], todos=[];
  const current=()=>projects.find(p=>p.id===project.value);
  const canWrite=()=>['owner','admin','editor'].includes(current().role);

  // One tab bar keeps the list in front and moves setup (GitHub, areas, alerts) behind tabs.
  const bar=el('div',undefined,'rules-bar'), tabList=el('div',undefined,'rules-tabs'), addSlot=el('div',undefined,'rules-add-slot');
  tabList.setAttribute('role','tablist');tabList.setAttribute('aria-label','이슈 · 질의 메뉴');
  const listPanel=el('div'), githubPanel=el('div'), areaPanel=el('div'), notifyPanel=el('div');
  const tabs={
    list:{label:'질의 목록',panel:listPanel,description:'담당자가 따로 있는 일은 바로 결정하지 말고 질의를 남깁니다. 작업을 시작할 때(start_task) 내게 배정된 질의, 내가 남긴 질의의 답변, 관련 이슈가 함께 안내되고, "작업 보류" 질의는 답변 전까지 관련 작업을 멈추도록 안내됩니다.'},
    github:{label:'GitHub 연동',panel:githubPanel,description:'GitHub 저장소의 이슈를 이 목록으로 가져옵니다. 웹훅으로 새 이벤트를 자동 반영하고, 기존 이슈는 처음 한 번 가져오기로 채웁니다.'},
    areas:{label:'영역 · 담당자',panel:areaPanel,description:'여러 사람이 함께 건드리는 범위와 담당자를 등록합니다. 영역을 고르고 질의를 남기면 담당자에게 자동 배정됩니다.'},
    notify:{label:'알림',panel:notifyPanel,description:'질의 배정·답변·댓글·상태 변경을 담당자와 질의자에게 알리는 채널입니다.'},
  };
  const tabButtons={}, intro=el('p','','small muted');
  function showTab(key) {
    issuesTab=tabs[key]?key:'list';
    for (const [k,b] of Object.entries(tabButtons)) {const on=k===issuesTab;b.classList.toggle('active',on);b.setAttribute('aria-selected',String(on));b.tabIndex=on?0:-1;}
    for (const [k,t] of Object.entries(tabs)) t.panel.hidden=k!==issuesTab;
    listFilters.hidden=issuesTab!=='list'; // filters belong to the list tab only
    intro.textContent=tabs[issuesTab].description;
  }
  for (const [key,t] of Object.entries(tabs)) {
    const b=el('button',t.label,'rules-tab');b.type='button';b.setAttribute('role','tab');
    b.addEventListener('click',()=>showTab(key));
    b.addEventListener('keydown',e=>{if(e.key!=='ArrowLeft'&&e.key!=='ArrowRight')return;const keys=Object.keys(tabs),next=keys[(keys.indexOf(key)+(e.key==='ArrowRight'?1:keys.length-1))%keys.length];showTab(next);tabButtons[next].focus();});
    tabButtons[key]=b;tabList.append(b);
  }
  const setCount=(key,n)=>{tabButtons[key].textContent=tabs[key].label+(n?` (${n})`:'');};
  bar.append(tabList,addSlot);

  const filters=el('div',undefined,'vector-controls');
  const scope=selectField(filters,'보기','scope',[['all','전체'],['assigned','내게 배정'],['asked','내가 남긴']]);
  const status=selectField(filters,'상태','status',[['active','진행 중 (대기·답변됨)'],['open','답변 대기'],['answered','답변됨'],['resolved','해결'],['closed','닫힘'],['all','모든 상태']]);
  const query=selectQuery(filters);
  const summary=el('p','','small muted issues-summary');summary.setAttribute('role','status');
  const listFilters=el('div');listFilters.append(filters,summary);
  const list=el('div');
  listPanel.append(list);
  top.append(bar,intro,listFilters);
  root.append(listPanel,githubPanel,areaPanel,notifyPanel);
  showTab(issuesTab);

  async function loadProject() {
    const ticket=++generation, p=current();
    [members,areas,todos]=await Promise.all([tool('list_project_members',{project_id:p.id}),tool('list_work_areas',{project_id:p.id}),tool('list_todos',{project_id:p.id}).then(l=>l.filter(t=>t.status!=='completed')).catch(()=>[])]);
    if (ticket!==generation) return;
    renderAddButton(); renderAreas(); setCount('areas',areas.length); await refresh();
    const importCard=renderImport(), webhooks=await githubWebhookCard(p,{members,areas});
    if (ticket===generation||current().id===p.id) githubPanel.replaceChildren(webhooks,importCard);
    const notifications=await notificationCard(p);
    if (ticket===generation||current().id===p.id) notifyPanel.replaceChildren(notifications);
  }
  async function refresh() {
    const ticket=++generation, p=current();
    list.replaceChildren(el('p','불러오는 중…','muted'));
    if (focus?.issue_ids?.length) return showFocused(ticket,p,focus);
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

  async function showFocused(ticket,p,f) {
    const items=(await Promise.all(f.issue_ids.map(id=>tool('get_issue',{project_id:p.id,issue_id:id}).catch(()=>null)))).filter(Boolean);
    if (ticket!==generation) return;
    summary.replaceChildren(document.createTextNode(`${f.label||'선택한 질의·이슈'} ${items.length}건 `),btn('전체 목록 보기',()=>{focus=null;return refresh();},'secondary issues-focus-clear'));
    list.replaceChildren(...(items.length?items.map(issueCard):[el('p','질의·이슈를 찾지 못했습니다. 삭제되었거나 볼 권한이 없을 수 있습니다.','empty')]));
  }

  function issueCard(i) {
    const c=card(), head=el('div',undefined,'issue-head');
    head.append(el('span',issueKindLabels[i.kind],'pill'),el('span',issueStatusLabels[i.status],'pill issue-'+i.status));
    if (i.blocking&&i.status==='open') head.append(el('span','작업 보류','pill error'));
    if (i.origin_project_name) head.append(el('span','하위 프로젝트 · '+i.origin_project_name,'pill'));
    head.append(el('h2',i.title));
    c.append(head);
    const meta=[i.area_name&&('영역 '+i.area_name),'담당 '+(i.assignee_name||'미지정'),'작성 '+i.created_by_name,new Date(i.updated_at).toLocaleString()];
    if (i.comment_count) meta.push('댓글 '+i.comment_count);
    c.append(el('p',meta.filter(Boolean).join(' · '),'small muted'));
    if (i.todo) {
      const b=btn(`할 일 · ${i.todo.title} (${todoStatusLabel[i.todo.status]||i.todo.status})`,()=>render('todos'),'secondary task-todo');
      b.title='할 일 보드로 이동';const row=el('div',undefined,'task-facts');row.append(b);c.append(row);
    }
    if (i.tags?.length) c.append(el('p',i.tags.join(' · '),'small'));
    if (i.source==='github') {
      const src=el('p',undefined,'small muted');
      if (i.source_url?.startsWith('https://')) { const a=el('a','GitHub '+i.source_key);a.href=i.source_url;a.target='_blank';a.rel='noopener noreferrer';src.append(a); }
      else src.append(document.createTextNode('GitHub '+i.source_key));
      src.append(document.createTextNode(' 에서 가져옴'+(i.source_updated_at?' · GitHub 수정 '+new Date(i.source_updated_at).toLocaleString():'')));
      c.append(src);
    }
    c.append(renderMarkdown(i.body,'vector-content'));
    const gallery=el('div',undefined,'issue-images');
    c.append(gallery);
    // One get_issue call fills the question, answer and comment galleries.
    function drawGallery(box,files) {
      box.replaceChildren(...files.map(f=>{
        const fig=el('figure',undefined,'issue-image'), link=el('a'), img=el('img');
        link.href=issueImageURL(f);link.target='_blank';link.rel='noopener';link.title=f.filename+' 원본 보기';
        img.src=issueImageURL(f)+'&size=thumb';img.alt=f.filename;img.loading='lazy';link.append(img);
        fig.append(link,el('figcaption',f.filename));
        if (canWrite()) {
          const remove=el('button','×','issue-image-remove');remove.type='button';remove.title='사진 삭제';remove.setAttribute('aria-label',f.filename+' 삭제');
          remove.addEventListener('click',async()=>{
            if (!confirm(`사진 "${f.filename}"을 삭제할까요?`)) return;
            remove.disabled=true;
            try { await tool('delete_attachment',{project_id:i.project_id,attachment_id:f.id});fig.remove();i.attachment_count=Math.max(0,i.attachment_count-1); }
            catch(e) { remove.disabled=false;notify(e.message,true); }
          });
          fig.append(remove);
        }
        return fig;
      }));
    }
    let answerGallery=null;
    async function loadImages() {
      if (!i.attachment_count) { gallery.replaceChildren();answerGallery?.replaceChildren(); return; }
      try {
        const full=await tool('get_issue',{project_id:i.project_id,issue_id:i.id});
        drawGallery(gallery,full.attachments||[]);
        if (answerGallery) drawGallery(answerGallery,full.answer_attachments||[]);
        if (thread.open) drawThread(full);
      } catch(e) { gallery.replaceChildren(el('p','사진을 불러오지 못했습니다: '+e.message,'small muted')); }
    }
    if (i.answer) {
      const answer=el('div',undefined,'issue-answer');
      answerGallery=el('div',undefined,'issue-images');
      answer.append(el('p',`답변 · ${i.answered_by_name||''} · ${i.answered_at?new Date(i.answered_at).toLocaleString():''}`,'small muted'),renderMarkdown(i.answer,'vector-content'),answerGallery);
      c.append(answer);
    }
    if (i.resolution) c.append(el('p','해결 메모: '+i.resolution,'small'));
    if (i.knowledge_document_id) c.append(el('p','지식 문서로 저장됨 · '+i.knowledge_document_id,'small muted'));
    const thread=el('details'), threadBody=el('div');
    thread.append(el('summary','댓글'+(i.comment_count?` ${i.comment_count}개`:'')),threadBody);
    function drawThread(full) {
      const comments=full.comments||[];
      threadBody.replaceChildren(...comments.map(cm=>{
        const n=el('div',undefined,'issue-comment'), images=el('div',undefined,'issue-images');
        drawGallery(images,cm.attachments||[]);
        n.append(el('p',`${cm.author_name} · ${new Date(cm.created_at).toLocaleString()}`,'small muted'),renderMarkdown(cm.body,'vector-content'),images);
        return n;
      }));
      if (!comments.length) threadBody.append(el('p','댓글이 없습니다.','small muted'));
    }
    thread.addEventListener('toggle',async()=>{
      if (!thread.open||threadBody.childElementCount) return;
      try { drawThread(await tool('get_issue',{project_id:i.project_id,issue_id:i.id})); }
      catch(e) { threadBody.replaceChildren(el('p',e.message,'muted')); }
    });
    c.append(thread);
    void loadImages();
    if (!canWrite()) return c;

    const text=el('textarea');text.rows=3;text.maxLength=20000;text.placeholder=i.status==='open'||i.status==='answered'?'답변 또는 댓글':'댓글';
    const label=el('label','답변 · 댓글');label.append(text);
    const replyImages=issueImagePicker(text,'답변·댓글 칸');
    const actions=el('div',undefined,'actions');
    const need=()=>{const v=text.value.trim();if(!v){text.focus();throw Error('내용을 입력해 주세요.');}return v;};
    // Uploads the picked images after the answer or comment is saved; the text is kept even if an image fails.
    async function attachReplyImages(target,done) {
      const picked=replyImages.files();
      if (!picked.length) { notify(done); return; }
      notify(done+' 사진을 올리는 중…');
      const failed=await uploadIssueImages(i.project_id,i.id,picked,target);
      if (failed.length) notify(done+' 일부 사진을 올리지 못했습니다: '+failed.join(', '),true);
      else notify(`${done} 사진 ${picked.length}장을 첨부했습니다.`);
    }
    if (i.status==='open'||i.status==='answered') actions.append(btn(i.answer?'답변 수정':'답변 등록',async()=>{
      await tool('answer_issue',{project_id:i.project_id,issue_id:i.id,answer:need()});
      await attachReplyImages({issue_part:'answer'},'답변을 등록했습니다.');await refresh();
    },''));
    actions.append(btn('댓글',async()=>{
      const out=await tool('comment_issue',{project_id:i.project_id,issue_id:i.id,body:need()});
      await attachReplyImages({issue_part:'comment',issue_comment_id:out.created_comment_id},'댓글을 남겼습니다.');await refresh();
    }));
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
    const addImages=el('input');addImages.type='file';addImages.multiple=true;addImages.accept=issueImageTypes.join(',');addImages.hidden=true;
    addImages.addEventListener('change',async()=>{
      const picked=[...addImages.files];addImages.value='';
      const bad=picked.find(f=>!issueImageTypes.includes(f.type)||f.size<1||f.size>issueImageMaxBytes);
      if (bad) { notify(`${bad.name}: 10MB 이하 PNG·JPG·GIF·WEBP 이미지만 첨부할 수 있습니다.`,true); return; }
      if (picked.length>issueImageMaxCount) { notify(`한 번에 최대 ${issueImageMaxCount}장까지 올릴 수 있습니다.`,true); return; }
      notify('사진을 올리는 중…');
      const failed=await uploadIssueImages(i.project_id,i.id,picked);
      if (failed.length) notify('일부 사진을 올리지 못했습니다: '+failed.join(', '),true); else notify('사진을 첨부했습니다.');
      i.attachment_count=(i.attachment_count||0)+picked.length-failed.length;await loadImages();
    });
    actions.append(btn(i.todo?'할 일 변경':'할 일 연결',()=>issueTodoForm(c,i,refresh)));
    const addBody=btn('질문에 사진 추가',()=>addImages.click());addBody.title='질문 본문에 사진을 붙입니다. 답변·댓글 사진은 위 입력란에서 첨부합니다.';
    actions.append(addBody,addImages);
    if (i.status==='open') actions.append(btn(i.blocking?'보류 해제':'작업 보류로 표시',async()=>{await tool('update_issue',{project_id:i.project_id,issue_id:i.id,blocking:!i.blocking});await refresh();}));
    c.append(label,replyImages.node,actions);
    return c;
  }

  function memberOptions(empty) {
    return [...(empty?[['',empty]]:[]),...members.filter(m=>m.role!=='viewer'&&!m.disabled).map(m=>[m.user_id,`${m.name} (${m.role})`])];
  }
  function renderAddButton() {
    addSlot.replaceChildren();
    if (canWrite()) addSlot.append(btn('새 질의 · 이슈',openCreate,'rule-add'));
  }
  // The create form opens in a dialog so the list stays the first thing on the page.
  function openCreate() {
    const dialog=el('dialog',undefined,'issue-dialog');
    const close=btn('닫기',()=>dialog.close(),'secondary');
    const head=el('div',undefined,'issue-dialog-head');head.append(el('h2','새 질의 · 이슈'),close);
    dialog.append(head,createForm(()=>dialog.close()));
    // No close on backdrop click: a stray click must not throw away a half-written question.
    dialog.addEventListener('close',()=>dialog.remove(),{once:true});
    root.append(dialog);dialog.showModal();dialog.querySelector('input[name="title"]')?.focus();
  }
  function createForm(done) {
    const f=el('form');
    selectField(f,'종류','kind',[['question','질의 — 담당자에게 확인할 내용'],['issue','이슈 — 문제·결함·리스크']]);
    const t=field(f,'제목','title');t.maxLength=200;
    const bl=el('label','내용 (배경, 확인할 점, 검토한 선택지)'),body=el('textarea');body.name='body';body.rows=5;body.required=true;body.maxLength=20000;bl.append(body);f.append(bl);
    const images=issueImagePicker(body);f.append(images.node);
    selectField(f,'공통 작업 영역 (선택하면 담당자가 자동 지정)','area_id',[['','없음'],...areas.map(a=>[a.id,`${a.name} — ${a.owner_name}`])]);
    selectField(f,'담당자 (비우면 영역 담당자)','assignee_id',memberOptions('자동 / 미지정'));
    if (todos.length) selectField(f,'할 일 (선택, 할 일 보드에 질의·보류 수로 표시)','todo_id',[['','없음'],...todos.map(t=>[t.id,`${t.title} · ${todoStatusLabel[t.status]}`])]);
    const tags=field(f,'태그 (쉼표로 구분, 선택)','tags');tags.required=false;
    const bk=el('label','답변 전까지 관련 작업 보류'),cb=el('input');cb.type='checkbox';cb.name='blocking';cb.value='true';cb.style.width='auto';bk.classList.add('inline-check');bk.prepend(cb);f.append(bk);
    formSubmit(f,'등록',async d=>{
      const input={project_id:current().id,kind:d.kind,title:d.title,body:d.body,blocking:d.blocking==='true'};
      if (d.area_id) input.area_id=d.area_id;
      if (d.assignee_id) input.assignee_id=d.assignee_id;
      if (d.todo_id) input.todo_id=d.todo_id;
      const tagList=(d.tags||'').split(',').map(s=>s.trim()).filter(Boolean);if(tagList.length)input.tags=tagList;
      const issue=await tool('create_issue',input);
      const picked=images.files();
      if (picked.length) {
        notify('등록했습니다. 사진을 올리는 중…');
        const failed=await uploadIssueImages(issue.project_id,issue.id,picked);
        if (failed.length) notify('등록했지만 일부 사진을 올리지 못했습니다. 카드의 "사진 추가"로 다시 올려 주세요: '+failed.join(', '),true);
        else notify(`등록하고 사진 ${picked.length}장을 첨부했습니다.`);
      } else notify('등록했습니다.');
      done();showTab('list');await loadProject();
    });
    return f;
  }
  function renderImport() {
    const importCard=card('기존 이슈 가져오기');
    importCard.append(el('p','GitHub 저장소의 이슈를 이 프로젝트의 이슈·질의로 가져옵니다(단방향, PR 제외). 웹훅은 설정 이후의 이벤트만 보내므로, 기존 이슈는 처음 한 번 여기서 가져옵니다. 다시 가져오면 같은 항목의 제목·본문·라벨이 갱신되고, 상태는 GitHub에서 열림·닫힘이 바뀐 경우에만 따라갑니다. 여기서 단 답변·담당자·영역은 유지됩니다. 비공개 저장소는 서버에 GITHUB_TOKEN이 있어야 합니다.','small muted'));
    if (!canWrite()) { importCard.append(el('p','편집자 이상만 가져올 수 있습니다.','small muted')); return importCard; }
    const f=el('form',undefined,'issue-form-grid');
    const repo=field(f,'저장소 (owner/name 또는 GitHub 주소)','repository');repo.placeholder='octocat/hello-world';repo.maxLength=200;
    selectField(f,'상태','state',[['open','열린 이슈'],['closed','닫힌 이슈'],['all','전체']]);
    selectField(f,'종류','kind',[['issue','모두 이슈'],['question','모두 질의'],['auto','자동 — question 라벨이면 질의']]);
    const labels=field(f,'라벨 필터 (쉼표로 구분, 모두 포함한 이슈만, 선택)','labels');labels.required=false;
    const limit=field(f,'최대 개수 (1–500)','limit','number','100');limit.min=1;limit.max=500;
    selectField(f,'새 항목의 공통 작업 영역','area_id',[['','없음'],...areas.map(a=>[a.id,`${a.name} — ${a.owner_name}`])]);
    selectField(f,'새 항목의 담당자 (비우면 영역 담당자)','assignee_id',memberOptions('자동 / 미지정'));
    const tags=field(f,'추가 태그 (쉼표로 구분, 선택)','tags');tags.required=false;
    const result=el('div');
    formSubmit(f,'가져오기',async d=>{
      const input={project_id:current().id,repository:d.repository.trim(),state:d.state,kind:d.kind,limit:Number(d.limit)||100};
      const split=v=>(v||'').split(',').map(s=>s.trim()).filter(Boolean);
      if (split(d.labels).length) input.labels=split(d.labels);
      if (split(d.tags).length) input.tags=split(d.tags);
      if (d.area_id) input.area_id=d.area_id;
      if (d.assignee_id) input.assignee_id=d.assignee_id;
      result.replaceChildren(el('p','GitHub에서 가져오는 중…','muted'));
      try {
        const r=await tool('import_github_issues',input);
        const text=`${r.repository}: ${r.fetched}건 조회 · 새로 ${r.created} · 갱신 ${r.updated} · 변경 없음 ${r.unchanged}`+(r.skipped_pull_requests?` · PR ${r.skipped_pull_requests}건 제외`:'')+(r.authenticated?'':' · 토큰 없이 조회(공개 저장소만)');
        result.replaceChildren(el('p',text,'small'));
        notify('GitHub 이슈를 가져왔습니다.');await refresh();
      } catch(e) { result.replaceChildren(); throw e; }
    });
    const fold=el('details',undefined,'issue-setup');fold.append(el('summary','가져오기 설정'),f);
    importCard.append(fold,result);
    return importCard;
  }
  function renderAreas() {
    const c=card('공통 작업 영역과 담당자');
    areaPanel.replaceChildren(c);
    c.append(el('p','작업 내용에 영역 이름이 포함되면 작업 시작 시 담당자가 표시됩니다. 담당자가 아닌 사람(또는 AI)은 결정 전에 질의를 남깁니다.','small muted'));
    const p=current();
    const areaOwnerCell=a=>{if(a.owner_active)return a.owner_name;const n=el('div');n.append(el('div',a.owner_name),el('span','담당자 확인 필요','pill stale'));n.title='담당자가 프로젝트를 떠났거나 조회자로 바뀌었거나 비활성화되었습니다. 담당자를 바꾸거나 영역을 정리하세요.';return n;};
    const inactive=areas.filter(a=>!a.owner_active).length;
    if (inactive) c.append(el('p',`담당자 확인이 필요한 영역 ${inactive}개: 담당자를 바꾸거나 더 쓰지 않는 영역은 삭제하세요.`,'vector-warning'));
    c.append(table(['영역','설명','담당자','진행 중',''],areas.map(a=>[a.name,a.description||'—',areaOwnerCell(a),a.open_issues,canDeleteArea(a)?areaDelete(a):''])));
    if (!canWrite()) return;
    const f=el('form',undefined,'issue-form-grid');
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
    // The edit form stays folded until needed so the table reads first.
    const edit=el('details',undefined,'issue-setup');edit.open=!areas.length;
    edit.append(el('summary','영역 추가 · 수정'),f);c.append(edit);
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
    let timer;i.addEventListener('input',()=>{clearTimeout(timer);focus=null;timer=setTimeout(()=>refresh().catch(e=>notify(e.message,true)),300);});
    return i;
  }
  project.addEventListener('change',()=>{focus=null;loadProject().catch(e=>notify(e.message,true));});
  for (const s of [scope,status]) s.addEventListener('change',()=>{focus=null;refresh().catch(e=>notify(e.message,true));});
  await loadProject();
  return root;
}
