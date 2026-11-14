'use strict';

// Issue notification channels (webhook · Slack · email) for one project.
const notificationKinds={webhook:'웹훅',slack:'Slack',email:'이메일'};
const notificationEventNames={issue_assigned:'담당 배정',issue_answered:'답변 등록',issue_commented:'댓글',issue_status:'상태 변경 (해결·닫기·다시 열기)'};
const notificationStatus={pending:'대기',processing:'발송 중',sent:'발송됨',failed:'실패'};

async function notificationCard(project) {
  const c=card('담당자 알림');
  c.append(el('p','질의·이슈가 배정되거나 답변·댓글·상태 변경이 생기면 담당자와 질의자에게 알립니다. 본인이 한 변경은 본인에게 알리지 않습니다. 알림에는 제목·상태·이름만 담고 본문과 답변은 보내지 않습니다.','small muted'));
  const owner=project.role==='owner';
  let channels;
  try { channels=await tool('list_notification_channels',{project_id:project.id}); }
  catch(e) { c.append(el('p',e.message,'muted')); return c; }
  c.append(table(['채널','대상','알림','최근 발송',''],channels.map(ch=>{
    const last=ch.last_status?`${notificationStatus[ch.last_status]||ch.last_status}${ch.last_error?' · '+ch.last_error:''} · ${new Date(ch.last_event_at).toLocaleString()}`:'—';
    const actions=el('div',undefined,'actions');
    if (owner) {
      actions.append(btn('테스트',async()=>{await tool('test_notification_channel',{project_id:project.id,channel_id:ch.id});notify('테스트 알림을 대기열에 넣었습니다. 잠시 후 최근 발송을 확인하세요.');await reload();}));
      actions.append(btn(ch.enabled?'끄기':'켜기',async()=>{await tool('set_notification_channel',{project_id:project.id,channel_id:ch.id,enabled:!ch.enabled});await reload();}));
      actions.append(btn('삭제',async()=>{if(!confirm(`알림 채널 "${ch.name}"을 삭제할까요? 대기 중인 알림도 함께 지워집니다.`))return;await tool('set_notification_channel',{project_id:project.id,channel_id:ch.id,remove:true});notify('삭제했습니다.');await reload();},'danger'));
    }
    const name=el('div');name.append(el('strong',ch.name),el('div',notificationKinds[ch.kind]+(ch.enabled?'':' · 꺼짐'),'small muted'));
    return [name,ch.kind==='email'?'받는 사람의 계정 이메일':ch.target_hint,ch.events.map(e=>notificationEventNames[e]||e).join(', '),last+(ch.pending?` · 대기 ${ch.pending}건`:''),actions];
  })));
  if (!owner) { c.append(el('p','알림 채널은 프로젝트 소유자가 관리합니다.','small muted')); return c; }
  const f=el('form');
  f.append(el('h3','채널 추가'));
  const kind=selectField(f,'종류','kind',[['slack','Slack — incoming webhook URL'],['webhook','웹훅 — 임의 HTTPS 주소에 JSON 전송'],['email','이메일 — 받는 사람 계정 주소로 발송 (서버 SMTP 설정 필요)']]);
  const name=field(f,'이름','name');name.maxLength=100;name.placeholder='예: #team-knowledge';
  const target=field(f,'주소 (HTTPS)','target','url');target.maxLength=2000;target.placeholder='https://hooks.slack.com/services/…';
  const events=el('fieldset',undefined,'search-scope');events.append(el('legend','보낼 알림'));
  for (const [value,label] of Object.entries(notificationEventNames)) {
    const l=el('label',undefined,'check'),i=el('input');i.type='checkbox';i.name='events';i.value=value;i.checked=true;l.append(i,document.createTextNode(label));events.append(l);
  }
  f.append(events);
  f.append(el('p','주소에는 비밀 값이 들어 있을 수 있어 저장 후에는 일부만 표시합니다. 내부망·사설 주소로는 보내지 않습니다. 실패한 알림은 최대 6번까지 다시 보냅니다.','small muted'));
  const sync=()=>{const email=kind.value==='email';target.parentElement.hidden=email;target.required=!email;};
  kind.addEventListener('change',sync);sync();
  formSubmit(f,'채널 추가',async()=>{
    const picked=[...f.querySelectorAll('input[name=events]:checked')].map(i=>i.value);
    if (!picked.length) throw Error('보낼 알림을 하나 이상 고르세요.');
    await tool('set_notification_channel',{project_id:project.id,kind:kind.value,name:name.value.trim(),events:picked,...(kind.value==='email'?{}:{target:target.value.trim()})});
    notify('알림 채널을 추가했습니다.');await reload();
  });
  c.append(f);
  async function reload(){c.replaceWith(await notificationCard(project));}
  return c;
}
