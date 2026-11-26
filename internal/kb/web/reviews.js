'use strict';

const reviewKinds={brief:'기획',decision:'결정',history:'작업 이력',artifact:'산출물',reference:'참고',glossary:'용어집',wiki:'위키'};

async function reviewsPage() {
  const projects=[];
  for (let offset=0; offset<=100000; offset+=50) {
    const batch=await tool('list_projects',{limit:50,offset});
    projects.push(...batch); if(batch.length<50)break;
  }
  const root=el('div',undefined,'review-page');
  if (!projects.length) { root.append(el('p','먼저 프로젝트에 참여해 주세요.','empty')); return root; }
  const project=projectSelect(projects);
  const summary=el('p','','small muted');summary.setAttribute('role','status');
  const list=el('div',undefined,'review-grid');
  root.append(summary,list);
  let generation=0;
  async function refresh() {
    const ticket=++generation, current=projects.find(p=>p.id===project.value);
    list.replaceChildren(el('p','불러오는 중…','muted'));
    const drafts=[];
    for (let offset=0; offset<=1000; offset+=50) {
      const batch=await tool('list_documents',{project_id:current.id,status:'draft',limit:50,offset});
      drafts.push(...batch); if(batch.length<50)break;
    }
    if (ticket!==generation) return;
    const canReview=['owner','admin','editor'].includes(current.role);
    if (current.id===currentProjectID) refreshReviewBadge();
    summary.textContent=`검토 대기 ${drafts.length}건`+(canReview?'':' · 조회 권한만 있어 승인·반려할 수 없습니다.');
    if (!drafts.length) { list.replaceChildren(el('p','검토할 draft 문서가 없습니다.','empty')); return; }
    list.replaceChildren(...drafts.map(d=>draftCard(current,d,canReview)));
  }
  function draftCard(current,d,canReview) {
    const c=card(d.title);c.classList.add('review-card');
    const meta=el('p',`${reviewKinds[d.kind]||d.kind} · 버전 ${d.revision} · 수정 ${new Date(d.updated_at).toLocaleString()}`,'small muted');
    c.append(meta);
    if (d.tags?.length) c.append(el('p',d.tags.join(' · '),'small review-tags'));
    c.append(btn('본문 보기',()=>openDraftDialog(c,current,d,meta.textContent),'secondary review-open'));
    if (!canReview) return c;
    const label=el('label','검토 사유 (필수, 버전 기록에 남음)'), reason=el('textarea');
    reason.rows=2;reason.maxLength=900;reason.required=true;label.append(reason);
    const actions=el('div',undefined,'actions');
    const decide=(decision,question)=>async()=>{
      const text=reason.value.trim();
      if (!text) { reason.focus(); throw Error('검토 사유를 입력해 주세요.'); }
      if (!confirm(question)) return;
      await tool('review_document',{project_id:current.id,document_id:d.id,expected_revision:d.revision,decision,reason:text});
      notify(decision==='approve'?'승인했습니다. 이제 검색 대상입니다.':'반려했습니다. 문서는 보관 상태로 남습니다.');
      refreshReviewBadge();
      await refresh();
    };
    actions.append(btn('승인',decide('approve','이 문서를 active로 승인할까요?'),''),btn('반려',decide('reject','이 문서를 반려하고 보관(archived)할까요?'),'danger'));
    c.append(label,actions);
    return c;
  }
  project.addEventListener('change',()=>refresh().catch(e=>notify(e.message,true)));
  await refresh();
  return root;
}

// Shows a draft's full Markdown body in a modal; it closes on 닫기, Esc or a backdrop click.
function openDraftDialog(parent,project,d,metaText) {
  const dialog=el('dialog',undefined,'review-dialog'), content=el('div',undefined,'review-dialog-body');
  content.append(el('p','불러오는 중…','muted'));
  const close=btn('닫기',()=>dialog.close(),'secondary');
  dialog.append(el('h2',d.title),el('p',metaText,'small muted'),content,el('div',undefined,'actions review-dialog-actions'));
  dialog.lastElementChild.append(close);
  dialog.addEventListener('close',()=>dialog.remove(),{once:true});
  dialog.addEventListener('click',e=>{if(e.target!==dialog)return;const r=dialog.getBoundingClientRect();if(e.clientX<r.left||e.clientX>r.right||e.clientY<r.top||e.clientY>r.bottom)dialog.close();});
  parent.append(dialog);dialog.showModal();close.focus();
  tool('get_document',{project_id:project.id,document_id:d.id})
    .then(doc=>content.replaceChildren(renderMarkdown(doc.content,'vector-content',{projectId:doc.project_id})))
    .catch(e=>content.replaceChildren(el('p',e.message,'muted')));
}
