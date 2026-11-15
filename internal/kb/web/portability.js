'use strict';

// Project backup · migration (export/import) and AGENTS.md for other repositories.
async function portability(project) {
  const root=el('div');
  root.append(btn('프로젝트 목록',()=>render('projects'),'secondary refresh'));
  const base='/api/projects/'+encodeURIComponent(project.id);

  const exp=card(project.name+' · 내보내기');
  exp.append(el('p','백업을 보조하거나 다른 서버·프로젝트로 옮길 때 사용합니다. 정기 백업(DB 볼륨 백업)을 대신하지 않습니다.','muted'));
  const links=el('div',undefined,'actions');
  for (const [label,query,hint] of [
    ['JSON (버전 기록 포함)','format=json','문서 전체 버전, 관계, 공통 작업 영역, 이슈·질의와 댓글'],
    ['JSON (현재 버전만)','format=json&revisions=false','파일이 작아집니다'],
    ['Markdown (zip)','format=markdown','문서별 .md 파일 + 색인 README + 가져오기용 export.json'],
  ]) {
    const a=el('a',label,'button-link');a.href=base+'/export?'+query;a.setAttribute('download','');a.title=hint;links.append(a);
  }
  exp.append(links,el('p','내보낸 파일에는 프로젝트 문서 원문과 참여자 이름이 들어 있습니다. 안전한 곳에 보관하세요. 첨부 파일 원본은 포함하지 않습니다.','small muted'));
  root.append(exp);

  const imp=card('가져오기');
  if (project.role!=='owner') {
    imp.append(el('p','가져오기는 프로젝트 소유자만 할 수 있습니다.','small muted'));
  } else {
    imp.append(el('p','이 서버에서 내보낸 JSON 또는 Markdown zip을 이 프로젝트에 가져옵니다. 문서와 문서 관계를 새로 만들고, 같은 파일을 다시 가져오면 이미 가져온 문서는 건너뜁니다.','muted'));
    imp.append(el('p','각 문서는 현재 내용으로 첫 버전이 만들어지며 작성자는 가져온 사람으로 기록됩니다. 공통 작업 영역·이슈는 원본 서버 사용자를 참조하므로 가져오지 않습니다. 가져온 문서는 임베딩 대기열에 들어가 검색 준비에 시간이 걸릴 수 있습니다. 최대 32 MiB, 문서 5,000개.','small muted'));
    const f=el('form'), l=el('label','내보낸 파일 (.json 또는 .zip)'), file=el('input');
    file.type='file';file.accept='.json,.zip,application/json,application/zip';file.required=true;l.append(file);f.append(l);
    const out=el('div');
    formSubmit(f,'가져오기',async()=>{
      const picked=file.files[0];
      if (!picked) throw Error('파일을 고르세요.');
      if (picked.size>32*1024*1024) throw Error('32 MiB 이하 파일만 가져올 수 있습니다.');
      const zipped=picked.name.toLowerCase().endsWith('.zip');
      if (!confirm(`"${picked.name}"을 프로젝트 "${project.name}"에 가져올까요?`)) return;
      const response=await fetch(base+'/import',{method:'POST',headers:{'Content-Type':zipped?'application/zip':'application/json','X-CSRF-Token':csrf},body:picked});
      let body;try{body=await response.json();}catch{throw Error('가져오기 결과를 읽지 못했습니다.');}
      if (!response.ok) throw Error(body.error?.message||'가져오지 못했습니다.');
      const r=body.data;
      out.replaceChildren(table(['항목','건수'],[['새로 만든 문서',r.documents_created],['이미 있어 건너뛴 문서',r.documents_skipped],['만든 관계',r.links_created],['건너뛴 관계',r.links_skipped],['가져오지 않은 영역',r.work_areas_ignored],['가져오지 않은 이슈·질의',r.issues_ignored]]),...r.warnings.map(w=>el('p',w,'small muted')));
      notify(`문서 ${r.documents_created}건을 가져왔습니다.`);f.reset();refreshReviewBadge();
    });
    imp.append(f,out);
  }
  root.append(imp);

  const agents=card('AI 작업 지침 (선택, 사용자 수준)');
  agents.append(el('p','AI 도구는 서버 접속 시 받는 지침만으로 저장소 git remote로 이 프로젝트를 찾아 작업합니다. 저장소에는 아무것도 둘 필요가 없고, 이 파일은 지침을 직접 두고 싶을 때만 사용자 수준 설정에 넣습니다. 저장소에 커밋하지 마세요.','muted'));
  root.append(agents);
  try {
    const t=await tool('get_agents_template',{project_id:project.id});
    const notes=el('ul');for(const n of t.notes)notes.append(el('li',n,'small'));
    const pre=el('pre',undefined,'agents-preview');pre.append(el('code',t.content));
    const actions=el('div',undefined,'actions');
    const download=el('a','AGENTS.md 내려받기','button-link');download.href=base+'/agents.md';download.setAttribute('download','AGENTS.md');
    actions.append(btn('내용 복사',async()=>{await navigator.clipboard.writeText(t.content);notify('AGENTS.md 내용을 복사했습니다.');}),download);
    agents.append(notes,actions,pre);
  } catch(e) { agents.append(el('p',e.message,'muted')); }
  $('#page').replaceChildren(root);
}
