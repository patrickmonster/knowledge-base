'use strict';

const documentKinds={brief:'기획',decision:'결정',history:'작업 이력',artifact:'산출물',reference:'참고',glossary:'용어집',wiki:'위키'};
const documentStatuses={active:'active',draft:'draft',archived:'archived'};

async function documentsPage() {
  const focus=takePageFocus('documents');
  const root=el('div'), projects=[];
  for (let offset=0; offset<=100000; offset+=50) {
    const batch=await tool('list_projects',{limit:50,offset});
    projects.push(...batch); if(batch.length<50)break;
  }
  const controls=card();
  root.append(controls);
  if (!projects.length) { controls.append(el('p','먼저 프로젝트에 참여해 주세요.','empty')); return root; }

  const form=el('form',undefined,'vector-controls document-filters');
  const project=projectSelect(projects);
  const kind=selectField(form,'종류','kind',[['','전체'],...Object.entries(documentKinds)]);
  const status=selectField(form,'상태','status',[['','전체'],...Object.entries(documentStatuses)]);
  const author=selectField(form,'작성자 (작성·최종 수정)','author',[['','전체']]);
  const tags=field(form,'태그 (쉼표로 구분, 모두 포함)','tags');tags.required=false;tags.placeholder='예: 운영, Railway';
  const after=field(form,'수정일 시작','updated_after','date');after.required=false;
  const before=field(form,'수정일 끝','updated_before','date');before.required=false;
  const staleDays=field(form,'재확인 기준 (일)','stale_days','number','90');staleDays.min=1;staleDays.max=3650;
  const staleLabel=el('label',undefined,'document-check'), staleOnly=el('input');
  staleOnly.type='checkbox';staleOnly.name='stale_only';staleLabel.append(staleOnly,document.createTextNode('재확인 필요만'));
  form.append(staleLabel);
  const search=el('button','조회');form.append(search);
  controls.append(form);
  const summary=el('p','','small muted');summary.setAttribute('role','status');
  controls.append(summary);
  const list=el('tbody'), more=btn('더 보기',()=>loadMore(),'secondary');
  more.hidden=true;
  const tableWrap=el('div',undefined,'table-wrap document-table'), tbl=el('table'), headRow=el('tr'), thead=el('thead');
  // Header buttons change the server-side sort; the same column again flips the order.
  const columns=[['title','제목'],['kind','종류'],['status','상태'],['revision','버전'],['author','작성자'],['updated_at','수정일']];
  const defaultOrder=key=>key==='updated_at'||key==='revision'?'desc':'asc';
  let sortKey='updated_at', sortOrder='desc';
  const sortHeads=columns.map(([key,label])=>{
    const th=el('th'), b=el('button',undefined,'sort-button');
    b.type='button';b.append(el('span',label),el('span','','sort-indicator'));
    b.addEventListener('click',()=>{
      if (sortKey===key) sortOrder=sortOrder==='asc'?'desc':'asc'; else { sortKey=key;sortOrder=defaultOrder(key); }
      updateSortHeads();load(true).catch(e=>notify(e.message,true));
    });
    th.append(b);headRow.append(th);return {key,th,b};
  });
  function updateSortHeads() {
    for (const {key,th,b} of sortHeads) {
      const active=key===sortKey;
      th.setAttribute('aria-sort',active?(sortOrder==='asc'?'ascending':'descending'):'none');
      b.classList.toggle('is-sorted',active);
      b.lastChild.textContent=active?(sortOrder==='asc'?'▲':'▼'):'';
    }
  }
  updateSortHeads();
  thead.append(headRow);tbl.append(thead,list);tableWrap.append(tbl);
  const listCard=card();listCard.append(tableWrap,more);
  root.append(listCard);
  // Infinite scroll: when the "more" button scrolls into view, load the next page.
  // The button stays as a fallback (keyboard users, observer unsupported, after an error).
  let hasMore=false, busy=false;
  const observer='IntersectionObserver' in window?new IntersectionObserver(entries=>{
    if (entries.some(e=>e.isIntersecting)) loadMore();
  },{rootMargin:'200px 0px'}):null;
  observer?.observe(more);
  function loadMore() {
    if (!hasMore||busy) return;
    busy=true;more.disabled=true;more.textContent='불러오는 중…';
    load(false).catch(e=>notify(e.message,true)).finally(()=>{
      busy=false;more.disabled=false;more.textContent='더 보기';
      // Re-observe so a still-visible button (tall screen) triggers the next page too.
      if (observer&&hasMore&&more.isConnected) { observer.unobserve(more);observer.observe(more); }
    });
  }
  function message(text,cls) { const tr=el('tr'), td=el('td',text,cls); td.colSpan=6; tr.append(td); return tr; }

  let generation=0, offset=0, names={}, shown=0, staleShown=0;
  async function loadMembers() {
    const current=project.value;
    names={};
    author.replaceChildren(new Option('전체',''));
    try {
      const members=await tool('list_project_members',{project_id:current});
      if (project.value!==current) return;
      for (const m of members) { names[m.user_id]=m.name; author.append(new Option(m.name,m.user_id)); }
    } catch(e) { notify(e.message,true); }
  }
  function filters() {
    const input={project_id:project.value,limit:50,offset,sort:sortKey,order:sortOrder};
    if (kind.value) input.kind=kind.value;
    if (status.value) input.status=status.value;
    if (author.value) input.author=author.value;
    const tagList=tags.value.split(',').map(t=>t.trim()).filter(Boolean);
    if (tagList.length) input.tags=tagList;
    if (after.value) input.updated_after=after.value;
    if (before.value) input.updated_before=before.value;
    const days=Number(staleDays.value);
    if (days && days!==90) input.stale_days=days;
    if (staleOnly.checked) input.stale_only=true;
    return input;
  }
  async function load(reset) {
    if (after.value&&before.value&&after.value>before.value) throw Error('수정일 시작이 끝보다 늦습니다.');
    const ticket=reset?++generation:generation;
    if (reset) { offset=0;shown=0;staleShown=0;hasMore=false;more.hidden=true;list.replaceChildren(message('불러오는 중…','muted')); }
    const batch=await tool('list_documents',filters());
    if (ticket!==generation) return;
    if (reset) list.replaceChildren();
    offset+=batch.length;shown+=batch.length;
    staleShown+=batch.filter(d=>d.needs_recheck).length;
    list.append(...batch.map(row));
    if (!shown) list.replaceChildren(message('조건에 맞는 문서가 없습니다.','empty'));
    hasMore=batch.length>=50;more.hidden=!hasMore;
    summary.textContent=`${shown}건${batch.length<50?'':' 이상'} · 재확인 필요 ${staleShown}건 (기준 ${Number(staleDays.value)||90}일)`;
  }
  function person(id) { return names[id]||id?.slice(0,8)||'—'; }
  function byline(d) { return d.created_by===d.updated_by?person(d.created_by):`${person(d.created_by)} → ${person(d.updated_by)}`; }
  function titleCell(d) {
    const cell=el('div',undefined,'document-title');
    cell.append(el('strong',d.title));
    if (d.needs_recheck) {
      const badge=el('span','재확인 필요','pill stale');
      badge.title=`${d.days_since_update}일 동안 갱신되지 않았습니다 (기준 ${d.stale_after_days}일).`;
      cell.append(badge);
    }
    return cell;
  }
  function row(d) {
    const tr=el('tr',undefined,'document-row'+(d.needs_recheck?' is-stale':''));
    tr.tabIndex=0;tr.setAttribute('role','button');tr.setAttribute('aria-label',`${d.title} 열기`);
    const first=el('td');first.append(titleCell(d));
    if (d.tags?.length) first.append(el('p',d.tags.map(t=>'#'+t).join(' '),'small document-tags'));
    const statusCell=el('td');statusCell.append(el('span',d.status,'pill'));
    const updated=el('td');updated.append(el('span',new Date(d.updated_at).toLocaleString()),el('span',`${d.days_since_update}일 전`,'small muted document-age'));
    tr.append(first,el('td',documentKinds[d.kind]||d.kind),statusCell,el('td',String(d.revision)),el('td',byline(d)),updated);
    tr.addEventListener('click',()=>openDocument(d));
    tr.addEventListener('keydown',e=>{if(e.key==='Enter'||e.key===' '){e.preventDefault();openDocument(d);}});
    return tr;
  }
  function openDocument(d) {
    const dialog=el('dialog',undefined,'document-dialog'), close=btn('닫기',()=>dialog.close(),'secondary');
    const head=el('div',undefined,'document-dialog-head');
    head.append(titleCell(d),close);
    const meta=el('p',`${documentKinds[d.kind]||d.kind} · ${d.status} · 버전 ${d.revision} · ${byline(d)} · 수정 ${new Date(d.updated_at).toLocaleString()} (${d.days_since_update}일 전)`,'small muted document-meta');
    const top=el('div',undefined,'document-dialog-top');
    top.append(head,meta);
    if (d.tags?.length) top.append(el('p',d.tags.map(t=>'#'+t).join(' '),'small document-tags'));
    // Version bar: pick which revision the left column shows; compare opens the diff view.
    const versionBar=el('div',undefined,'document-version-bar');
    const versionLabel=el('label',undefined,'document-version-select'), version=el('select');
    version.name='revision';version.append(new Option(`버전 ${d.revision} (현재)`,String(d.revision)));
    versionLabel.append(document.createTextNode('문서 버전'),version);
    const compareBtn=btn('버전 비교',()=>toggleCompare(),'secondary');
    compareBtn.hidden=d.revision<2;compareBtn.setAttribute('aria-pressed','false');
    versionBar.append(versionLabel,compareBtn);
    top.append(versionBar);
    const docCol=el('section',undefined,'document-dialog-main'), sideCol=el('div',undefined,'document-dialog-side');
    const docBody=el('div');docBody.append(el('p','불러오는 중…','muted'));
    const compareBox=el('div',undefined,'document-compare');compareBox.hidden=true;
    docCol.append(el('code',d.id),docBody,compareBox);
    const body=el('div',undefined,'document-dialog-body');
    body.append(docCol,sideCol);
    dialog.append(top,body);
    dialog.addEventListener('close',()=>dialog.remove(),{once:true});
    dialog.addEventListener('click',e=>{if(e.target!==dialog)return;const r=dialog.getBoundingClientRect();if(e.clientX<r.left||e.clientX>r.right||e.clientY<r.top||e.clientY>r.bottom)dialog.close();});
    root.append(dialog);dialog.showModal();close.focus();

    let loadTicket=0;
    function toggleCompare() {
      const open=compareBox.hidden;
      compareBox.hidden=!open;docBody.hidden=open;
      compareBtn.setAttribute('aria-pressed',String(open));
      compareBtn.textContent=open?'본문 보기':'버전 비교';
      if (open&&!compareBox.childElementCount) compareBox.append(versionCompare(d,person));
    }
    async function showRevision(revision) {
      const ticket=++loadTicket;
      docBody.replaceChildren(el('p','불러오는 중…','muted'));
      const doc=await tool('get_document',{project_id:d.project_id,document_id:d.id,...(revision===d.revision?{}:{revision})});
      if (ticket!==loadTicket) return;
      const parts=[];
      if (revision!==d.revision) parts.push(el('p',`이전 버전 ${revision}을 보고 있습니다. 제목: ${doc.title}`,'small document-old-revision'));
      parts.push(renderMarkdown(doc.content,'vector-content',{projectId:doc.project_id}));
      docBody.replaceChildren(...parts);
    }
    version.addEventListener('change',()=>{
      if (!compareBox.hidden) toggleCompare();
      showRevision(Number(version.value)).catch(e=>docBody.replaceChildren(el('p',e.message,'muted')));
    });
    const role=projects.find(p=>p.id===d.project_id)?.role, canEdit=['owner','admin','editor'].includes(role);
    sideCol.append(linksPanel(d,canEdit));
    showRevision(d.revision).catch(e=>docBody.replaceChildren(el('p',e.message,'muted')));
    if (d.revision>1) tool('get_document_history',{project_id:d.project_id,document_id:d.id}).then(history=>{
      if (!history.length) return;
      const current=version.value;
      version.replaceChildren(...history.map(h=>new Option(`버전 ${h.revision}${h.revision===d.revision?' (현재)':''} · ${person(h.actor_id)} · ${new Date(h.created_at).toLocaleString()}`,String(h.revision))));
      version.value=current;if(!version.value)version.value=String(d.revision);
    }).catch(()=>{});
  }
  form.addEventListener('submit',e=>{e.preventDefault();search.disabled=true;load(true).catch(err=>notify(err.message,true)).finally(()=>search.disabled=false);});
  project.addEventListener('change',async()=>{author.value='';await loadMembers();load(true).catch(e=>notify(e.message,true));});
  await loadMembers();
  await load(true);
  if (focus?.document_id) await openFocused(focus.document_id);
  return root;

  // Opens one document asked for by another page (e.g. a task's history) once
  // the page is on screen; the list below stays as it is.
  async function openFocused(id) {
    try {
      const doc=await tool('get_document',{project_id:project.value,document_id:id});
      const days=Math.max(0,Math.floor((Date.now()-new Date(doc.updated_at).getTime())/86400000));
      setTimeout(()=>{if(root.isConnected)openDocument({...doc,content:undefined,days_since_update:days});},0);
    } catch(e) { notify(e.message,true); }
  }
}
