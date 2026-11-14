'use strict';

const searchKinds={brief:'기획',decision:'결정',history:'작업 이력',artifact:'산출물',reference:'참고',glossary:'용어집'};
const searchRetrieval={hybrid:'의미+키워드','lexical':'키워드',semantic:'의미','lexical-fallback':'키워드 (임베딩 실패로 대체)'};

async function searchPage() {
  const root=el('div'), projects=[];
  for (let offset=0; offset<=100000; offset+=50) {
    const batch=await tool('list_projects',{limit:50,offset});
    projects.push(...batch); if(batch.length<50)break;
  }
  const controls=card('지식 검색');
  controls.append(el('p','참여 중인 여러 프로젝트를 한 번에 검색합니다. 결과마다 어느 프로젝트의 문서인지 표시합니다. 한 번에 최대 50개 프로젝트를 검색합니다.','muted'));
  root.append(controls);
  if (!projects.length) { controls.append(el('p','먼저 프로젝트에 참여해 주세요.','empty')); return root; }
  const form=el('form');
  const query=field(form,'검색어','query');query.maxLength=1000;query.placeholder='예: 첨부 업로드 권한';
  const mode=selectField(form,'검색 방식','mode',[['','기본 (의미+키워드)'],['lexical','키워드'],['semantic','의미']]);
  const status=selectField(form,'상태','status',[['active','active (확정)'],['draft','draft (검토 전)'],['archived','archived (보관)']]);
  const scope=el('fieldset',undefined,'search-scope'), legend=el('legend','검색할 프로젝트');scope.append(legend);
  const boxes=projects.map(p=>{
    const l=el('label',undefined,'check'), c=el('input');c.type='checkbox';c.value=p.id;c.checked=true;
    l.append(c,document.createTextNode(` ${p.name} (${p.slug})`));scope.append(l);return c;
  });
  const toggles=el('div',undefined,'actions');
  toggles.append(btn('모두 선택',()=>boxes.forEach(b=>b.checked=true)),btn('모두 해제',()=>boxes.forEach(b=>b.checked=false)));
  scope.append(toggles);form.append(scope);
  const submit=el('button','검색');form.append(submit);
  const summary=el('p','','small muted');summary.setAttribute('role','status');
  controls.append(form,summary);
  const list=el('div');root.append(list);
  let generation=0;
  async function run() {
    const chosen=boxes.filter(b=>b.checked).map(b=>b.value);
    if (!chosen.length) throw Error('검색할 프로젝트를 하나 이상 선택해 주세요.');
    if (chosen.length>50) throw Error('한 번에 최대 50개 프로젝트를 검색할 수 있습니다.');
    const ticket=++generation, input={query:query.value.trim(),status:status.value,limit:30};
    if (mode.value) input.mode=mode.value;
    // Omitting project_ids lets the server search every accessible project.
    if (chosen.length<projects.length) input.project_ids=chosen;
    list.replaceChildren(el('p','검색 중…','muted'));
    const result=await tool('search_across_projects',input);
    if (ticket!==generation) return;
    let text=`${result.projects.length}개 프로젝트에서 ${result.hits.length}건 · ${searchRetrieval[result.retrieval]||result.retrieval}`;
    if (result.unindexed_chunks) text+=` · 임베딩 대기 조각 ${result.unindexed_chunks}개`;
    summary.textContent=text;
    const nodes=[];
    if (result.warning) nodes.push(el('p',result.warning,'small muted'));
    if (!result.hits.length) nodes.push(el('p','일치하는 문서가 없습니다. 결과가 없다고 해서 자료가 없다는 뜻은 아닙니다.','empty'));
    nodes.push(...result.hits.map(hitCard));
    list.replaceChildren(...nodes);
  }
  function hitCard(h) {
    const c=card(h.title);
    c.append(el('p',`${h.project_name||h.project_id} · ${searchKinds[h.kind]||h.kind} · ${h.status} · 버전 ${h.revision} · 점수 ${Number(h.score).toFixed(4)}`,'small muted'));
    c.append(el('p',h.excerpt.length>400?h.excerpt.slice(0,400)+'…':h.excerpt,'small'));
    const body=el('details'), content=el('div');
    body.append(el('summary','본문 보기'),content);
    body.addEventListener('toggle',async()=>{
      if (!body.open||content.childElementCount) return;
      content.append(el('p','불러오는 중…','muted'));
      try {
        const doc=await tool('get_document',{project_id:h.project_id,document_id:h.document_id,revision:h.revision});
        content.replaceChildren(renderMarkdown(doc.content,'vector-content'));
      } catch(e) { content.replaceChildren(el('p',e.message,'muted')); }
    });
    c.append(body,el('code',h.citation));
    return c;
  }
  form.addEventListener('submit',e=>{e.preventDefault();submit.disabled=true;run().catch(err=>notify(err.message,true)).finally(()=>submit.disabled=false);});
  return root;
}
