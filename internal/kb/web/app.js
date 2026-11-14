'use strict';
let renderGeneration=0;
let me, csrf, pageName='projects', inviteToken=new URLSearchParams(location.hash.slice(1)).get('invite'), noticeTimer;
const $=s=>document.querySelector(s);
const el=(tag,text,cls)=>{const n=document.createElement(tag);if(text!==undefined)n.textContent=text;if(cls)n.className=cls;return n;};
function notify(text,error=false){clearTimeout(noticeTimer);$('#notice').textContent=text;$('#notice').className=error?'error':'';$('#notice').hidden=false;noticeTimer=setTimeout(()=>$('#notice').hidden=true,6000);}
async function api(path,data){const response=await fetch(path,{method:data===undefined?'GET':'POST',headers:{'Content-Type':'application/json',...(csrf?{'X-CSRF-Token':csrf}:{})},...(data===undefined?{}:{body:JSON.stringify(data)})});let body;try{body=await response.json();}catch{throw Error('요청을 처리하지 못했습니다.');}if(!response.ok){if(response.status===401&&me){me=null;showLogin();}throw Error(body.error?.message||'요청을 처리하지 못했습니다.');}return body.data;}
const tool=(name,data={})=>api('/api/tools/'+name,data);
// Shared project context: chosen once in the sidebar and applied to every project page.
const projectPages=new Set(['documents','reviews','issues','attachments','vectors']);
let projectList=[], currentProjectID=null;
try{currentProjectID=localStorage.getItem('kb.project');}catch{}
function setCurrentProject(id){currentProjectID=id||null;try{id?localStorage.setItem('kb.project',id):localStorage.removeItem('kb.project');}catch{}const s=$('#project-switch');if(s&&id&&[...s.options].some(o=>o.value===id))s.value=id;updateProjectBadge();}
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
function showLogin(){++renderGeneration;$('#app-view').hidden=true;$('#invite-view').hidden=true;$('#login-view').hidden=false;csrf=null;}
async function loadMe(){const d=await api('/api/me');me=d.user;csrf=d.csrf_token;me.mcpURL=d.mcp_url;$('#login-view').hidden=true;$('#invite-view').hidden=true;$('#app-view').hidden=false;$('#identity-name').textContent=me.name;$('#identity-email').textContent=me.email;document.querySelectorAll('.admin-only').forEach(n=>n.hidden=me.global_role!=='admin');try{await loadProjects();}catch(e){notify(e.message,true);}await render(pageName);}
$('#login-form').addEventListener('submit',async e=>{e.preventDefault();const b=e.target.querySelector('button');b.disabled=true;try{await api('/api/login',Object.fromEntries(new FormData(e.target)));e.target.reset();await loadMe();}catch(err){notify(err.message,true);}finally{b.disabled=false;}});
$('#accept-form').addEventListener('submit',async e=>{e.preventDefault();const d=Object.fromEntries(new FormData(e.target));if(d.password!==d.confirmation){notify('비밀번호가 일치하지 않습니다.',true);return;}const b=e.target.querySelector('button');b.disabled=true;try{await api('/api/accept-invitation',{token:inviteToken,password:d.password});inviteToken=null;e.target.reset();showLogin();notify('설정되었습니다. 새 비밀번호로 로그인하세요.');}catch(err){notify(err.message,true);}finally{b.disabled=false;}});
$('#logout').onclick=async()=>{try{await api('/api/logout',{});me=null;showLogin();}catch(e){notify(e.message,true);}};
document.querySelectorAll('[data-page]').forEach(b=>b.addEventListener('click',()=>render(b.dataset.page).catch(e=>notify(e.message,true))));
$('#project-switch').addEventListener('change',e=>{setCurrentProject(e.target.value);if(projectPages.has(pageName)||pageName==='projects')render(pageName).catch(err=>notify(err.message,true));});
const titles={search:'지식 검색',documents:'지식 문서',reviews:'draft 검토 대기',issues:'이슈 · 질의',attachments:'첨부 문서',vectors:'벡터 탐색',projects:'프로젝트와 권한',keys:'MCP 접속 키',account:'내 계정',overview:'운영 현황',users:'사용자 관리',audit:'접근 기록'};
async function render(name){const ticket=++renderGeneration;pageName=name;$('#page-title').textContent=titles[name];updateProjectBadge();document.querySelectorAll('[data-page]').forEach(b=>b.classList.toggle('active',b.dataset.page===name));const page=$('#page');page.replaceChildren(el('p','불러오는 중…','muted'));try{const content=await({search:searchPage,documents:documentsPage,reviews:reviewsPage,issues:issuesPage,attachments:attachmentsPage,vectors:vectorPage,projects,keys,account,overview,users,audit}[name])();if(pageName===name&&ticket===renderGeneration&&me)page.replaceChildren(content);}catch(e){if(pageName===name&&ticket===renderGeneration&&me)page.replaceChildren(el('p',e.message,'muted'));throw e;}}
async function overview(){const d=await api('/api/admin/stats'),root=el('div'),grid=el('div',undefined,'grid');root.append(el('p','최근 24시간의 작업 기록입니다. 새로고침 시 최신 상태를 조회합니다.','muted'),btn('새로고침',()=>render('overview'),'secondary refresh'));for(const [key,label]of [['active_users','활성 사용자'],['projects','전체 프로젝트'],['active_keys','유효한 MCP 키'],['requests_24h','기록된 작업 · 24시간'],['denied_24h','거부된 작업 · 24시간'],['errors_24h','처리 오류 · 24시간'],['login_failures_24h','로그인 실패 · 24시간'],['average_duration_ms','평균 처리 시간 · ms']]){const c=card();c.classList.add('metric');c.append(el('p',label,'muted'),el('div',d[key],'value'));grid.append(c);}root.append(grid);return root;}
async function projects(){const items=await tool('list_projects',{limit:50}),root=el('div',undefined,'split'),create=card('프로젝트 만들기'),form=el('form');field(form,'프로젝트 이름','name');const slug=field(form,'주소용 이름 (영문 소문자·숫자·하이픈)','slug');slug.pattern='[a-z0-9][a-z0-9-]{0,79}';field(form,'설명','description').required=false;formSubmit(form,'프로젝트 만들기',async d=>{await tool('create_project',d);notify('프로젝트를 만들었습니다.');await loadProjects();await render('projects');});create.append(form);const list=card('참여 중인 프로젝트');if(!items.length)list.append(el('p','프로젝트를 만들거나 소유자에게 참여를 요청하세요.','empty'));for(const p of items){const row=el('div',undefined,'project-row'),info=el('div');info.append(el('strong',p.name),el('p',p.description,'small muted'),el('code',p.id),el('p',p.role,'small'));const acts=el('div',undefined,'actions');acts.append(p.id===currentProjectID?el('span','현재 프로젝트','pill'):btn('이 프로젝트로 작업',async()=>{setCurrentProject(p.id);await render('documents');}),btn('참여자',()=>members(p)));row.append(info,acts);list.append(row);}root.append(create,list);return root;}
async function members(project){const data=await tool('list_project_members',{project_id:project.id}),root=el('div'),c=card(project.name+' · 참여자');root.append(btn('프로젝트 목록',()=>render('projects'),'secondary refresh'),c);c.append(table(['이름','사용자 ID','역할'],data.map(u=>[u.name,el('code',u.user_id),u.role])));if(project.role==='owner'){const f=el('form');f.append(el('h2','권한 설정'));field(f,'사용자 ID (사용자의 내 계정 화면에서 확인)','user_id');selectField(f,'역할','role',[['editor','편집자 — 조회·추가·수정'],['viewer','조회자 — 조회만'],['remove','프로젝트에서 제외']]);formSubmit(f,'권한 적용',async d=>{if(d.role==='remove'&&!confirm('이 사용자를 프로젝트에서 제외할까요?'))return;await tool('set_project_member',{...d,project_id:project.id});notify('권한을 적용했습니다.');await members(project);});c.append(f);}$('#page').replaceChildren(root);}
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
async function account(){const root=el('div'),info=card('내 정보');info.append(el('p',me.name+' · '+me.email),el('p','권한: '+me.global_role,'small muted'),el('code',me.id),btn('사용자 ID 복사',async()=>{await navigator.clipboard.writeText(me.id);notify('복사했습니다.');}));const c=card('비밀번호 변경'),f=el('form');field(f,'현재 비밀번호','current','password').autocomplete='current-password';const next=field(f,'새 비밀번호 (12~128자)','next','password');next.minLength=12;next.maxLength=128;next.autocomplete='new-password';field(f,'새 비밀번호 확인','confirmation','password').autocomplete='new-password';f.append(el('p','변경하면 모든 로그인 세션과 MCP 키가 만료됩니다.','small muted'));formSubmit(f,'비밀번호 변경',async d=>{if(d.next!==d.confirmation)throw Error('비밀번호가 일치하지 않습니다.');await api('/api/password',{current:d.current,next:d.next});me=null;showLogin();notify('변경되었습니다. 다시 로그인하세요.');});c.append(f);root.append(info,c);return root;}
async function users(){const items=await api('/api/admin/users'),root=el('div'),c=card('사용자 초대'),f=el('form');field(f,'이름','name');field(f,'이메일','email','email');formSubmit(f,'초대 링크 만들기',async d=>{const result=await api('/api/admin/invite',d);await render('users');secret('사용자 초대',result.invitation_url,'이 링크를 해당 사용자에게 직접 전달하세요. 메일은 자동 발송하지 않습니다. 링크는 24시간 동안 한 번 사용할 수 있습니다.');});c.append(f);root.append(c);const list=card('팀 사용자 · 최근 200명');list.append(table(['사용자','상태','역할','관리'],items.map(u=>{const identity=el('div');identity.append(el('strong',u.name),el('div',u.email),el('code',u.id));const role=el('select');for(const v of ['member','admin']){const o=el('option',v);o.value=v;o.selected=u.global_role===v;role.append(o);}role.disabled=u.id===me.id;const actions=el('div',undefined,'actions');if(u.id!==me.id){actions.append(btn('역할 저장',async()=>{if(!confirm('역할을 변경하고 기존 로그인·접속 키를 만료시킬까요?'))return;await api('/api/admin/users',{user_id:u.id,disabled:u.disabled,global_role:role.value});await render('users');}),btn(u.disabled?'활성화':'비활성화',async()=>{if(!confirm('사용자 상태를 변경하고 기존 접속을 만료시킬까요?'))return;await api('/api/admin/users',{user_id:u.id,disabled:!u.disabled,global_role:u.global_role});await render('users');},u.disabled?'secondary':'danger'));}if(!u.disabled)actions.append(btn(u.activated?'비밀번호 재설정':'초대 재발급',async()=>{const result=await api('/api/admin/invite',{reset_user_id:u.id});secret('비밀번호 설정 링크',result.invitation_url,'해당 사용자에게 직접 전달하세요. 설정 완료 시 기존 로그인과 MCP 키가 만료됩니다.');}));return[identity,u.disabled?'비활성':u.activated?'활성':'초대 대기',role,actions];})));root.append(list);return root;}
async function audit(){const root=el('div'),c=card('작업·인증 감사 로그'),f=el('form',undefined,'actions'),rows=el('div');const actor=field(f,'사용자 ID','actor_id');actor.required=false;const project=field(f,'프로젝트 ID','project_id');project.required=false;selectField(f,'결과','outcome',[['','전체'],['success','성공'],['denied','차단'],['error','오류']]);let before=0;const more=btn('이전 기록',()=>load(false));async function load(reset){if(reset)before=0;const d=Object.fromEntries(new FormData(f));const items=await api('/api/admin/audit',{...d,before_id:before});before=items.at(-1)?.id||before;rows.replaceChildren(table(['시간','사용자','작업 / 대상','결과','소요 시간'],items.map(e=>{const target=el('div');target.append(el('strong',e.action),el('div',e.target_id||e.project_id||'—','small muted'));return[new Date(e.created_at).toLocaleString(),e.actor_name||'미인증',target,el('span',e.outcome+(e.error_code?' · '+e.error_code:''),'pill '+e.outcome),e.duration_ms+' ms'];})));more.disabled=items.length<100;}formSubmit(f,'조회',()=>load(true));c.append(el('p','문서 본문, 비밀번호, 키 원문은 기록하지 않습니다. 최대 100개씩 조회합니다.','small muted'),f,rows,more);root.append(c);await load(true);return root;}
if(inviteToken){history.replaceState(null,'',location.pathname);$('#invite-view').hidden=false;}else{loadMe().catch(()=>showLogin());}
