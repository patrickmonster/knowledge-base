'use strict';

// GitHub webhooks: GitHub pushes issue and pull request events to this server.
const webhookOutcomes={processed:'처리',ignored:'무시',duplicate:'중복',rejected:'거부',failed:'실패'};
const webhookKinds={issue:'모두 이슈',question:'모두 질의',auto:'자동 — question 라벨이면 질의'};

// r is set_github_webhook's answer: webhook_id, payload_url and secret.
function webhookSecretDialog(r){
  secret('GitHub 웹훅 연결 정보',`Webhook ID: ${r.webhook_id}\nPayload URL: ${r.payload_url}\nContent type: application/json\nSecret: ${r.secret}`,
    `GitHub 저장소 ${r.webhook.repository}의 Settings → Webhooks → Add webhook에 Payload URL과 Secret을 붙여 넣으세요. 이벤트는 "Let me select individual events"에서 Issues와 Pull requests를 고릅니다. 이 정보는 "연결 정보"에서 다시 볼 수 있습니다.`);
}

async function githubWebhookCard(project,{members=[],areas=[]}={}) {
  const c=card('GitHub 웹훅 (자동 동기화)');
  c.append(el('p','GitHub에서 이슈가 열리거나 바뀌고, PR이 병합·닫힐 때 GitHub가 이 서버로 알려 줍니다. 이벤트가 생길 때만 반영하므로 GitHub API 호출 한도를 거의 쓰지 않고, 비공개 저장소의 새 이벤트도 토큰 없이 받습니다.','small muted'));
  c.append(el('p','웹훅은 설정 이후의 이벤트만 보냅니다. 기존 이슈는 아래 "기존 이슈 가져오기"로 한 번 가져오세요. 서버 재시작 중 놓친 PR 병합은 6시간 간격 확인으로 보정합니다.','small muted'));
  let data;
  try { data=await tool('list_github_webhooks',{project_id:project.id}); }
  catch(e) { c.append(el('p',e.message,'muted')); return c; }
  c.append(el('p',data.token_configured?'서버에 GITHUB_TOKEN이 설정되어 있습니다.':'서버에 GITHUB_TOKEN이 없습니다. 웹훅 수신에는 필요 없지만, 비공개 저장소의 처음 가져오기와 누락 보정 확인에는 필요합니다.','small muted'));
  const owner=project.role==='owner';
  const areaName=id=>areas.find(a=>a.id===id)?.name, memberName=id=>members.find(m=>m.user_id===id)?.name;
  c.append(table(['저장소','동기화','최근 수신',''],data.webhooks.map(h=>{
    const repo=el('div');
    const link=el('a',h.repository);link.href='https://github.com/'+h.repository;link.target='_blank';link.rel='noopener noreferrer';
    repo.append(link,el('div',(h.enabled?'켜짐':'꺼짐')+' · 설정 '+(h.created_by_name||''),'small muted'));
    const url=el('code',h.payload_url,'small');repo.append(url);
    const sync=[];
    if (h.import_issues) sync.push('이슈 → '+(webhookKinds[h.issue_kind]||h.issue_kind)+(h.area_id?` · 영역 ${areaName(h.area_id)||'?'}`:'')+(h.assignee_id?` · 담당 ${memberName(h.assignee_id)||'?'}`:''));
    if (h.track_pull_requests) sync.push('작업 PR 병합·닫힘');
    const last=el('div');
    last.append(el('div',h.last_delivery_at?`${webhookOutcomes[h.last_outcome]||h.last_outcome||''} · ${h.last_event||''} · ${new Date(h.last_delivery_at).toLocaleString()}`:'아직 받은 이벤트 없음'));
    if (!h.secret_set) last.append(el('div','Secret이 없습니다. Secret 재발급 후 GitHub 설정을 바꾸세요.','pill error'));
    if (h.last_error) last.append(el('div',h.last_error,'pill error'));
    if (h.recent?.length) {
      const d=el('details');d.append(el('summary','최근 10건'));
      const ul=el('ul','', 'small');
      for (const r of h.recent) ul.append(el('li',`${new Date(r.received_at).toLocaleString()} · ${r.event} · ${webhookOutcomes[r.outcome]||r.outcome}${r.detail?' · '+r.detail:''}`));
      d.append(ul);last.append(d);
    }
    const actions=el('div',undefined,'actions');
    if (owner) {
      if (h.secret_set) actions.append(btn('연결 정보',async()=>webhookSecretDialog(await tool('set_github_webhook',{project_id:project.id,webhook_id:h.id,show_secret:true}))));
      actions.append(btn(h.enabled?'끄기':'켜기',async()=>{await tool('set_github_webhook',{project_id:project.id,webhook_id:h.id,enabled:!h.enabled});await reload();}));
      actions.append(btn('Secret 재발급',async()=>{
        if(!confirm('Secret을 새로 만들면 GitHub에 등록된 기존 Secret으로 보낸 이벤트는 거부됩니다. GitHub 설정도 함께 바꿔야 합니다. 계속할까요?'))return;
        const r=await tool('set_github_webhook',{project_id:project.id,webhook_id:h.id,rotate_secret:true});webhookSecretDialog(r);await reload();
      }));
      actions.append(btn('삭제',async()=>{if(!confirm(`${h.repository} 웹훅을 삭제할까요? GitHub 저장소 설정의 웹훅도 지워 주세요.`))return;await tool('set_github_webhook',{project_id:project.id,webhook_id:h.id,remove:true});notify('삭제했습니다.');await reload();},'danger'));
    }
    return [repo,sync.join(' / ')||'—',last,actions];
  })));
  if (!owner) { c.append(el('p','GitHub 웹훅은 프로젝트 소유자가 관리합니다.','small muted')); return c; }
  const f=el('form',undefined,'issue-form-grid');
  const repo=field(f,'저장소 (owner/name 또는 GitHub 주소)','repository');repo.placeholder='octocat/hello-world';repo.maxLength=200;
  const check=(label,name)=>{const l=el('label',undefined,'check'),i=el('input');i.type='checkbox';i.name=name;i.checked=true;l.append(i,document.createTextNode(label));f.append(l);return i;};
  const issues=check('이슈를 이 프로젝트의 이슈·질의로 동기화','import_issues');
  const kind=selectField(f,'이슈 종류','issue_kind',Object.entries(webhookKinds));
  const area=selectField(f,'새 항목의 공통 작업 영역','area_id',[['','없음'],...areas.map(a=>[a.id,`${a.name} — ${a.owner_name}`])]);
  const assignee=selectField(f,'새 항목의 담당자 (비우면 영역 담당자)','assignee_id',[['','자동 / 미지정'],...members.filter(m=>m.role!=='viewer'&&!m.disabled).map(m=>[m.user_id,`${m.name} (${m.role})`])]);
  const tags=field(f,'추가 태그 (쉼표로 구분, 선택)','tags');tags.required=false;
  check('작업 기록의 PR 병합·닫힘 반영 (finish_task의 pull_requests)','track_pull_requests');
  const sync=()=>{for(const x of [kind,area,assignee,tags])x.parentElement.hidden=!issues.checked;};
  issues.addEventListener('change',sync);sync();
  f.append(el('p','저장하면 Webhook ID·Payload URL·Secret을 보여 줍니다(이후 "연결 정보"에서 다시 볼 수 있습니다). 이벤트는 저장 시점의 내 권한으로 처리되며, 이후 내가 편집 권한을 잃으면 거부됩니다.','small muted'));
  formSubmit(f,'웹훅 추가',async d=>{
    const input={project_id:project.id,repository:d.repository.trim(),import_issues:!!d.import_issues,track_pull_requests:!!d.track_pull_requests,issue_kind:d.issue_kind};
    if (d.area_id) input.area_id=d.area_id;
    if (d.assignee_id) input.assignee_id=d.assignee_id;
    const tagList=(d.tags||'').split(',').map(s=>s.trim()).filter(Boolean);if(tagList.length)input.tags=tagList;
    const r=await tool('set_github_webhook',input);
    webhookSecretDialog(r);await reload();
  });
  // Folded once a webhook exists so the list of webhooks reads first.
  const add=el('details',undefined,'issue-setup');add.open=!data.webhooks.length;
  add.append(el('summary','웹훅 추가'),f);c.append(add);
  async function reload(){c.replaceWith(await githubWebhookCard(project,{members,areas}));}
  return c;
}
