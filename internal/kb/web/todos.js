'use strict';

// 할 일 보드: project to-dos in status columns × priority rows. Cards drag
// between cells (move_todo) with the mouse or, after a long press, a finger,
// and open an edit dialog (save_todo/delete_todo). Completed to-dos are not
// on the board; they have their own page (todosDonePage).
const todoStatuses=[['not_started','시작 전'],['in_progress','진행 중'],['in_review','검토대기'],['completed','완료'],['idea','아이디어']];
const todoBoardStatuses=todoStatuses.filter(([s])=>s!=='completed');
// Only work under way can be ticked off from its card.
const todoCheckable=new Set(['in_progress','in_review']);
const todoPriorities=[['high','높음'],['medium','보통'],['low','낮음']];
const todoStatusLabel=Object.fromEntries(todoStatuses), todoPriorityLabel=Object.fromEntries(todoPriorities);
// Live filter over the cards already loaded: every word must appear in the
// title, body or category, like list_todos' query on the server.
const todoWords=value=>value.trim().toLowerCase().split(/\s+/).filter(Boolean);
const todoMatches=(t,words)=>{const text=`${t.title} ${t.body||''} ${t.category||''}`.toLowerCase();return words.every(w=>text.includes(w));};
function todoSearchBox(onInput){
  const box=el('input','',undefined);box.type='search';box.className='todo-search';
  box.placeholder='제목·본문 검색';box.setAttribute('aria-label','할 일 검색');box.maxLength=200;
  box.addEventListener('input',onInput);
  return box;
}
const todoCollapseKey='kb.todo.collapsed';

function todoCollapsed(){try{return new Set(JSON.parse(localStorage.getItem(todoCollapseKey)||'[]'));}catch{return new Set();}}
function saveTodoCollapsed(set){try{localStorage.setItem(todoCollapseKey,JSON.stringify([...set]));}catch{}}

// Hidden columns (statuses) and rows (priorities), kept per browser like the
// collapsed rows. Only the display changes; the to-dos themselves stay put.
const todoHiddenKeys={status:'kb.todo.hidden.status',priority:'kb.todo.hidden.priority'};
function todoHidden(kind,known){
  try{const v=JSON.parse(localStorage.getItem(todoHiddenKeys[kind])||'[]');return new Set(Array.isArray(v)?v.filter(x=>known.includes(x)):[]);}catch{return new Set();}
}
function saveTodoHidden(kind,set){try{localStorage.setItem(todoHiddenKeys[kind],JSON.stringify([...set]));}catch{}}

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
  // Waiting cards with no activity for a while (list_stale_todos) get a badge
  // and can be shown alone to tidy the board.
  const staleLabel=el('label',undefined,'inline-check todo-stale-filter'),staleOnly=el('input'),staleText=el('span','오래 묵은 것만');staleOnly.type='checkbox';
  staleLabel.append(staleOnly,staleText);staleLabel.hidden=true;
  const summary=el('span','','small muted');summary.setAttribute('role','status');
  const hideWrap=el('div',undefined,'todo-hide'),hideBtn=el('button',undefined,'secondary todo-hide-btn'),hideMenu=el('div',undefined,'todo-hide-menu');
  hideBtn.type='button';hideBtn.setAttribute('aria-haspopup','true');hideBtn.setAttribute('aria-expanded','false');
  hideMenu.hidden=true;hideMenu.setAttribute('role','group');hideMenu.setAttribute('aria-label','열·행 숨기기');
  hideWrap.append(hideBtn,hideMenu);
  const search=todoSearchBox(()=>draw());
  filters.append(category,search,mineLabel,staleLabel,hideWrap,summary);
  bar.append(filters);
  const barActions=el('div',undefined,'todo-bar-actions');
  const doneLink=btn('완료된 할 일',()=>render('todosDone'),'secondary todo-done-link');
  barActions.append(doneLink);
  if(canWrite)barActions.append(btn('+ 새 할 일',()=>todoDialog(root,project,ctx,{status:'not_started',priority:'medium'}),'primary'));
  bar.append(barActions);
  const board=el('div',undefined,'todo-board');
  const issuePanel=el('section',undefined,'todo-issues');
  root.append(bar,board,issuePanel);
  // complete: ctx.todos holds every to-do, so the dialog can pick relations.
  const ctx={todos:[],members:[],stale:new Map(),staleDays:14,reload:refresh,complete:true};
  let generation=0;
  const collapsed=todoCollapsed();
  const hidden={status:todoHidden('status',todoBoardStatuses.map(x=>x[0])),priority:todoHidden('priority',todoPriorities.map(x=>x[0]))};
  const hiddenCount=()=>hidden.status.size+hidden.priority.size;
  const shownStatuses=()=>todoBoardStatuses.filter(([s])=>!hidden.status.has(s));
  const shownPriorities=()=>todoPriorities.filter(([p])=>!hidden.priority.has(p));
  function setHidden(kind,value,off){
    off?hidden[kind].add(value):hidden[kind].delete(value);
    saveTodoHidden(kind,hidden[kind]);draw();
  }
  function drawHideMenu(){
    const n=hiddenCount();
    hideBtn.textContent=n?`숨김 ${n}`:'숨기기';
    hideBtn.classList.toggle('active',n>0);
    hideBtn.setAttribute('aria-label',n?`숨긴 열·행 ${n}개, 설정 열기`:'열·행 숨기기');
    const parts=[];
    for(const [kind,title,list,shown] of [['status','상태 열',todoBoardStatuses,shownStatuses()],['priority','우선순위 행',todoPriorities,shownPriorities()]]){
      const group=el('fieldset',undefined,'todo-hide-group');group.append(el('legend',title));
      for(const [v,label] of list){
        const row=el('label',undefined,'inline-check'),box=el('input');box.type='checkbox';box.checked=!hidden[kind].has(v);
        // At least one column and one row stay visible so the board is never blank.
        box.disabled=box.checked&&shown.length<=1;
        box.addEventListener('change',()=>setHidden(kind,v,!box.checked));
        row.append(box,el('span',label,kind==='status'?'todo-status todo-s-'+v:'todo-priority todo-p-'+v));
        group.append(row);
      }
      parts.push(group);
    }
    const all=btn('모두 보이기',()=>{hidden.status.clear();hidden.priority.clear();saveTodoHidden('status',hidden.status);saveTodoHidden('priority',hidden.priority);draw();},'secondary');
    all.disabled=!n;
    parts.push(all);
    hideMenu.replaceChildren(...parts);
  }
  function toggleHideMenu(open){
    hideMenu.hidden=!open;hideBtn.setAttribute('aria-expanded',String(open));
  }
  hideBtn.addEventListener('click',()=>toggleHideMenu(hideMenu.hidden));
  document.addEventListener('click',function away(e){
    if(!root.isConnected){document.removeEventListener('click',away);return;}
    if(!hideMenu.hidden&&!hideWrap.contains(e.target))toggleHideMenu(false);
  });
  hideWrap.addEventListener('keydown',e=>{if(e.key==='Escape'&&!hideMenu.hidden){toggleHideMenu(false);hideBtn.focus();}});

  async function refresh(){
    const ticket=++generation;
    const [todos,members,stale]=await Promise.all([
      tool('list_todos',{project_id:project.id}),
      ctx.members.length?ctx.members:tool('list_project_members',{project_id:project.id}).catch(()=>[]),
      tool('list_stale_todos',{project_id:project.id}).catch(()=>null),
    ]);
    if(ticket!==generation)return;
    ctx.todos=todos;ctx.members=members;
    ctx.stale=new Map((stale?.todos||[]).map(x=>[x.id,x]));
    if(stale?.older_than_days)ctx.staleDays=stale.older_than_days;
    staleLabel.hidden=!ctx.stale.size&&!staleOnly.checked;
    staleText.textContent=`${ctx.staleDays}일 넘게 멈춘 것만 (${stale?.total??ctx.stale.size})`;
    drawIssuePanel(project,issuePanel,todos);
    const done=todos.filter(t=>t.status==='completed').length;
    doneLink.textContent=`완료된 할 일 ${done}건`;
    const cats=[...new Set(todos.map(t=>t.category).filter(Boolean))].sort((a,b)=>a.localeCompare(b));
    const chosen=category.value;
    category.replaceChildren(new Option('모든 분류',''),...cats.map(c=>new Option(c,c)));
    category.value=cats.includes(chosen)?chosen:'';
    draw();
  }
  function visible(){
    const words=todoWords(search.value);
    return ctx.todos.filter(t=>t.status!=='completed'&&(!category.value||t.category===category.value)&&(!mine.checked||t.assignee_id===me.id)&&(!staleOnly.checked||ctx.stale.has(t.id))&&todoMatches(t,words));
  }
  function draw(){
    if(touchDrag)endTouchDrag(false);
    const items=visible();
    const statuses=shownStatuses(),priorities=shownPriorities();
    const onBoard=items.filter(t=>!hidden.status.has(t.status)&&!hidden.priority.has(t.priority)).length;
    summary.textContent=`${onBoard}건`+(onBoard<items.length?` · 숨김 ${items.length-onBoard}건`:'')+(ctx.todos.length>=500?' · 최대 500건까지 표시':'');
    drawHideMenu();
    const inner=el('div',undefined,'todo-board-inner');
    inner.style.setProperty('--todo-cols',String(statuses.length));
    const head=el('div',undefined,'todo-row todo-columns');
    head.append(el('div'));
    for(const [s,label] of statuses){
      const h=el('div',undefined,'todo-col-head');
      h.append(el('span',label,'todo-status todo-s-'+s),el('span',String(items.filter(t=>t.status===s).length),'todo-count'));
      head.append(h);
    }
    inner.append(head);
    for(const [pr,plabel] of priorities){
      const lane=el('section',undefined,'todo-lane'),inLane=items.filter(t=>t.priority===pr);
      const toggle=el('button',undefined,'todo-lane-toggle');toggle.type='button';
      const open=!collapsed.has(pr);toggle.setAttribute('aria-expanded',String(open));
      toggle.append(el('span',open?'▾':'▸','todo-caret'),el('span',plabel,'todo-priority todo-p-'+pr),el('span',String(inLane.length),'todo-count'));
      toggle.addEventListener('click',()=>{collapsed.has(pr)?collapsed.delete(pr):collapsed.add(pr);saveTodoCollapsed(collapsed);draw();});
      lane.append(toggle);
      if(open){
        const row=el('div',undefined,'todo-row');
        row.append(el('div'));
        for(const [s,slabel] of statuses){
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
    if(items.length&&!onBoard)board.append(el('p','조건에 맞는 할 일이 모두 숨겨진 열·행에 있습니다. 숨김 설정에서 보이게 할 수 있습니다.','empty'));
    if(!items.length&&ctx.todos.length)board.append(el('p','조건에 맞는 할 일이 없습니다.','empty'));
    if(!ctx.todos.length)board.append(el('p',canWrite?'아직 할 일이 없습니다. 칸의 “새로 만들기”나 오른쪽 위 버튼으로 추가하세요.':'아직 할 일이 없습니다.','empty'));
  }
  function todoCard(t){
    const c=el('article',undefined,'todo-card');c.tabIndex=0;c.dataset.id=t.id;
    c.setAttribute('aria-label',`${t.title}, ${todoStatusLabel[t.status]}`);
    if(t.parent_id)c.append(el('p','↳ '+(t.parent_title||'상위 할 일'),'todo-parent'));
    c.append(el('h3',t.title));
    const tags=el('div',undefined,'todo-tags');
    tags.append(el('span',todoStatusLabel[t.status],'todo-status todo-s-'+t.status));
    if(t.category)tags.append(el('span',t.category,'todo-category'));
    const idle=ctx.stale.get(t.id);
    if(idle){const b=el('span',`${idle.idle_days}일 정체`,'todo-stale');b.title=`${idle.idle_days}일 동안 수정·이동·연결 작업·질의 변경이 없었습니다. 끝났는지, 아직 필요한지 확인해 정리해 주세요.`;tags.append(b);c.classList.add('stale');}
    c.append(tags);
    if(todoCheckable.has(t.status)){
      const check=el('label',undefined,'todo-done'),box=el('input');box.type='checkbox';box.disabled=!canWrite;
      check.append(box,document.createTextNode('완료'));
      check.addEventListener('click',e=>e.stopPropagation());
      box.addEventListener('change',async()=>{
        if(!box.checked)return;
        box.disabled=true;
        try{
          await tool('move_todo',{project_id:project.id,todo_id:t.id,status:'completed',priority:t.priority});
          notify(`“${t.title}”을(를) 완료했습니다. “완료된 할 일”에서 볼 수 있습니다.`);
          await refresh();
        }
        catch(e){box.checked=false;box.disabled=!canWrite;notify(e.message,true);}
      });
      c.append(check);
    }
    if(t.progress!=null){
      const p=el('div',undefined,'todo-progress');p.setAttribute('role','progressbar');p.setAttribute('aria-valuemin','0');p.setAttribute('aria-valuemax','100');p.setAttribute('aria-valuenow',String(t.progress));
      const track=el('span',undefined,'todo-progress-track'),fill=el('span',undefined,'todo-progress-fill');fill.style.width=t.progress+'%';
      track.append(fill);p.append(track,el('span',t.progress+'%','todo-progress-label'));c.append(p);
    }
    const links=todoLinkPills(t);if(links)c.append(links);
    const rel=todoRelationPills(t);if(rel)c.append(rel);
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
  staleOnly.addEventListener('change',draw);
  await refresh();
  return root;
}

// 완료된 할 일: completed to-dos, newest first, off the board. A to-do can be
// opened (edit dialog) or put back into progress from here.
async function todosDonePage(){
  const root=el('div',undefined,'todo-page todo-done-page');
  const project=projectList.find(p=>p.id===currentProjectID);
  if(!project){const c=card('완료된 할 일');c.append(el('p','먼저 프로젝트에 참여하고 사이드바에서 현재 프로젝트를 선택해 주세요.','empty'));root.append(c);return root;}
  const canWrite=['owner','admin','editor'].includes(project.role);
  const bar=el('div',undefined,'todo-toolbar'),filters=el('div',undefined,'todo-filters');
  const category=el('select');category.setAttribute('aria-label','분류');
  const mineLabel=el('label',undefined,'inline-check'),mine=el('input');mine.type='checkbox';
  mineLabel.append(mine,document.createTextNode('내게 배정된 것만'));
  const summary=el('span','','small muted');summary.setAttribute('role','status');
  const search=todoSearchBox(()=>draw());
  filters.append(category,search,mineLabel,summary);
  bar.append(filters,btn('← 할 일 보드',()=>render('todos'),'secondary'));
  const list=el('div',undefined,'todo-done-list');
  root.append(bar,list);
  const ctx={todos:[],members:[],reload:refresh};
  let generation=0;
  async function refresh(){
    const ticket=++generation;
    const [todos,members]=await Promise.all([
      tool('list_todos',{project_id:project.id,status:'completed',limit:500}),
      ctx.members.length?ctx.members:tool('list_project_members',{project_id:project.id}).catch(()=>[]),
    ]);
    if(ticket!==generation)return;
    ctx.todos=todos.filter(t=>t.status==='completed').sort((a,b)=>when(b).localeCompare(when(a)));ctx.members=members;
    const cats=[...new Set(ctx.todos.map(t=>t.category).filter(Boolean))].sort((a,b)=>a.localeCompare(b));
    const chosen=category.value;
    category.replaceChildren(new Option('모든 분류',''),...cats.map(c=>new Option(c,c)));
    category.value=cats.includes(chosen)?chosen:'';
    draw();
  }
  function draw(){
    const words=todoWords(search.value);
    const items=ctx.todos.filter(t=>(!category.value||t.category===category.value)&&(!mine.checked||t.assignee_id===me.id)&&todoMatches(t,words));
    summary.textContent=`${items.length}건`+(ctx.todos.length>=500?' · 최근 500건까지 표시':'');
    if(!items.length){list.replaceChildren(el('p',ctx.todos.length?'조건에 맞는 완료된 할 일이 없습니다.':'아직 완료된 할 일이 없습니다. 보드에서 진행 중·검토대기 카드의 “완료”를 체크하면 여기로 옵니다.','empty'));return;}
    const rows=items.map(t=>{
      const title=el('button',t.title,'todo-done-title');title.type='button';
      title.addEventListener('click',()=>todoDialog(root,project,ctx,t));
      const links=todoLinkPills(t)||'—';
      const actions=el('div',undefined,'todo-done-actions');
      if(canWrite)actions.append(btn('진행 중으로 되돌리기',async()=>{
        await tool('move_todo',{project_id:project.id,todo_id:t.id,status:'in_progress',priority:t.priority});
        notify(`“${t.title}”을(를) 진행 중으로 되돌렸습니다.`);await refresh();
      },'secondary'));
      const done=when(t);
      return [done?new Date(done).toLocaleDateString():'—',title,el('span',todoPriorityLabel[t.priority]||t.priority,'todo-priority todo-p-'+t.priority),t.category||'—',t.assignee_name||'—',links,actions];
    });
    list.replaceChildren(table(['완료일','제목','우선순위','분류','담당자','연결',''],rows));
  }
  function when(t){return t.completed_at||t.updated_at||'';}
  category.addEventListener('change',draw);
  mine.addEventListener('change',draw);
  await refresh();
  return root;
}

// Open questions and issues under the board, so they are visible next to the
// cards. Failures leave the board itself untouched.
async function drawIssuePanel(project,panel,todos){
  try{
    const issues=await tool('list_issues',{project_id:project.id,limit:30});
    const byTodo=new Map(todos.map(t=>[t.id,t]));
    const blocking=issues.filter(i=>i.blocking&&i.status==='open').length;
    const head=el('div',undefined,'todo-issues-head');
    head.append(el('h2','열린 이슈 · 질의'),el('span',`${issues.length}건${issues.length>=30?' 이상':''}`+(blocking?` · 작업 보류 ${blocking}건`:''),'small muted'));
    head.append(btn('이슈 · 질의 화면으로',()=>render('issues'),'secondary'));
    const list=el('ul',undefined,'todo-issue-list');
    if(!issues.length)list.append(el('li','열린 이슈·질의가 없습니다.','muted'));
    for(const i of issues){
      const li=el('li');
      li.append(el('span',issueKindLabels[i.kind]||i.kind,'pill'),document.createTextNode(' '),el('span',issueStatusLabels[i.status]||i.status,'pill issue-'+i.status));
      if(i.blocking&&i.status==='open')li.append(document.createTextNode(' '),el('span','작업 보류','pill error'));
      li.append(document.createTextNode(' '+i.title+' '));
      const t=i.todo||(i.todo_id&&byTodo.get(i.todo_id));
      const meta=[t&&('할 일 · '+t.title),'담당 '+(i.assignee_name||'미지정')].filter(Boolean).join(' · ');
      li.append(el('span',meta,'small muted'));
      list.append(li);
    }
    panel.replaceChildren(head,list);
  }catch(e){panel.replaceChildren(el('p','이슈·질의를 불러오지 못했습니다: '+e.message,'small muted'));}
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

// Sub-items and predecessors (parent_id, blocked_by) on a card.
function todoRelationPills(t){
  const waiting=t.open_blocker_count||0;
  if(!t.subtask_count&&!(t.blocked_by||[]).length)return null;
  const row=el('div',undefined,'todo-links');
  if(t.subtask_count)row.append(el('span',`하위 ${t.subtasks_completed||0}/${t.subtask_count}`,'todo-link-pill'+(t.subtasks_completed===t.subtask_count?' pr-merged':'')));
  if(waiting){
    const names=(t.blocked_by||[]).filter(b=>b.status!=='completed').map(b=>b.title);
    const pill=el('span',`선행 대기 ${waiting}`,'todo-link-pill blocking');pill.title='먼저 끝나야 하는 할 일: '+names.join(', ');
    row.append(pill);
  }else if((t.blocked_by||[]).length)row.append(el('span','선행 완료','todo-link-pill pr-merged'));
  return row;
}

// Relation fields in the dialog. Pickers need every to-do (the board);
// elsewhere the relations are listed read-only and left unchanged on save.
function todoRelationsSection(ctx,t,canWrite){
  const box=el('section',undefined,'todo-tasks todo-relations');
  box.append(el('h3','상위 · 선행 할 일'));
  const byID=new Map(ctx.todos.map(x=>[x.id,x]));
  const label=x=>`${x.title} (${todoStatusLabel[x.status]||x.status})`;
  const startBlockers=(t.blocked_by||[]).map(b=>b.id);
  const children=t.id?ctx.todos.filter(x=>x.parent_id===t.id):[];
  const waiters=t.id?ctx.todos.filter(x=>(x.blocked_by||[]).some(b=>b.id===t.id)):[];
  const listOf=(title,items)=>{if(!items.length)return;const ul=el('ul');items.forEach(x=>ul.append(el('li',label(x))));box.append(el('p',title,'small muted'),ul);};
  if(!ctx.complete||!canWrite){
    if(t.parent_id)box.append(el('p','상위: '+(t.parent_title||t.parent_id),'small'));
    listOf('먼저 끝나야 하는 할 일',t.blocked_by||[]);
    listOf('하위 항목',children);
    listOf('이 할 일을 기다리는 할 일',waiters);
    if(box.childElementCount===1)box.append(el('p','상위·선행 관계가 없습니다.','small muted'));
    if(canWrite)box.append(el('p','관계는 할 일 보드의 수정 창에서 바꿀 수 있습니다.','small muted'));
    return {node:box,value:()=>({})};
  }
  // Parent: a top-level to-do other than this one. A to-do with sub-items stays top-level.
  const parentOptions=[['','없음 (최상위)'],...ctx.todos.filter(x=>x.id!==t.id&&!x.parent_id).map(x=>[x.id,label(x)])];
  const parent=selectField(box,'상위 할 일 (이 할 일을 하위 항목으로)','parent_id',parentOptions);
  if(t.parent_id&&!byID.has(t.parent_id))parent.append(new Option(t.parent_title||t.parent_id,t.parent_id));
  parent.value=t.parent_id||'';
  if(t.subtask_count||children.length){parent.disabled=true;box.append(el('p',`하위 항목 ${t.subtask_count||children.length}개가 있어 다른 할 일의 하위가 될 수 없습니다(한 단계만).`,'small muted'));}
  // Predecessors: a filterable checklist, chosen ones first.
  const chosen=new Set(startBlockers);
  const wrap=el('div',undefined,'todo-blockers');
  const search=el('input');search.type='search';search.placeholder='선행 할 일 찾기';search.setAttribute('aria-label','선행 할 일 찾기');
  const picks=el('div',undefined,'todo-blocker-list');picks.setAttribute('role','group');picks.setAttribute('aria-label','먼저 끝나야 하는 할 일');
  const candidates=ctx.todos.filter(x=>x.id!==t.id).sort((a,b)=>(chosen.has(b.id)-chosen.has(a.id))||((a.status==='completed')-(b.status==='completed'))||a.title.localeCompare(b.title));
  function drawPicks(){
    const q=search.value.trim().toLowerCase();
    const rows=candidates.filter(x=>chosen.has(x.id)||!q||x.title.toLowerCase().includes(q)).map(x=>{
      const l=el('label',undefined,'inline-check'),cb=el('input');cb.type='checkbox';cb.checked=chosen.has(x.id);
      cb.addEventListener('change',()=>{cb.checked?chosen.add(x.id):chosen.delete(x.id);count.textContent=`${chosen.size}개 선택`;});
      l.append(cb,document.createTextNode(label(x)));return l;
    });
    picks.replaceChildren(...(rows.length?rows:[el('p','맞는 할 일이 없습니다.','small muted')]));
  }
  const count=el('span',`${chosen.size}개 선택`,'small muted');
  search.addEventListener('input',drawPicks);
  const head=el('div',undefined,'todo-blockers-head');head.append(el('span','먼저 끝나야 하는 할 일 (최대 20개)'),count);
  wrap.append(head,search,picks);box.append(wrap);drawPicks();
  listOf('하위 항목',children);
  listOf('이 할 일을 기다리는 할 일',waiters);
  return {node:box,value(){
    const out={};
    if(parent.value!==(t.parent_id||''))out.parent_id=parent.value;
    const now=[...chosen];
    if(now.length!==startBlockers.length||now.some(id=>!startBlockers.includes(id)))out.blocked_by=now;
    return out;
  }};
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
  const relations=todoRelationsSection(ctx,t,canWrite);form.append(relations.node);
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
        Object.assign(d,relations.value());
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
