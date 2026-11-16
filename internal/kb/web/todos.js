'use strict';

// 할 일 보드: project to-dos in status columns × priority rows. Cards drag
// between cells (move_todo) with the mouse or, after a long press, a finger,
// and open an edit dialog (save_todo/delete_todo).
const todoStatuses=[['not_started','시작 전'],['in_progress','진행 중'],['completed','완료'],['idea','아이디어']];
const todoPriorities=[['high','높음'],['medium','보통'],['low','낮음']];
const todoStatusLabel=Object.fromEntries(todoStatuses), todoPriorityLabel=Object.fromEntries(todoPriorities);
const todoCollapseKey='kb.todo.collapsed';

function todoCollapsed(){try{return new Set(JSON.parse(localStorage.getItem(todoCollapseKey)||'[]'));}catch{return new Set();}}
function saveTodoCollapsed(set){try{localStorage.setItem(todoCollapseKey,JSON.stringify([...set]));}catch{}}

async function todosPage(){
  const root=el('div',undefined,'todo-page');
  const project=projectList.find(p=>p.id===currentProjectID);
  if(!project){const c=card('할 일 보드');c.append(el('p','먼저 프로젝트에 참여하고 사이드바에서 현재 프로젝트를 선택해 주세요.','empty'));root.append(c);return root;}
  const canWrite=['owner','admin','editor'].includes(project.role);
  const bar=el('div',undefined,'todo-toolbar');
  const filters=el('div',undefined,'todo-filters');
  const category=el('select');category.setAttribute('aria-label','분류');
  const mineLabel=el('label',undefined,'inline-check'),mine=el('input');mine.type='checkbox';
  mineLabel.append(mine,document.createTextNode('내게 배정된 것만'));
  const summary=el('span','','small muted');summary.setAttribute('role','status');
  filters.append(category,mineLabel,summary);
  bar.append(filters);
  if(canWrite)bar.append(btn('+ 새 할 일',()=>todoDialog(root,project,ctx,{status:'not_started',priority:'medium'}),'primary'));
  const board=el('div',undefined,'todo-board');
  root.append(bar,board);
  const ctx={todos:[],members:[],reload:refresh};
  let generation=0;
  const collapsed=todoCollapsed();

  async function refresh(){
    const ticket=++generation;
    const [todos,members]=await Promise.all([
      tool('list_todos',{project_id:project.id}),
      ctx.members.length?ctx.members:tool('list_project_members',{project_id:project.id}).catch(()=>[]),
    ]);
    if(ticket!==generation)return;
    ctx.todos=todos;ctx.members=members;
    const cats=[...new Set(todos.map(t=>t.category).filter(Boolean))].sort((a,b)=>a.localeCompare(b));
    const chosen=category.value;
    category.replaceChildren(new Option('모든 분류',''),...cats.map(c=>new Option(c,c)));
    category.value=cats.includes(chosen)?chosen:'';
    draw();
  }
  function visible(){
    return ctx.todos.filter(t=>(!category.value||t.category===category.value)&&(!mine.checked||t.assignee_id===me.id));
  }
  function draw(){
    if(touchDrag)endTouchDrag(false);
    const items=visible();
    summary.textContent=`${items.length}건`+(ctx.todos.length>=500?' · 최대 500건까지 표시':'');
    const inner=el('div',undefined,'todo-board-inner');
    const head=el('div',undefined,'todo-row todo-columns');
    head.append(el('div'));
    for(const [s,label] of todoStatuses){
      const h=el('div',undefined,'todo-col-head');
      h.append(el('span',label,'todo-status todo-s-'+s),el('span',String(items.filter(t=>t.status===s).length),'todo-count'));
      head.append(h);
    }
    inner.append(head);
    for(const [pr,plabel] of todoPriorities){
      const lane=el('section',undefined,'todo-lane'),inLane=items.filter(t=>t.priority===pr);
      const toggle=el('button',undefined,'todo-lane-toggle');toggle.type='button';
      const open=!collapsed.has(pr);toggle.setAttribute('aria-expanded',String(open));
      toggle.append(el('span',open?'▾':'▸','todo-caret'),el('span',plabel,'todo-priority todo-p-'+pr),el('span',String(inLane.length),'todo-count'));
      toggle.addEventListener('click',()=>{collapsed.has(pr)?collapsed.delete(pr):collapsed.add(pr);saveTodoCollapsed(collapsed);draw();});
      lane.append(toggle);
      if(open){
        const row=el('div',undefined,'todo-row');
        row.append(el('div'));
        for(const [s,slabel] of todoStatuses){
          const cell=el('div',undefined,'todo-cell todo-c-'+s);
          cell.dataset.status=s;cell.dataset.priority=pr;
          cell.setAttribute('aria-label',`${plabel} · ${slabel}`);
          for(const t of inLane.filter(t=>t.status===s))cell.append(todoCard(t));
          if(canWrite){
            const add=el('button','+ 새로 만들기','todo-add');add.type='button';
            add.addEventListener('click',()=>todoDialog(root,project,ctx,{status:s,priority:pr}));
            cell.append(add);
            dropZone(cell);
          }
          row.append(cell);
        }
        lane.append(row);
      }
      inner.append(lane);
    }
    board.replaceChildren(inner);
    if(!ctx.todos.length)board.append(el('p',canWrite?'아직 할 일이 없습니다. 칸의 “새로 만들기”나 오른쪽 위 버튼으로 추가하세요.':'아직 할 일이 없습니다.','empty'));
  }
  function todoCard(t){
    const c=el('article',undefined,'todo-card');c.tabIndex=0;c.dataset.id=t.id;
    c.setAttribute('aria-label',`${t.title}, ${todoStatusLabel[t.status]}`);
    c.append(el('h3',t.title));
    const tags=el('div',undefined,'todo-tags');
    tags.append(el('span',todoStatusLabel[t.status],'todo-status todo-s-'+t.status));
    if(t.category)tags.append(el('span',t.category,'todo-category'));
    c.append(tags);
    const check=el('label',undefined,'todo-done'),box=el('input');box.type='checkbox';box.checked=t.status==='completed';box.disabled=!canWrite;
    check.append(box,document.createTextNode('완료'));
    check.addEventListener('click',e=>e.stopPropagation());
    box.addEventListener('change',async()=>{
      box.disabled=true;
      try{await tool('move_todo',{project_id:project.id,todo_id:t.id,status:box.checked?'completed':'in_progress',priority:t.priority});await refresh();}
      catch(e){box.checked=!box.checked;notify(e.message,true);}finally{box.disabled=!canWrite;}
    });
    c.append(check);
    if(t.progress!=null){
      const p=el('div',undefined,'todo-progress');p.setAttribute('role','progressbar');p.setAttribute('aria-valuemin','0');p.setAttribute('aria-valuemax','100');p.setAttribute('aria-valuenow',String(t.progress));
      const track=el('span',undefined,'todo-progress-track'),fill=el('span',undefined,'todo-progress-fill');fill.style.width=t.progress+'%';
      track.append(fill);p.append(track,el('span',t.progress+'%','todo-progress-label'));c.append(p);
    }
    const links=todoLinkPills(t);if(links)c.append(links);
    const meta=[t.assignee_name,t.due_date?`마감 ${t.due_date}`:''].filter(Boolean).join(' · ');
    if(meta){const m=el('p',meta,'todo-meta');if(t.due_date&&t.status!=='completed'&&t.due_date<todayISO())m.classList.add('overdue');c.append(m);}
    c.addEventListener('click',()=>todoDialog(root,project,ctx,t));
    c.addEventListener('keydown',e=>{if(e.key==='Enter'&&e.target===c){e.preventDefault();todoDialog(root,project,ctx,t);}});
    if(canWrite){
      c.draggable=true;
      c.addEventListener('dragstart',e=>{if(touchDrag){e.preventDefault();return;}e.dataTransfer.setData('text/plain',t.id);e.dataTransfer.effectAllowed='move';c.classList.add('dragging');});
      c.addEventListener('dragend',()=>{c.classList.remove('dragging');clearDropMarks();});
      touchDraggable(c,t);
    }
    return c;
  }
  function beforeCard(cell,y){
    for(const c of cell.querySelectorAll('.todo-card:not(.dragging)')){const b=c.getBoundingClientRect();if(y<b.top+b.height/2)return c;}
    return null;
  }
  function dropZone(cell){
    cell.addEventListener('dragover',e=>{
      e.preventDefault();e.dataTransfer.dropEffect='move';
      board.querySelectorAll('.drop-before').forEach(n=>n.classList.remove('drop-before'));
      cell.classList.add('drop-target');
      const before=beforeCard(cell,e.clientY);
      if(before)before.classList.add('drop-before');
    });
    cell.addEventListener('dragleave',e=>{if(!cell.contains(e.relatedTarget)){cell.classList.remove('drop-target');cell.querySelectorAll('.drop-before').forEach(n=>n.classList.remove('drop-before'));}});
    cell.addEventListener('drop',async e=>{
      e.preventDefault();
      const id=e.dataTransfer.getData('text/plain'),before=beforeCard(cell,e.clientY);
      cell.classList.remove('drop-target');
      await moveCard(id,cell,before);
    });
  }
  function clearDropMarks(){board.querySelectorAll('.drop-before,.drop-target').forEach(n=>n.classList.remove('drop-before','drop-target'));}
  async function moveCard(id,cell,before){
    if(!ctx.todos.some(x=>x.id===id))return;
    const body={project_id:project.id,todo_id:id,status:cell.dataset.status,priority:cell.dataset.priority};
    if(before&&before.dataset.id!==id)body.before_id=before.dataset.id;
    try{await tool('move_todo',body);}catch(err){notify(err.message,true);}
    await refresh().catch(err=>notify(err.message,true));
  }

  // Touch: HTML5 drag events do not fire for fingers. A long press picks the
  // card up (a short swipe still scrolls the board), a copy follows the finger
  // and lifting it drops the card into the cell underneath.
  const touchHoldMs=350,touchSlop=10,touchEdge=56,touchScrollStep=14;
  let touchDrag=null;
  function touchDraggable(c,t){
    let timer=0,press=null;
    const cancelPress=()=>{clearTimeout(timer);timer=0;press=null;};
    c.addEventListener('touchstart',e=>{
      cancelPress();
      if(touchDrag||e.touches.length!==1||e.target.closest('input,label,button,a'))return;
      const p=e.touches[0];press={x:p.clientX,y:p.clientY,id:p.identifier};
      timer=setTimeout(()=>{timer=0;if(press&&c.isConnected)beginTouchDrag(c,t,press);press=null;},touchHoldMs);
    },{passive:true});
    c.addEventListener('touchmove',e=>{
      if(touchDrag&&touchDrag.card===c){
        e.preventDefault();
        const p=[...e.touches].find(x=>x.identifier===touchDrag.touchID);
        if(p)trackTouchDrag(p.clientX,p.clientY);
        return;
      }
      if(!press)return;
      const p=e.touches[0];
      if(e.touches.length!==1||Math.hypot(p.clientX-press.x,p.clientY-press.y)>touchSlop)cancelPress();
    },{passive:false});
    const end=e=>{
      cancelPress();
      if(!touchDrag||touchDrag.card!==c)return;
      if([...e.touches].some(x=>x.identifier===touchDrag.touchID))return;
      e.preventDefault(); // no click (edit dialog) after a drag
      endTouchDrag(e.type==='touchend');
    };
    c.addEventListener('touchend',end);
    c.addEventListener('touchcancel',end);
    c.addEventListener('contextmenu',e=>{if(timer||touchDrag)e.preventDefault();});
  }
  function beginTouchDrag(c,t,press){
    const r=c.getBoundingClientRect(),ghost=c.cloneNode(true);
    ghost.classList.add('todo-ghost');ghost.removeAttribute('tabindex');ghost.setAttribute('aria-hidden','true');
    ghost.style.width=r.width+'px';
    document.body.append(ghost);
    c.classList.add('dragging');
    touchDrag={card:c,id:t.id,touchID:press.id,ghost,dx:press.x-r.left,dy:press.y-r.top,x:press.x,y:press.y,cell:null,before:null,raf:0};
    try{navigator.vibrate&&navigator.vibrate(10);}catch{}
    trackTouchDrag(press.x,press.y);
    touchDrag.raf=requestAnimationFrame(touchAutoScroll);
  }
  function trackTouchDrag(x,y){
    const d=touchDrag;if(!d)return;
    d.x=x;d.y=y;
    d.ghost.style.transform=`translate(${Math.round(x-d.dx)}px,${Math.round(y-d.dy)}px)`;
    const under=document.elementFromPoint(x,y),cell=under&&board.contains(under)?under.closest('.todo-cell'):null;
    clearDropMarks();
    d.cell=cell;d.before=null;
    if(!cell)return;
    cell.classList.add('drop-target');
    d.before=beforeCard(cell,y);
    if(d.before)d.before.classList.add('drop-before');
  }
  // Holding the card near an edge scrolls the board sideways or the page up and down.
  function touchAutoScroll(){
    const d=touchDrag;if(!d)return;
    const b=board.getBoundingClientRect(),beforeLeft=board.scrollLeft,beforeTop=window.scrollY;
    if(d.x<b.left+touchEdge)board.scrollLeft-=touchScrollStep;
    else if(d.x>b.right-touchEdge)board.scrollLeft+=touchScrollStep;
    if(d.y<touchEdge)window.scrollBy(0,-touchScrollStep);
    else if(d.y>window.innerHeight-touchEdge)window.scrollBy(0,touchScrollStep);
    if(board.scrollLeft!==beforeLeft||window.scrollY!==beforeTop)trackTouchDrag(d.x,d.y);
    d.raf=requestAnimationFrame(touchAutoScroll);
  }
  function endTouchDrag(commit){
    const d=touchDrag;if(!d)return;
    touchDrag=null;
    cancelAnimationFrame(d.raf);
    d.ghost.remove();d.card.classList.remove('dragging');clearDropMarks();
    if(!commit||!d.cell)return;
    // Dropped where it already was: nothing to save.
    let next=d.card.nextElementSibling;if(next&&!next.classList.contains('todo-card'))next=null;
    if(d.cell===d.card.parentElement&&d.before===next)return;
    moveCard(d.id,d.cell,d.before);
  }
  category.addEventListener('change',draw);
  mine.addEventListener('change',draw);
  await refresh();
  return root;
}

// Linked AI tasks (start_task todo_id / link_task_todo) and their latest pull request.
const todoPRLabels={unknown:'확인 전',open:'PR 열림',closed:'PR 닫힘',merged:'PR 병합',deployed:'PR 배포'};
function todoLinkPills(t){
  if(!t.task_count&&!t.open_issue_count)return null;
  const row=el('div',undefined,'todo-links');
  if(t.task_count)row.append(el('span',`작업 ${t.task_count}`+(t.open_task_count?` · 진행 중 ${t.open_task_count}`:''),'todo-link-pill'+(t.open_task_count?' active':'')));
  const pr=(t.pull_requests||[])[0];
  if(pr)row.append(el('span',`#${pr.number} ${todoPRLabels[pr.state]||pr.state}`,'todo-link-pill pr-'+pr.state));
  if(t.open_issue_count)row.append(el('span',`질의 ${t.open_issue_count}`,'todo-link-pill issue'));
  if(t.blocking_issue_count)row.append(el('span',`보류 ${t.blocking_issue_count}`,'todo-link-pill blocking'));
  return row;
}

// Questions and issues linked to a to-do, shown in its dialog.
async function todoIssuesSection(project,t){
  const box=el('section',undefined,'todo-tasks');
  box.append(el('h3','연결된 질의·이슈'));
  const list=el('ul');box.append(list);list.append(el('li','불러오는 중…','muted'));
  try{
    const issues=await tool('list_issues',{project_id:project.id,todo_id:t.id,status:'all',limit:30});
    if(!issues.length){list.replaceWith(el('p','연결된 질의·이슈가 없습니다. 이슈·질의 화면에서 “할 일 연결”로 붙이거나, 작업 중에 남긴 질의는 그 작업의 할 일에 자동으로 연결됩니다.','small muted'));return box;}
    list.replaceChildren(...issues.map(i=>{
      const li=el('li');
      li.append(el('span',issueStatusLabels[i.status]||i.status,'pill issue-'+i.status));
      if(i.blocking&&i.status==='open')li.append(document.createTextNode(' '),el('span','작업 보류','pill error'));
      li.append(document.createTextNode(` ${issueKindLabels[i.kind]||i.kind} · ${i.title} `),el('span',`담당 ${i.assignee_name||'미지정'}`,'small muted'));
      return li;
    }));
  }catch(e){list.replaceChildren(el('li',e.message,'muted'));}
  const go=btn('이슈 · 질의 화면으로',()=>{document.querySelector('.todo-dialog')?.close();render('issues');});
  box.append(go);
  return box;
}

// The tasks linked to a to-do, shown in its dialog.
async function todoTasksSection(project,t){
  const box=el('section',undefined,'todo-tasks');
  box.append(el('h3','연결된 AI 작업'));
  if(!t.task_count){box.append(el('p','아직 연결된 작업이 없습니다. AI가 start_task에 todo_id를 넘기거나, 진행 중 작업 화면에서 “할 일 연결”로 붙이면 시작 시 진행 중, 완료 시 완료로 자동 이동합니다.','small muted'));return box;}
  const list=el('ul');box.append(list);list.append(el('li','불러오는 중…','muted'));
  try{
    const runs=await tool('list_task_runs',{project_id:project.id,todo_id:t.id,limit:20});
    list.replaceChildren(...runs.map(r=>{
      const li=el('li');
      li.append(el('span',taskStatusLabels[r.status]||r.status,'pill task-'+r.status),document.createTextNode(' '+r.title+' '));
      li.append(el('span',`${r.created_by_name||''} · ${new Date(r.started_at).toLocaleDateString()}`,'small muted'));
      for(const pr of r.pull_requests||[]){const a=el('a',` #${pr.number} ${todoPRLabels[pr.state]||pr.state}`,'small');a.href=pr.url;a.target='_blank';a.rel='noopener noreferrer';li.append(a);}
      return li;
    }));
  }catch(e){list.replaceChildren(el('li',e.message,'muted'));}
  return box;
}

function todayISO(){const d=new Date();return `${d.getFullYear()}-${String(d.getMonth()+1).padStart(2,'0')}-${String(d.getDate()).padStart(2,'0')}`;}

// Add (no id) or edit a to-do. Viewers get a read-only dialog.
function todoDialog(parent,project,ctx,t){
  const canWrite=['owner','admin','editor'].includes(project.role), editing=!!t.id;
  const dialog=el('dialog',undefined,'review-dialog todo-dialog'),form=el('form');
  dialog.append(el('h2',editing?(canWrite?'할 일 수정':t.title):'새 할 일'));
  const title=field(form,'제목','title','text',t.title||'');title.maxLength=200;
  const bodyLabel=el('label','내용 (선택)'),body=el('textarea');body.name='body';body.rows=4;body.maxLength=10000;body.value=t.body||'';bodyLabel.append(body);form.append(bodyLabel);
  const grid=el('div',undefined,'todo-form-grid');form.append(grid);
  const status=selectField(grid,'상태','status',todoStatuses);status.value=t.status||'not_started';
  const priority=selectField(grid,'우선순위','priority',todoPriorities);priority.value=t.priority||'medium';
  const cat=field(grid,'분류 (선택)','category','text',t.category||'');cat.required=false;cat.maxLength=50;
  const listID='todo-categories';const dl=el('datalist');dl.id=listID;
  [...new Set(ctx.todos.map(x=>x.category).filter(Boolean))].forEach(c=>dl.append(new Option(c)));cat.setAttribute('list',listID);grid.append(dl);
  const progress=field(grid,'진행률 % (비우면 표시 안 함)','progress','number',t.progress==null?'':String(t.progress));progress.required=false;progress.min='0';progress.max='100';progress.step='1';
  const members=(ctx.members||[]).filter(m=>!m.disabled||m.user_id===t.assignee_id);
  const assignee=selectField(grid,'담당자','assignee_id',[['','없음'],...members.map(m=>[m.user_id,m.name])]);
  if(t.assignee_id&&![...assignee.options].some(o=>o.value===t.assignee_id))assignee.append(new Option(t.assignee_name||t.assignee_id,t.assignee_id));
  assignee.value=t.assignee_id||'';
  const due=field(grid,'마감일 (선택)','due_date','date',t.due_date||'');due.required=false;
  if(editing){
    form.append(el('p',`${t.created_by_name||''} 작성 · ${new Date(t.updated_at).toLocaleString()} 수정`,'small muted'));
    const slot=el('div');form.append(slot);
    Promise.all([todoTasksSection(project,t),todoIssuesSection(project,t)]).then(s=>slot.replaceWith(...s));
  }
  const actions=el('div',undefined,'actions review-dialog-actions');
  const close=btn('닫기',()=>dialog.close(),'secondary');
  if(!canWrite){form.querySelectorAll('input,select,textarea').forEach(n=>n.disabled=true);actions.append(close);}
  else{
    if(editing)actions.append(btn('삭제',async()=>{
      if(!confirm(`“${t.title}”을(를) 삭제할까요? 되돌릴 수 없습니다.`))return;
      await tool('delete_todo',{project_id:project.id,todo_id:t.id});
      dialog.close();notify('할 일을 삭제했습니다.');await ctx.reload();
    },'danger todo-delete'));
    const save=el('button',editing?'저장':'추가');save.type='submit';
    actions.append(close,save);
    form.addEventListener('submit',async e=>{
      e.preventDefault();save.disabled=true;
      try{
        const d={project_id:project.id,title:title.value.trim(),body:body.value,status:status.value,priority:priority.value,category:cat.value.trim()};
        if(progress.value!=='')d.progress=Number(progress.value);
        if(assignee.value)d.assignee_id=assignee.value;
        if(due.value)d.due_date=due.value;
        if(editing){d.todo_id=t.id;d.expected_revision=t.revision;}
        await tool('save_todo',d);
        dialog.close();notify(editing?'저장했습니다.':'할 일을 추가했습니다.');await ctx.reload();
      }catch(err){notify(err.message,true);}finally{save.disabled=false;}
    });
  }
  form.append(actions);dialog.append(form);
  dialog.addEventListener('close',()=>dialog.remove(),{once:true});
  dialog.addEventListener('click',e=>{if(e.target!==dialog)return;const b=dialog.getBoundingClientRect();if(e.clientX<b.left||e.clientX>b.right||e.clientY<b.top||e.clientY>b.bottom)dialog.close();});
  parent.append(dialog);dialog.showModal();title.focus();
}
