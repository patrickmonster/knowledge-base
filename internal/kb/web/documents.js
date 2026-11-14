'use strict';

const documentKinds={brief:'기획',decision:'결정',history:'작업 이력',artifact:'산출물',reference:'참고',glossary:'용어집'};
const documentStatuses={active:'active',draft:'draft',archived:'archived'};

async function documentsPage() {
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
  const list=el('tbody'), more=btn('더 보기',()=>load(false).catch(e=>notify(e.message,true)));
  more.hidden=true;
  const tableWrap=el('div',undefined,'table-wrap document-table'), tbl=el('table'), headRow=el('tr'), thead=el('thead');
  for (const h of ['제목','종류','상태','버전','작성자','수정일']) headRow.append(el('th',h));
  thead.append(headRow);tbl.append(thead,list);tableWrap.append(tbl);
  const listCard=card();listCard.append(tableWrap,more);
  root.append(listCard);
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
    const input={project_id:project.value,limit:50,offset};
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
    if (reset) { offset=0;shown=0;staleShown=0;list.replaceChildren(message('불러오는 중…','muted')); }
    const batch=await tool('list_documents',filters());
    if (ticket!==generation) return;
    if (reset) list.replaceChildren();
    offset+=batch.length;shown+=batch.length;
    staleShown+=batch.filter(d=>d.needs_recheck).length;
    list.append(...batch.map(row));
    if (!shown) list.replaceChildren(message('조건에 맞는 문서가 없습니다.','empty'));
    more.hidden=batch.length<50;
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
    const meta=el('p',`${documentKinds[d.kind]||d.kind} · ${d.status} · 버전 ${d.revision} · ${byline(d)} · 수정 ${new Date(d.updated_at).toLocaleString()} (${d.days_since_update}일 전)`,'small muted');
    const content=el('div');content.append(el('p','불러오는 중…','muted'));
    dialog.append(head,meta);
    if (d.tags?.length) dialog.append(el('p',d.tags.map(t=>'#'+t).join(' '),'small document-tags'));
    dialog.append(content);
    dialog.addEventListener('close',()=>dialog.remove(),{once:true});
    dialog.addEventListener('click',e=>{if(e.target!==dialog)return;const r=dialog.getBoundingClientRect();if(e.clientX<r.left||e.clientX>r.right||e.clientY<r.top||e.clientY>r.bottom)dialog.close();});
    root.append(dialog);dialog.showModal();close.focus();
    (async()=>{
      try {
        const doc=await tool('get_document',{project_id:d.project_id,document_id:d.id});
        const role=projects.find(p=>p.id===d.project_id)?.role, canEdit=['owner','admin','editor'].includes(role);
        const parts=[el('code',d.id),renderMarkdown(doc.content,'vector-content'),linksPanel(d,canEdit)];
        if (d.revision>1) {
          const versions=el('details',undefined,'version-details');
          versions.append(el('summary',`버전 비교 (현재 버전 ${d.revision})`));
          versions.addEventListener('toggle',()=>{if(versions.open&&versions.childElementCount===1)versions.append(versionCompare(d,person));});
          parts.push(versions);
        }
        content.replaceChildren(...parts);
      } catch(e) { content.replaceChildren(el('p',e.message,'muted')); }
    })();
  }
  form.addEventListener('submit',e=>{e.preventDefault();search.disabled=true;load(true).catch(err=>notify(err.message,true)).finally(()=>search.disabled=false);});
  project.addEventListener('change',async()=>{author.value='';await loadMembers();load(true).catch(e=>notify(e.message,true));});
  await loadMembers();
  await load(true);
  return root;
}
