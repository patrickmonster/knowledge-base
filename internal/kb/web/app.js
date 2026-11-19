'use strict';
let renderGeneration=0;
let me, csrf, pageName='projects', inviteToken=new URLSearchParams(location.hash.slice(1)).get('invite'), noticeTimer;
const $=s=>document.querySelector(s);
const el=(tag,text,cls)=>{const n=document.createElement(tag);if(text!==undefined)n.textContent=text;if(cls)n.className=cls;return n;};
function notify(text,error=false){clearTimeout(noticeTimer);$('#notice').textContent=text;$('#notice').className=error?'error':'';$('#notice').hidden=false;noticeTimer=setTimeout(()=>$('#notice').hidden=true,6000);}
async function api(path,data){const response=await fetch(path,{method:data===undefined?'GET':'POST',headers:{'Content-Type':'application/json',...(csrf?{'X-CSRF-Token':csrf}:{})},...(data===undefined?{}:{body:JSON.stringify(data)})});let body;try{body=await response.json();}catch{throw Error('요청을 처리하지 못했습니다.');}if(!response.ok){if(response.status===401&&me){me=null;showLogin();}throw Error(body.error?.message||'요청을 처리하지 못했습니다.');}return body.data;}
const tool=(name,data={})=>api('/api/tools/'+name,data);
// Shared project context: chosen once in the sidebar and applied to every project page.
const projectPages=new Set(['rules','prompts','documents','todos','todosDone','tasks','reviews','issues','attachments','vectors','usage','rechecks']);
let projectList=[], currentProjectID=null;
try{currentProjectID=localStorage.getItem('kb.project');}catch{}
function setCurrentProject(id){currentProjectID=id||null;try{id?localStorage.setItem('kb.project',id):localStorage.removeItem('kb.project');}catch{}const s=$('#project-switch');if(s&&id&&[...s.options].some(o=>o.value===id))s.value=id;updateProjectBadge();refreshReviewBadge();refreshTaskBadge();}
// Sidebar badge: drafts waiting for review in the current project.
let reviewBadgeTicket=0;
async function refreshReviewBadge(){const b=$('#review-badge'),ticket=++reviewBadgeTicket,id=currentProjectID;if(!b)return;if(!id||!me){b.hidden=true;return;}try{const r=await tool('count_documents',{project_id:id,status:'draft'});if(ticket!==reviewBadgeTicket)return;b.textContent=r.count>99?'99+':String(r.count);b.hidden=!r.count;b.parentElement.title=r.count?`검토 대기 ${r.count}건`:'';}catch{if(ticket===reviewBadgeTicket)b.hidden=true;}}
setInterval(()=>{if(me&&document.visibilityState==='visible'){refreshReviewBadge();refreshTaskBadge();}},60000);
function updateProjectBadge(){const p=projectList.find(x=>x.id===currentProjectID);$('#project-badge').textContent=projectPages.has(pageName)&&p?'프로젝트 · '+p.name:'팀 워크스페이스';}
async function loadProjects(){const items=[];for(let offset=0;offset<=100000;offset+=50){const batch=await tool('list_projects',{limit:50,offset});items.push(...batch);if(batch.length<50)break;}projectList=items;const s=$('#project-switch');s.replaceChildren(...items.map(p=>new Option(p.name,p.id)));$('#project-context').hidden=!items.length;if(!items.some(p=>p.id===currentProjectID))setCurrentProject(items[0]?.id);else setCurrentProject(currentProjectID);}
// Pages keep using a <select> for the project, but it follows the sidebar choice instead of being shown per page.
function projectSelect(projects){const s=el('select');s.name='project';for(const p of projects)s.append(new Option(p.name,p.id));if(projects.some(p=>p.id===currentProjectID))s.value=currentProjectID;else if(projects.length)setCurrentProject(s.value);return s;}
function btn(text,fn,cls='secondary'){const b=el('button',text,cls);b.type='button';b.addEventListener('click',async()=>{b.disabled=true;try{await fn();}catch(e){notify(e.message,true);}finally{b.disabled=false;}});return b;}
function card(title){const c=el('section',undefined,'card');if(title)c.append(el('h2',title));return c;}
function field(form,label,name,type='text',value=''){const l=el('label',label),i=el('input');i.name=name;i.type=type;i.value=value;i.required=true;l.append(i);form.append(l);return i;}
function selectField(form,label,name,options){const l=el('label',label),s=el('select');s.name=name;for(const [value,title]of options){const o=el('option',title);o.value=value;s.append(o);}l.append(s);form.append(l);return s;}
function formSubmit(form,title,fn){const b=el('button',title);form.append(b);form.addEventListener('submit',async e=>{e.preventDefault();b.disabled=true;try{await fn(Object.fromEntries(new FormData(form)));}catch(err){notify(err.message,true);}finally{b.disabled=false;}});}
function table(headers,rows){const wrap=el('div',undefined,'table-wrap');if(!rows.length){wrap.append(el('p','표시할 항목이 없습니다.','empty'));return wrap;}const t=el('table'),head=el('tr');headers.forEach(h=>head.append(el('th',h)));const thead=el('thead');thead.append(head);t.append(thead);const body=el('tbody');for(const cells of rows){const row=el('tr');for(const value of cells){const td=el('td');td.append(value instanceof Node?value:document.createTextNode(String(value??'—')));row.append(td);}body.append(row);}t.append(body);wrap.append(t);return wrap;}
function secret(title,value,description){$('#secret-title').textContent=title;$('#secret-description').textContent=description;$('#secret-value').value=value;$('#secret-dialog').showModal();}
$('#close-secret').onclick=()=>{$('#secret-dialog').close();$('#secret-value').value='';};
$('#secret-dialog').addEventListener('close',()=>$('#secret-value').value='');
$('#copy-secret').onclick=async()=>{try{await navigator.clipboard.writeText($('#secret-value').value);notify('복사했습니다.');}catch{$('#secret-value').select();notify('선택된 내용을 복사해 주세요.');}};
function showLogin(){++renderGeneration;$('#app-view').hidden=true;$('#invite-view').hidden=true;$('#setup-view').hidden=true;$('#login-view').hidden=false;$('#mfa-form').hidden=true;$('#login-form').hidden=false;csrf=null;}
async function loadMe(){const route=location.hash.slice(1);if(Object.hasOwn(titles,route))pageName=route;const d=await api('/api/me');me=d.user;csrf=d.csrf_token;me.mcpURL=d.mcp_url;$('#login-view').hidden=true;$('#invite-view').hidden=true;$('#setup-view').hidden=true;$('#app-view').hidden=false;$('#identity-name').textContent=me.name;$('#identity-email').textContent=me.email;document.querySelectorAll('.admin-only').forEach(n=>n.hidden=me.global_role!=='admin');try{await loadProjects();}catch(e){notify(e.message,true);}await render(pageName);}
$('#login-form').addEventListener('submit',async e=>{e.preventDefault();const b=e.target.querySelector('button');b.disabled=true;try{const r=await api('/api/login',Object.fromEntries(new FormData(e.target)));e.target.reset();if(r.mfa_required){showMFAStep();return;}await loadMe();}catch(err){notify(err.message,true);}finally{b.disabled=false;}});
$('#accept-form').addEventListener('submit',async e=>{e.preventDefault();const d=Object.fromEntries(new FormData(e.target));if(d.password!==d.confirmation){notify('비밀번호가 일치하지 않습니다.',true);return;}const b=e.target.querySelector('button');b.disabled=true;try{await api('/api/accept-invitation',{token:inviteToken,password:d.password});inviteToken=null;e.target.reset();showLogin();notify('설정되었습니다. 새 비밀번호로 로그인하세요.');}catch(err){notify(err.message,true);}finally{b.disabled=false;}});
// First-run setup: shown instead of sign-in while the server has no administrator.
async function showLoginOrSetup(){showLogin();try{const d=await api('/api/setup');if(d.required&&!me){$('#login-view').hidden=true;$('#setup-view').hidden=false;}}catch{}}
$('#setup-form').addEventListener('submit',async e=>{e.preventDefault();const d=Object.fromEntries(new FormData(e.target));if(d.password!==d.confirmation){notify('비밀번호가 일치하지 않습니다.',true);return;}const b=e.target.querySelector('button');b.disabled=true;try{await api('/api/setup',{name:d.name,email:d.email,password:d.password});e.target.reset();showLogin();try{await api('/api/login',{email:d.email,password:d.password});await loadMe();notify('관리자 계정을 만들었습니다.');}catch{notify('관리자 계정을 만들었습니다. 로그인하세요.');}}catch(err){notify(err.message,true);}finally{b.disabled=false;}});
$('#logout').onclick=async()=>{try{await api('/api/logout',{});me=null;showLogin();}catch(e){notify(e.message,true);}};
document.querySelectorAll('[data-page]').forEach(b=>b.addEventListener('click',()=>render(b.dataset.page).catch(e=>notify(e.message,true))));
$('#project-switch').addEventListener('change',e=>{setCurrentProject(e.target.value);if(projectPages.has(pageName)||pageName==='projects')render(pageName).catch(err=>notify(err.message,true));});
const titles={rules:'작업 규칙',prompts:'작업 프롬프트',search:'지식 검색',documents:'지식 문서',todos:'할 일 보드',todosDone:'완료된 할 일',tasks:'진행 중 작업',reviews:'draft 검토 대기',issues:'이슈 · 질의',attachments:'첨부 문서',vectors:'벡터 탐색',usage:'사용 통계',rechecks:'재확인 · 맥락 피드백',projects:'프로젝트와 권한',keys:'MCP 접속 키',account:'내 계정',overview:'운영 현황',users:'사용자 관리',audit:'접근 기록'};
// One-shot target for the next page: another page asks it to show a record
// (a task's history document, its questions). The page takes it once on open.
let pageFocus=null;
function openPage(name,focus){if(focus?.project_id&&projectList.some(p=>p.id===focus.project_id))setCurrentProject(focus.project_id);pageFocus={...focus,page:name};return render(name);}
function takePageFocus(name){const f=pageFocus;pageFocus=null;return f?.page===name?f:null;}
async function render(name){const ticket=++renderGeneration;pageName=name;$('#page-title').textContent=titles[name];updateProjectBadge();document.querySelectorAll('[data-page]').forEach(b=>b.classList.toggle('active',b.dataset.page===name));const page=$('#page');page.replaceChildren(el('p','불러오는 중…','muted'));try{const content=await({rules:rulesPage,prompts:promptsPage,search:searchPage,documents:documentsPage,todos:todosPage,todosDone:todosDonePage,tasks:tasksPage,reviews:reviewsPage,issues:issuesPage,attachments:attachmentsPage,vectors:vectorPage,usage:usagePage,rechecks:rechecksPage,projects,keys,account,overview,users,audit}[name])();if(pageName===name&&ticket===renderGeneration&&me)page.replaceChildren(content);}catch(e){if(pageName===name&&ticket===renderGeneration&&me)page.replaceChildren(el('p',e.message,'muted'));throw e;}}
async function overview(){const d=await api('/api/admin/stats'),root=el('div'),grid=el('div',undefined,'grid');root.append(el('p','최근 24시간의 작업 기록입니다. 새로고침 시 최신 상태를 조회합니다.','muted'),btn('새로고침',()=>render('overview'),'secondary refresh'));for(const [key,label]of [['active_users','활성 사용자'],['projects','전체 프로젝트'],['active_keys','유효한 MCP 키'],['requests_24h','기록된 작업 · 24시간'],['denied_24h','거부된 작업 · 24시간'],['errors_24h','처리 오류 · 24시간'],['login_failures_24h','로그인 실패 · 24시간'],['average_duration_ms','평균 처리 시간 · ms']]){const c=card();c.classList.add('metric');c.append(el('p',label,'muted'),el('div',d[key],'value'));grid.append(c);}root.append(grid);try{const draw=async()=>{const old=root.querySelector('.retention-card');const n=retentionCard(await api('/api/admin/retention'),draw);n.classList.add('retention-card');old?old.replaceWith(n):root.append(n);};await draw();}catch(e){root.append(el('p','보관 정책을 불러오지 못했습니다: '+e.message,'muted'));}try{const draw=async()=>{const old=root.querySelector('.search-settings-card');const n=searchSettingsCard(await api('/api/admin/search-settings'),draw);n.classList.add('search-settings-card');old?old.replaceWith(n):root.append(n);};await draw();}catch(e){root.append(el('p','검색 설정을 불러오지 못했습니다: '+e.message,'muted'));}try{root.append(await discordAdminCard());}catch(e){root.append(el('p','Discord 설정을 불러오지 못했습니다: '+e.message,'muted'));}return root;}
function retentionCard(r,reload){const c=card('보관 정책'),p=r.policy||{},src=r.sources||{},def=r.defaults||{},days=n=>n?n+'일':'무기한 보관',when=v=>v?new Date(v).toLocaleString():'없음',d=r.data||{},last=r.last_purge,from=k=>src[k]==='admin'?' · 관리자 설정':' · 서버 기본값';
c.append(table(['항목','설정','현재 보관 건수','가장 오래된 기록'],[['감사 로그',days(p.audit_days)+from('audit_days'),String(d.audit_events??'-'),when(d.oldest_audit_event)],['문서 이전 버전',days(p.revision_days)+from('revision_days')+(p.revision_days?' · 문서별 최신 '+p.revision_keep_latest+'개 유지'+from('revision_keep_latest'):''),String(d.revisions??'-'),when(d.oldest_revision)]]));
c.append(el('p',last?'마지막 정리: '+when(last.at)+' · 감사 로그 '+last.details.audit_events+'건, 버전 '+last.details.revisions+'건 삭제':'아직 정리 실행 기록이 없습니다.','muted'));
const form=el('form',undefined,'retention-form'),mk=(label,name,hint,value,min,max)=>{const l=el('label',label),i=el('input');i.name=name;i.type='number';i.min=0;i.max=max;i.step=1;i.placeholder='서버 기본값'+(def[name]!==undefined?' ('+(def[name]||'무기한')+')':'');i.value=src[name]==='admin'?String(p[name]):'';l.append(i,el('small',hint,'muted'));form.append(l);return i;};
mk('감사 로그 보관 일수','audit_days','0 = 무기한, 또는 90~3650일. 비우면 서버 기본값.',0,90,3650);
mk('문서 이전 버전 보관 일수','revision_days','0 = 무기한, 또는 30~3650일. 비우면 서버 기본값.',0,30,3650);
mk('문서별 최신 버전 유지 개수','revision_keep_latest','1~1000. 기간이 지나도 이 개수만큼은 남깁니다. 비우면 서버 기본값.',0,1,1000);
const save=el('button','보관 정책 저장');form.append(save);
form.addEventListener('submit',async e=>{e.preventDefault();const v=n=>{const t=form.elements[n].value.trim();return t===''?null:Number(t);},body={audit_days:v('audit_days'),revision_days:v('revision_days'),revision_keep_latest:v('revision_keep_latest')};
const shorter=(k,cur)=>body[k]!==null&&body[k]>0&&(!cur||body[k]<cur);
if((shorter('audit_days',p.audit_days)||shorter('revision_days',p.revision_days))&&!confirm('기간을 정하거나 줄이면 그보다 오래된 기록이 1시간 안에 삭제되며 되돌릴 수 없습니다. 볼륨 백업을 확인했나요? 저장할까요?'))return;
save.disabled=true;try{await api('/api/admin/retention',body);notify('보관 정책을 저장했습니다.');await reload();}catch(err){notify(err.message,true);}finally{save.disabled=false;}});
c.append(form,el('p','현재 버전과 첨부가 연결된 버전은 삭제하지 않습니다. 결과 0건 검색어·검색 시간 표본은 최대 365일(감사 로그 기간이 더 짧으면 그 기간) 뒤 삭제합니다. 환경변수(AUDIT_RETENTION_DAYS, REVISION_RETENTION_DAYS, REVISION_KEEP_LATEST)는 이 화면에서 값을 비운 항목의 기본값으로만 쓰입니다.','muted'));return c;}
// Search settings a global administrator can change (search_settings). The
// floor hides weak question matches; it deletes nothing, so it can be undone.
function searchSettingsCard(r,reload){const c=card('검색 설정'),p=r.policy||{},src=r.sources||{},def=r.defaults||{},lim=r.limits||{};
const floor=v=>v?Number(v).toFixed(2):'하한 없음',from=k=>src[k]==='admin'?' · 관리자 설정':' · 서버 기본값';
c.append(table(['항목','설정','적용 범위'],[['질의·이슈 결과 점수 하한',floor(p.issue_score_floor)+from('issue_score_floor'),'지식 검색의 “관련 질의·이슈”(의미·하이브리드 검색). 키워드 검색 결과는 거르지 않습니다.']]));
if(r.updated_at)c.append(el('p','마지막 변경: '+new Date(r.updated_at).toLocaleString(),'muted'));
const form=el('form',undefined,'search-settings-form'),l=el('label','질의·이슈 점수 하한'),i=el('input');
i.name='issue_score_floor';i.type='number';i.min=0;i.max=lim.issue_score_floor_max??0.95;i.step=0.01;
i.placeholder='서버 기본값'+(def.issue_score_floor!==undefined?' ('+floor(def.issue_score_floor)+')':'');
i.value=src.issue_score_floor==='admin'?String(p.issue_score_floor):'';
l.append(i,el('small','0 = 하한 없음, 또는 '+(lim.issue_score_floor_min??0.1)+'~'+(lim.issue_score_floor_max??0.95)+'. 비우면 서버 기본값.','muted'));
form.append(l);
const save=el('button','검색 설정 저장');form.append(save);
form.addEventListener('submit',async e=>{e.preventDefault();const t=i.value.trim(),body={issue_score_floor:t===''?null:Number(t)};
save.disabled=true;try{await api('/api/admin/search-settings',body);notify('검색 설정을 저장했습니다.');await reload();}catch(err){notify(err.message,true);}finally{save.disabled=false;}});
c.append(form,el('p','벡터 유사도(코사인)가 하한에 못 미치는 질의는 결과에서 빠지고, 빠진 건수는 검색 응답의 filtered_issues로 알려 줍니다. 문서 결과와 키워드 검색은 그대로입니다. 환경변수 ISSUE_SCORE_FLOOR는 이 화면에서 값을 비웠을 때의 기본값으로만 쓰입니다.','muted'));return c;}

// Lists the caller's projects as a table; 프로젝트 만들기 opens the create form in a modal.
async function projects(){
  const items=await tool('list_projects',{limit:50}),root=el('div'),list=el('section',undefined,'card');
  const head=el('div',undefined,'projects-head'),create=el('button','프로젝트 만들기');create.type='button';
  create.addEventListener('click',()=>openProjectDialog(root));
  head.append(el('h2','참여 중인 프로젝트'),create);list.append(head);
  if(!items.length)list.append(el('p','프로젝트를 만들거나 소유자에게 참여를 요청하세요.','empty'));
  else{
    const wrap=table(['프로젝트','저장소','역할','관리'],items.map(p=>{
      const info=el('div',undefined,'project-name'),title=el('div',undefined,'project-title');
      title.append(el('strong',p.name));
      if(p.id===currentProjectID)title.append(el('span','현재 프로젝트','pill project-current'));
      info.append(title);
      if(p.description)info.append(el('p',p.description,'small muted'));
      if(p.slug){const slug=el('div',undefined,'project-slug small muted');slug.append('주소용 이름 ',el('code',p.slug));info.append(slug);}
      info.append(el('code',p.id));
      const acts=el('div',undefined,'actions project-actions');
      if(p.id!==currentProjectID)acts.append(btn('이 프로젝트로 작업',async()=>{setCurrentProject(p.id);await render('documents');},''));
      acts.append(btn('저장소',()=>repositories(p)),btn('상위 프로젝트',()=>upstreams(p)),btn('참여자',()=>members(p)),btn('백업 · 이전 · AGENTS.md',()=>portability(p)));
      return [info,repositoryPills(p.repositories),roleLabels[p.role]||p.role,acts];
    }));
    wrap.classList.add('project-table');
    wrap.querySelectorAll('tbody tr').forEach((tr,i)=>{if(items[i].id===currentProjectID)tr.classList.add('current');});
    list.append(wrap);
  }
  root.append(list);return root;
}

// The create-project form in a modal; it closes on success, Esc, 취소 or a backdrop click.
function openProjectDialog(parent){
  const dialog=el('dialog',undefined,'project-dialog'),form=el('form');
  field(form,'프로젝트 이름','name');
  const slug=field(form,'주소용 이름 (영문 소문자·숫자·하이픈)','slug');slug.pattern='[a-z0-9][a-z0-9-]{0,79}';
  field(form,'설명','description').required=false;
  const rl=el('label','소유자 규칙 (선택, 한 줄에 하나 · "커밋:" 으로 시작하면 커밋 규칙)'),rt=el('textarea');rt.name='rules';rt.rows=4;rt.placeholder='커밋: 커밋 메시지는 feat:/fix: 접두사를 쓴다\n작업: 작업 전 start_task로 맥락을 조회한다';rl.append(rt);form.append(rl);
  const gl=el('label','소스 저장소 (선택, 한 줄에 하나 · git remote 또는 owner/name)'),gt=el('textarea');gt.name='repositories';gt.rows=2;gt.placeholder='https://github.com/owner/name.git\nowner/another-repo';gl.append(gt);form.append(gl);
  formSubmit(form,'프로젝트 만들기',async d=>{
    const rules=parseRuleLines(d.rules||''),repos=parseRepositoryLines(d.repositories||'');
    delete d.rules;delete d.repositories;if(rules.length)d.rules=rules;
    const created=await tool('create_project',d);
    dialog.close();
    const failed=await addRepositories(created.id,repos);
    notify(failed.length?'프로젝트를 만들었지만 저장소 '+failed.length+'개를 등록하지 못했습니다: '+failed.join(' · '):'프로젝트를 만들었습니다.',failed.length>0);
    await loadProjects();await render('projects');
  });
  const actions=el('div',undefined,'actions project-dialog-actions');actions.append(btn('취소',()=>dialog.close()),form.querySelector('button'));form.append(actions);
  dialog.append(el('h2','프로젝트 만들기'),form);
  dialog.addEventListener('close',()=>dialog.remove(),{once:true});
  dialog.addEventListener('click',e=>{if(e.target!==dialog)return;const r=dialog.getBoundingClientRect();if(e.clientX<r.left||e.clientX>r.right||e.clientY<r.top||e.clientY>r.bottom)dialog.close();});
  parent.append(dialog);dialog.showModal();form.querySelector('input[name="name"]')?.focus();
}
// Upstream projects: a downstream follows an upstream (design, planning, specs) read-only; the upstream's owner or admin approves here.
const upstreamStatus={pending:'승인 대기',approved:'연결됨'};
// Picks an upstream from the caller's own projects in a table (filtered by name or slug); a project the caller is not a member of is still requested by its ID.
function upstreamPicker(project,data){
  const box=el('div',undefined,'upstream-picker'),linked=new Set([project.id,...data.upstreams.map(l=>l.upstream_project_id),...data.downstreams.map(l=>l.project_id)]);
  const request=async id=>{const l=await tool('request_project_upstream',{project_id:project.id,upstream_project_id:id});notify(l.status==='approved'?'연결했습니다.':'요청했습니다. 상위 프로젝트 소유자·관리자가 승인하면 연결됩니다.');await upstreams(project);};
  const candidates=projectList.filter(p=>!linked.has(p.id));
  box.append(el('h2','상위 프로젝트 연결 요청'),el('p','참여 중인 프로젝트에서 상위 프로젝트를 고르세요. 이미 연결됐거나 이 프로젝트를 따르는 하위 프로젝트는 목록에서 뺐습니다. 내가 소유자·관리자인 프로젝트는 바로 연결되고, 그 밖에는 상위 프로젝트 소유자·관리자가 승인해야 연결됩니다.','small muted'));
  const search=el('input');search.type='search';search.placeholder='이름 또는 주소용 이름으로 찾기';search.setAttribute('aria-label','상위 프로젝트 찾기');
  const list=el('div');
  const draw=()=>{const q=search.value.trim().toLowerCase(),rows=candidates.filter(p=>!q||p.name.toLowerCase().includes(q)||(p.slug||'').toLowerCase().includes(q));
    const wrap=table(['프로젝트','주소용 이름','내 역할',''],rows.map(p=>{const info=el('div',undefined,'project-name');info.append(el('strong',p.name));if(p.description)info.append(el('p',p.description,'small muted'));
      const b=btn('연결 요청',async()=>{if(!confirm(p.name+' 프로젝트를 '+project.name+'의 상위 프로젝트로 연결 요청할까요?'))return;b.disabled=true;try{await request(p.id);}catch(err){notify(err.message,true);b.disabled=false;}});
      return [info,p.slug?el('code',p.slug):'—',roleLabels[p.role]||p.role,b];}));
    wrap.classList.add('upstream-candidates');const empty=wrap.querySelector('.empty');if(empty)empty.textContent=candidates.length?'찾는 프로젝트가 없습니다.':'연결할 수 있는 다른 참여 프로젝트가 없습니다. 아래에서 프로젝트 ID로 요청하세요.';list.replaceChildren(wrap);};
  search.addEventListener('input',draw);
  if(candidates.length>5)box.append(search);
  box.append(list);draw();
  const more=el('details',undefined,'upstream-by-id');more.append(el('summary','목록에 없는 프로젝트 ID로 요청'));
  const f=el('form');const id=field(f,'상위 프로젝트 ID (상위 프로젝트 소유자에게 받은 ID)','upstream_project_id');id.pattern='[0-9a-fA-F-]{36}';
  formSubmit(f,'연결 요청',d=>request(d.upstream_project_id.trim()));more.append(f);box.append(more);
  return box;
}
async function upstreams(project){const data=await tool('list_project_upstreams',{project_id:project.id}),canManage=project.role==='owner'||project.role==='admin',root=el('div');root.append(btn('프로젝트 목록',()=>render('projects'),'secondary refresh'));
const up=card(project.name+' · 따르는 상위 프로젝트');up.append(el('p','상위 프로젝트(디자인 시안·기획·공통 명세 등)는 이 프로젝트의 기준 자료입니다. 연결되면 이 프로젝트 참여자는 상위 프로젝트에 참여하지 않아도 문서를 읽을 수 있고, start_task가 상위의 active 자료를 upstream_sources로 함께 돌려줍니다. 상위 문서는 이 프로젝트에서 고칠 수 없으며 문제는 상위 프로젝트에 이슈(create_issue의 from_project_id)로 올립니다.','small muted'));
up.append(table(canManage?['상위 프로젝트','상태','요청','']:['상위 프로젝트','상태','요청'],data.upstreams.map(l=>{const cells=[el('span',l.upstream_project_name+' ('+l.upstream_project_slug+')'),el('span',upstreamStatus[l.status]||l.status,'pill'),el('span',(l.requested_by_name||'-')+' · '+new Date(l.requested_at).toLocaleString(),'small')];if(canManage)cells.push(btn(l.status==='pending'?'요청 취소':'연결 해제',async()=>{if(!confirm(l.upstream_project_name+' 연결을 해제할까요? 이 프로젝트 참여자가 더 이상 상위 문서를 읽지 못합니다.'))return;await tool('remove_project_upstream',{project_id:project.id,other_project_id:l.upstream_project_id});notify('연결을 해제했습니다.');await upstreams(project);},'danger'));return cells;})));
if(canManage)up.append(upstreamPicker(project,data));else up.append(el('p','연결 요청·해제는 프로젝트 소유자·관리자만 할 수 있습니다.','small muted'));
const down=card(project.name+' · 이 프로젝트를 따르는 하위 프로젝트');down.append(el('p','승인하면 하위 프로젝트 참여자가 이 프로젝트를 읽기 전용으로 참조하고 이슈를 올릴 수 있습니다. 하위 프로젝트에서 올린 이슈는 이슈 화면에 하위 프로젝트 이름이 표시됩니다.','small muted'));
down.append(table(canManage?['하위 프로젝트','상태','요청','']:['하위 프로젝트','상태','요청'],data.downstreams.map(l=>{const cells=[el('span',l.project_name+' ('+l.project_slug+')'),el('span',upstreamStatus[l.status]||l.status,'pill'),el('span',(l.requested_by_name||'-')+' · '+new Date(l.requested_at).toLocaleString(),'small')];if(canManage){const acts=el('div',undefined,'actions');if(l.status==='pending')acts.append(btn('승인',async()=>{await tool('approve_project_upstream',{project_id:project.id,downstream_project_id:l.project_id});notify('승인했습니다.');await upstreams(project);}));acts.append(btn(l.status==='pending'?'거절':'연결 해제',async()=>{if(!confirm(l.project_name+' 연결을 '+(l.status==='pending'?'거절':'해제')+'할까요?'))return;await tool('remove_project_upstream',{project_id:project.id,other_project_id:l.project_id});notify('처리했습니다.');await upstreams(project);},'danger'));cells.push(acts);}return cells;})));
down.append(el('p','이 프로젝트 ID: '+project.id+' — 하위 프로젝트 소유자가 연결을 요청할 때 필요합니다.','small muted'));
root.append(up,down);$('#page').replaceChildren(root);}
// Source repositories: agents pass their checkout's git remote to resolve_project/list_projects to find the project.
function parseRepositoryLines(text){return [...new Set(text.split(/[\n,]/).map(s=>s.trim()).filter(Boolean))];}
async function addRepositories(projectID,repos){const failed=[];for(const r of repos){try{await tool('add_project_repository',{project_id:projectID,repository:r});}catch(e){failed.push(r+' ('+e.message+')');}}return failed;}
function repositoryPills(repos){const box=el('div',undefined,'repo-pills');if(!repos?.length){box.append(el('span','등록된 저장소 없음','small muted'));return box;}for(const r of repos)box.append(el('span',r,'pill'));return box;}
async function repositories(project){await loadProjects();const repos=(projectList.find(p=>p.id===project.id)||project).repositories||[],canManage=project.role==='owner'||project.role==='admin',root=el('div'),c=card(project.name+' · 소스 저장소');root.append(btn('프로젝트 목록',()=>render('projects'),'secondary refresh'),c);c.append(el('p','AI 에이전트는 작업 중인 저장소의 git remote로 이 프로젝트를 찾습니다(resolve_project). 같은 프로젝트에 저장소를 여러 개(최대 20개) 연결할 수 있고, 주소는 host/owner/name 형태로 정리되어 저장됩니다.','small muted'));const rows=repos.map(r=>{const host=r.split('/')[0],link=el('a',r);link.href='https://'+r;link.target='_blank';link.rel='noopener noreferrer';const cells=[host==='github.com'||host==='gitlab.com'||host==='bitbucket.org'?link:el('code',r)];if(canManage)cells.push(btn('삭제',async()=>{if(!confirm(r+' 저장소 연결을 해제할까요? 이 저장소에서 작업하는 AI가 더 이상 이 프로젝트를 찾지 못합니다.'))return;await tool('remove_project_repository',{project_id:project.id,repository:r});notify('저장소 연결을 해제했습니다.');await repositories(project);},'danger'));return cells;});c.append(table(canManage?['저장소','']:['저장소'],rows));if(canManage){const f=el('form');f.append(el('h2','저장소 추가'));const l=el('label','저장소 주소 (한 줄에 하나 · git remote get-url origin 결과 또는 owner/name)'),t=el('textarea');t.name='repositories';t.rows=3;t.required=true;t.placeholder='https://github.com/owner/name.git\ngit@github.com:owner/other.git\nowner/name';l.append(t);f.append(l);formSubmit(f,'저장소 추가',async d=>{const list=parseRepositoryLines(d.repositories||'');if(!list.length)return;const failed=await addRepositories(project.id,list);if(failed.length)notify('등록하지 못한 저장소: '+failed.join(' · '),true);else notify('저장소를 등록했습니다.');await repositories(project);});c.append(f);}else c.append(el('p','저장소 추가·삭제는 프로젝트 소유자·관리자만 할 수 있습니다.','small muted'));$('#page').replaceChildren(root);}
const roleLabels={owner:'소유자',admin:'관리자',editor:'편집자',viewer:'조회자'};
async function members(project){const data=await tool('list_project_members',{project_id:project.id}),root=el('div'),c=card(project.name+' · 참여자');root.append(btn('프로젝트 목록',()=>render('projects'),'secondary refresh'),c);c.append(table(['이름','사용자 ID','역할'],data.map(u=>[u.name,el('code',u.user_id),roleLabels[u.role]||u.role])));if(project.role==='owner'){const f=el('form');f.append(el('h2','권한 설정'));field(f,'사용자 ID (사용자의 내 계정 화면에서 확인)','user_id');selectField(f,'역할','role',[['editor','편집자 — 조회·추가·수정'],['admin','관리자 — 편집 + 소유자 규칙 관리'],['viewer','조회자 — 조회만'],['remove','프로젝트에서 제외']]);formSubmit(f,'권한 적용',async d=>{if(d.role==='remove'&&!confirm('이 사용자를 프로젝트에서 제외할까요?'))return;await tool('set_project_member',{...d,project_id:project.id});notify('권한을 적용했습니다.');await members(project);});c.append(f);const candidates=data.filter(u=>u.role!=='owner'&&!u.disabled);if(candidates.length){const t=el('form');t.append(el('h2','소유자 이전'),el('p','새 소유자는 참여자 관리·재색인 권한을 갖고, 나는 아래에서 고른 역할로 남습니다. 되돌리려면 새 소유자가 다시 이전해야 합니다.'));selectField(t,'새 소유자','user_id',candidates.map(u=>[u.user_id,u.name+' ('+(roleLabels[u.role]||u.role)+')']));selectField(t,'이전 후 내 역할','previous_owner_role',[['editor','편집자'],['admin','관리자'],['viewer','조회자']]);field(t,'확인을 위해 주소용 이름 입력: '+project.slug,'confirm_slug');formSubmit(t,'소유자 이전',async d=>{if(d.confirm_slug!==project.slug){notify('주소용 이름이 일치하지 않습니다.',true);return;}const name=candidates.find(u=>u.user_id===d.user_id)?.name||d.user_id;if(!confirm(name+'에게 “'+project.name+'” 소유권을 넘길까요?'))return;await tool('transfer_project_owner',{...d,project_id:project.id});notify('소유자를 이전했습니다.');await loadProjects();await members({...project,role:d.previous_owner_role});});c.append(t);}}$('#page').replaceChildren(root);}
function mcpGuide(url){
  const guide=card('MCP 연결 가이드');
  guide.append(el('p','키를 발급한 뒤 AI 도구에 이 서버를 등록하면 프로젝트의 자료를 조회하거나 저장할 수 있습니다.','muted'));
  const address=el('div',undefined,'mcp-address');
  address.append(el('code',url),btn('연결 주소 복사',async()=>{await navigator.clipboard.writeText(url);notify('연결 주소를 복사했습니다.');}));
  guide.append(address);
  const steps=el('ol',undefined,'mcp-steps');
  for(const text of [
    '아래에서 키 이름, 허용 작업, 접근 범위와 유효 기간을 선택해 키를 발급합니다. 키 원문은 발급 직후 한 번만 표시됩니다.',
    'AI 도구에서 원격 MCP 서버를 추가합니다. 연결 방식은 Streamable HTTP, 주소는 위의 /mcp 주소를 사용합니다.',
    '인증은 Bearer 토큰을 선택해 키 원문을 넣습니다. 헤더를 직접 입력할 때는 Authorization: Bearer <발급받은 키> 형식으로 설정합니다.',
    '서버를 연결한 뒤 “지식베이스의 whoami 도구로 연결을 확인하고, 참여 중인 프로젝트를 보여줘”라고 요청합니다.'
  ])steps.append(el('li',text));
  guide.append(steps,el('p','이 서버는 키 인증을 사용합니다. OAuth 로그인만 지원하는 연결 화면에서는 사용할 수 없습니다.','small muted'));
  const details=el('details',undefined,'mcp-guide-details');
  details.append(el('summary','Codex · Cursor 설정 예시'));
  details.append(el('p','실행 환경의 KNOWLEDGE_BASE_API_KEY 변수에 발급받은 키 원문을 설정하세요. 변수 값에는 Bearer를 붙이지 않습니다. GUI 앱에도 이 변수가 전달되어야 하며, 프로젝트의 .env 파일만 작성해도 자동으로 읽히는 것은 아닙니다.','small'));
  const addExample=(title,description,code,source)=>{
    const section=el('section',undefined,'mcp-example');
    section.append(el('h3',title),el('p',description,'small muted'));
    const pre=el('pre');pre.append(el('code',code));section.append(pre);
    const actions=el('div',undefined,'actions');
    actions.append(btn('설정 예시 복사',async()=>{await navigator.clipboard.writeText(code);notify('설정 예시를 복사했습니다.');}));
    const link=el('a','공식 연결 안내');link.href=source;link.target='_blank';link.rel='noopener noreferrer';actions.append(link);
    section.append(actions);details.append(section);
  };
  addExample('Codex','개인 설정 파일 ~/.codex/config.toml에 추가합니다. 기존 knowledge_base 항목이 있으면 해당 항목을 수정하세요.',
    `[mcp_servers.knowledge_base]\nurl = ${JSON.stringify(url)}\nbearer_token_env_var = "KNOWLEDGE_BASE_API_KEY"`,
    'https://learn.chatgpt.com/docs/extend/mcp');
  addExample('Cursor','개인 설정 파일 ~/.cursor/mcp.json의 mcpServers에 병합합니다. 설정 후 MCP 서버를 다시 연결하세요.',
    JSON.stringify({mcpServers:{knowledge_base:{url,headers:{Authorization:'Bearer ${env:KNOWLEDGE_BASE_API_KEY}'}}}},null,2),
    'https://cursor.com/docs/mcp');
  details.append(el('p','예시에는 실제 키가 포함되지 않습니다. 키를 소스 코드나 공개 저장소에 붙여 넣지 마세요.','small muted'));
  guide.append(details);
  const help=el('details',undefined,'mcp-guide-details');help.append(el('summary','연결이 안 될 때'));
  const tips=el('ul');for(const text of [
    '401 또는 인증 실패: 키가 전달되는지, 만료되거나 폐기된 키인지 확인하세요. 비밀번호 변경·재설정 후에는 키를 새로 발급해야 합니다.',
    '403 또는 권한 부족: 키의 허용 작업·프로젝트 범위와 현재 프로젝트 참여 권한을 확인하세요.',
    '프로젝트가 보이지 않음: 해당 프로젝트의 참여자인지 확인하세요. 개발과 운영의 키·사용자·프로젝트는 서로 분리되어 있습니다.',
    '만료 없음 키도 폐기하거나 계정을 비활성화하면 차단됩니다. 사용하지 않는 키는 아래 목록에서 폐기하세요.'
  ])tips.append(el('li',text));help.append(tips);guide.append(help);
  return guide;
}
async function keys(){
  const [items,ps]=await Promise.all([api('/api/keys'),tool('list_projects',{limit:50})]),root=el('div');
  root.append(mcpGuide(me.mcpURL));
  const f=el('form'),c=card('접속 키 발급');
  field(f,'키 이름','name');
  selectField(f,'허용 작업','access',[['read_only','조회 전용'],['read_write','조회·추가·수정']]);
  selectField(f,'접근 범위','project_id',[['','내가 참여한 모든 프로젝트'],...ps.map(p=>[p.id,p.name])]);
  const lifetime=selectField(f,'유효 기간','lifetime',[['limited','기간 지정'],['permanent','만료 없음 (영구)']]);
  const days=field(f,'사용 일수 (1~90일)','days','number','30');days.min=1;days.max=90;days.step=1;
  const note=el('p','만료 없음 키는 직접 폐기하기 전까지 사용할 수 있습니다. 비밀번호 변경·재설정이나 계정 비활성화 시에는 차단됩니다.','small muted');note.hidden=true;f.append(note);
  lifetime.addEventListener('change',()=>{const permanent=lifetime.value==='permanent';days.disabled=permanent;days.required=!permanent;days.parentElement.hidden=permanent;note.hidden=!permanent;});
  formSubmit(f,'키 발급',async d=>{
    const permanent=d.lifetime==='permanent';
    const result=await api('/api/keys',{name:d.name,access:d.access,project_id:d.project_id,never_expires:permanent,days:permanent?0:Number(d.days)});
    await render('keys');
    secret('MCP 접속 키',result.token,(result.never_expires?'만료 없음 키입니다. ':'')+'키 원문은 지금 한 번만 표시됩니다. 복사해 연결 가이드에 따라 설정하세요.');
  });
  c.append(f);root.append(c);
  const list=card(me.global_role==='admin'?'팀 접속 키 · 최근 200개':'내 접속 키 · 최근 200개');
  list.append(table(['이름 / 사용자','권한','만료','상태',''],items.map(k=>[
    k.name+' / '+k.user_name,k.access==='read_only'?'조회 전용':'조회·추가·수정',
    k.expires_at?new Date(k.expires_at).toLocaleDateString():'만료 없음',
    k.revoked_at?'폐기됨':k.expires_at&&new Date(k.expires_at)<new Date()?'만료':'유효',
    k.revoked_at?'—':btn('폐기',async()=>{if(!confirm('이 키의 접속을 차단할까요?'))return;await api('/api/keys/revoke',{id:k.id});await render('keys');},'danger')
  ])));
  root.append(list);return root;
}
async function account(){const root=el('div'),info=card('내 정보');info.append(el('p',me.name+' · '+me.email),el('p','권한: '+me.global_role,'small muted'),el('code',me.id),btn('사용자 ID 복사',async()=>{await navigator.clipboard.writeText(me.id);notify('복사했습니다.');}));const c=card('비밀번호 변경'),f=el('form');field(f,'현재 비밀번호','current','password').autocomplete='current-password';const next=field(f,'새 비밀번호 (12~128자)','next','password');next.minLength=12;next.maxLength=128;next.autocomplete='new-password';field(f,'새 비밀번호 확인','confirmation','password').autocomplete='new-password';f.append(el('p','변경하면 모든 로그인 세션과 MCP 키가 만료됩니다.','small muted'));formSubmit(f,'비밀번호 변경',async d=>{if(d.next!==d.confirmation)throw Error('비밀번호가 일치하지 않습니다.');await api('/api/password',{current:d.current,next:d.next});me=null;showLogin();notify('변경되었습니다. 다시 로그인하세요.');});c.append(f);root.append(info,await mfaCard(),await discordLinkCard(),c);return root;}
async function users(){const items=await api('/api/admin/users'),root=el('div'),c=card('사용자 초대'),f=el('form');field(f,'이름','name');field(f,'이메일','email','email');formSubmit(f,'초대 링크 만들기',async d=>{const result=await api('/api/admin/invite',d);await render('users');secret('사용자 초대',result.invitation_url,'이 링크를 해당 사용자에게 직접 전달하세요. 메일은 자동 발송하지 않습니다. 링크는 24시간 동안 한 번 사용할 수 있습니다.');});c.append(f);root.append(c);const list=card('팀 사용자 · 최근 200명');list.append(table(['사용자','상태','2단계 인증','역할','관리'],items.map(u=>{const identity=el('div');identity.append(el('strong',u.name),el('div',u.email),el('code',u.id));const role=el('select');for(const v of ['member','admin']){const o=el('option',v);o.value=v;o.selected=u.global_role===v;role.append(o);}role.disabled=u.id===me.id;const actions=el('div',undefined,'actions');if(u.id!==me.id){actions.append(btn('역할 저장',async()=>{if(!confirm('역할을 변경하고 기존 로그인·접속 키를 만료시킬까요?'))return;await api('/api/admin/users',{user_id:u.id,disabled:u.disabled,global_role:role.value});await render('users');}),btn(u.disabled?'활성화':'비활성화',async()=>{if(!confirm('사용자 상태를 변경하고 기존 접속을 만료시킬까요?'))return;await api('/api/admin/users',{user_id:u.id,disabled:!u.disabled,global_role:u.global_role});await render('users');},u.disabled?'secondary':'danger'));}if(u.mfa_enabled&&u.id!==me.id)actions.append(mfaResetButton(u));if(!u.disabled)actions.append(btn(u.activated?'비밀번호 재설정':'초대 재발급',async()=>{const result=await api('/api/admin/invite',{reset_user_id:u.id});secret('비밀번호 설정 링크',result.invitation_url,'해당 사용자에게 직접 전달하세요. 설정 완료 시 기존 로그인과 MCP 키가 만료됩니다.');}));return[identity,u.disabled?'비활성':u.activated?'활성':'초대 대기',u.mfa_enabled?'사용':'—',role,actions];})));root.append(list);return root;}
async function audit(){const root=el('div'),c=card('작업·인증 감사 로그'),f=el('form',undefined,'actions'),rows=el('div');const actor=field(f,'사용자 ID','actor_id');actor.required=false;const project=field(f,'프로젝트 ID','project_id');project.required=false;selectField(f,'결과','outcome',[['','전체'],['success','성공'],['denied','차단'],['error','오류']]);let before=0;const more=btn('이전 기록',()=>load(false));async function load(reset){if(reset)before=0;const d=Object.fromEntries(new FormData(f));const items=await api('/api/admin/audit',{...d,before_id:before});before=items.at(-1)?.id||before;rows.replaceChildren(table(['시간','사용자','작업 / 대상','결과','소요 시간'],items.map(e=>{const target=el('div');target.append(el('strong',e.action),el('div',e.target_id||e.project_id||'—','small muted'));return[new Date(e.created_at).toLocaleString(),e.actor_name||'미인증',target,el('span',e.outcome+(e.error_code?' · '+e.error_code:''),'pill '+e.outcome),e.duration_ms+' ms'];})));more.disabled=items.length<100;}formSubmit(f,'조회',()=>load(true));c.append(el('p','문서 본문, 비밀번호, 키 원문은 기록하지 않습니다. 최대 100개씩 조회합니다.','small muted'),f,rows,more);root.append(c);await load(true);return root;}
if(inviteToken){history.replaceState(null,'',location.pathname);$('#invite-view').hidden=false;}else{loadMe().catch(()=>showLoginOrSetup());}
