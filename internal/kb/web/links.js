'use strict';

// Document relations (supersedes · implements · related) in the web console.
// Each group is shown from the viewpoint of the open document; link_documents
// stores the direction, so "superseded_by" and "implemented_by" swap source/target.

const linkGroups=[
  ['supersedes','이 문서가 대체한 이전 문서',true],
  ['superseded_by','이 문서를 대체한 새 문서',false],
  ['implements','이 작업 이력이 수행한 결정·기획',true],
  ['implemented_by','이 문서를 수행한 작업 이력',false],
  ['related','관련 문서',true],
];
const linkDocumentCache=new Map();

async function linkCandidates(projectID) {
  if (!linkDocumentCache.has(projectID)) linkDocumentCache.set(projectID,tool('list_documents',{project_id:projectID,limit:100}).catch(e=>{linkDocumentCache.delete(projectID);throw e;}));
  return linkDocumentCache.get(projectID);
}

function linkRelationOptions(kind) {
  const options=[];
  if (kind!=='history') options.push(['supersedes','이 문서가 대체함 (이 문서 = 새 문서)'],['superseded_by','이 문서를 대체함 (상대 = 새 문서)']);
  if (kind==='history') options.push(['implements','이 작업 이력이 결정·기획을 수행함']);
  if (kind==='decision'||kind==='brief') options.push(['implemented_by','작업 이력이 이 문서를 수행함']);
  options.push(['related','관련 문서']);
  return options;
}

// linkDirection maps a viewpoint relation to link_documents input.
function linkDirection(relation,selfID,otherID) {
  if (relation==='superseded_by') return {relation:'supersedes',source_id:otherID,target_id:selfID};
  if (relation==='implemented_by') return {relation:'implements',source_id:otherID,target_id:selfID};
  return {relation,source_id:selfID,target_id:otherID};
}

function linksPanel(d,canEdit) {
  const root=el('section',undefined,'links-panel');
  root.append(el('h3','문서 관계'));
  const body=el('div');
  root.append(body);
  async function load() {
    const links=await tool('get_document_links',{project_id:d.project_id,document_id:d.id});
    const groups=linkGroups.filter(([key])=>links[key]?.length);
    body.replaceChildren();
    if (!groups.length) body.append(el('p','연결된 문서가 없습니다.','small muted'));
    for (const [key,label] of groups) {
      const group=el('div',undefined,'link-group');
      group.append(el('p',label,'small'));
      for (const other of links[key]) {
        const row=el('div',undefined,'link-row');
        const info=el('div');
        info.append(el('strong',other.title),el('span',` ${documentKinds[other.kind]||other.kind} · ${other.status} · 버전 ${other.revision}`,'small muted'));
        if (other.note) info.append(el('div',other.note,'small'));
        info.append(el('code',other.document_id));
        row.append(info);
        if (canEdit) row.append(btn('연결 해제',async()=>{
          if (!confirm(`"${other.title}"와의 연결을 해제할까요? 문서 자체는 바뀌지 않습니다.`)) return;
          await tool('unlink_documents',{project_id:d.project_id,...linkDirection(key,d.id,other.document_id)});
          notify('연결을 해제했습니다.');await load();
        },'secondary'));
        group.append(row);
      }
      body.append(group);
    }
    if (canEdit) body.append(linkForm());
  }
  function linkForm() {
    const f=el('form',undefined,'link-form');
    const relation=selectField(f,'관계','relation',linkRelationOptions(d.kind));
    const target=field(f,'상대 문서 (제목에서 고르거나 ID 붙여넣기)','target');
    const listID='link-targets-'+d.id;target.setAttribute('list',listID);target.autocomplete='off';
    const datalist=el('datalist');datalist.id=listID;f.append(datalist);
    linkCandidates(d.project_id).then(items=>datalist.replaceChildren(...items.filter(x=>x.id!==d.id).map(x=>{const o=el('option',`${x.title} · ${documentKinds[x.kind]||x.kind} · ${x.status}`);o.value=x.id;return o;}))).catch(()=>{});
    const note=field(f,'메모 (선택, 500자)','note');note.required=false;note.maxLength=500;
    formSubmit(f,'연결',async data=>{
      const other=data.target.trim();
      if (!/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(other)) throw Error('목록에서 문서를 고르거나 문서 ID를 입력하세요.');
      const out=await tool('link_documents',{project_id:d.project_id,...linkDirection(data.relation,d.id,other),...(data.note?.trim()?{note:data.note.trim()}:{})});
      notify(out.created?'연결했습니다.':'이미 연결되어 있습니다.');await load();
    });
    return f;
  }
  load().catch(e=>body.replaceChildren(el('p',e.message,'muted')));
  return root;
}
