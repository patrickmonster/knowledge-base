'use strict';

// Discord 연동 (슬래시 명령). Two cards:
//  - discordLinkCard: any person links their own Discord account with a
//    single-use code (내 계정).
//  - discordAdminCard: a global administrator saves the application public key
//    and turns the commands on (운영 현황). The public key is not a secret.
async function discordLinkCard(){
  const c=card('Discord 연결');
  let st;
  try{st=await api('/api/discord/link');}catch(e){c.append(el('p','Discord 연결 상태를 불러오지 못했습니다: '+e.message,'muted'));return c;}
  if(!st.enabled&&!st.linked){c.append(el('p','관리자가 Discord 연동을 켜지 않았습니다.','muted'));return c;}
  c.append(el('p',st.linked?'연결됨 · '+new Date(st.linked_at).toLocaleString():'연결되지 않았습니다.',st.linked?'':'muted'));
  const out=el('div');
  if(!st.linked){
    c.append(el('p','코드를 만들고 10분 안에 Discord에서 `/kb link` 명령에 입력하세요. 코드는 한 번만 쓸 수 있습니다.','small muted'),btn('연결 코드 만들기',async()=>{const r=await api('/api/discord/link-code',{});out.replaceChildren(el('code',r.code),el('p','만료: '+new Date(r.expires_at).toLocaleTimeString()+' · 이 화면을 닫으면 다시 볼 수 없습니다.','small muted'));}),out);
  }else{
    c.append(el('p','Discord에서 `/kb projects`, `/kb todos`를 쓰고, 메시지를 우클릭 → 앱 → 이슈로 등록으로 이슈를 만들 수 있습니다. 답변은 본인에게만 보이고, 프로젝트 권한은 이 계정의 역할을 따릅니다.','small muted'),btn('연결 해제',async()=>{if(!confirm('Discord 연결을 해제할까요?'))return;await api('/api/discord/unlink',{});await render('account');},'danger'));
  }
  return c;
}

async function discordAdminCard(){
  const c=card('Discord 연동'),s=await api('/api/admin/discord');
  c.append(el('p','Discord 슬래시 명령을 받는 설정입니다(요청·응답 방식, 봇 토큰은 서버에 저장하지 않습니다). 순서: ① 아래에 공개 키를 저장하고 켭니다 ② Discord 개발자 포털 → General Information → Interactions Endpoint URL에 아래 주소를 입력합니다.','small muted'));
  c.append(el('p','Interactions Endpoint URL'),el('code',s.endpoint_url||''));
  const f=el('form');
  const key=field(f,'애플리케이션 공개 키 (Public Key, 64자리 16진수)','public_key','text',s.public_key||'');key.required=false;key.maxLength=64;key.autocomplete='off';
  const cl=el('label','켜기'),cb=el('input');cb.type='checkbox';cb.name='enabled';cb.checked=!!s.enabled;cl.prepend(cb);f.append(cl);
  f.append(el('p','켜려면 공개 키를 저장해야 합니다. 봇이 들어 있는 모든 Discord 서버에서 명령을 쓸 수 있고, 연결(/kb link)한 사람만 데이터를 볼 수 있습니다.','small muted'));
  formSubmit(f,'저장',async()=>{await api('/api/admin/discord',{enabled:cb.checked,public_key:key.value.trim()});notify('저장했습니다.');await render('overview');});
  c.append(f);
  if(s.updated_at)c.append(el('p','마지막 저장: '+new Date(s.updated_at).toLocaleString(),'small muted'));
  c.append(discordChannelForm(s.channels||[]),discordRegisterForm());
  return c;
}

// 메시지 명령 '이슈로 등록'이 이슈를 만들 프로젝트를 Discord 채널(포럼)별로 정한다.
// 채널 ID는 비밀이 아니다. 매핑이 없으면 누른 사람의 프로젝트가 하나일 때만 그 프로젝트를 쓴다.
function discordChannelForm(channels){
  const box=el('div');
  box.append(el('h3','이슈로 등록 — 채널과 프로젝트 연결'),el('p','Discord에서 메시지를 우클릭 → 앱 → 이슈로 등록을 누르면 그 메시지가 이슈가 됩니다. 이슈가 들어갈 프로젝트를 포럼(또는 텍스트) 채널별로 정합니다. 누른 사람은 /kb link로 연결한 계정이어야 하고, 그 프로젝트의 편집자 이상이어야 합니다. 명령은 아래 \'슬래시 명령 등록\'을 다시 실행해야 Discord에 나타납니다.','small muted'));
  if(!channels.length)box.append(el('p','연결된 채널이 없습니다.','small muted'));
  for(const ch of channels){
    const row=el('div','row');
    row.append(el('code',ch.channel_id),el('span',' → '+ch.project_name+' ('+ch.project_slug+')'),btn('삭제',async()=>{if(!confirm('이 채널 연결을 지울까요?'))return;await api('/api/admin/discord/channels',{channel_id:ch.channel_id,project_slug:''});notify('지웠습니다.');await render('overview');},'danger'));
    box.append(row);
  }
  const f=el('form');
  const id=field(f,'채널 ID (개발자 모드 → 포럼 채널 우클릭 → ID 복사)','channel_id','text','');id.inputMode='numeric';id.autocomplete='off';
  const slug=field(f,'프로젝트 slug','project_slug','text','');slug.autocomplete='off';
  formSubmit(f,'채널 연결 저장',async()=>{await api('/api/admin/discord/channels',{channel_id:id.value.trim(),project_slug:slug.value.trim()});notify('저장했습니다.');await render('overview');});
  box.append(f);
  return box;
}

// Registers /kb with Discord. The bot token is typed here for this one request
// only: the server uses it once and stores nothing, and the field is cleared.
function discordRegisterForm(){
  const mem=k=>{try{return localStorage.getItem('kb.discord.'+k)||'';}catch{return '';}},keep=(k,v)=>{try{localStorage.setItem('kb.discord.'+k,v);}catch{}};
  const f=el('form');
  f.append(el('h3','슬래시 명령 등록'),el('p','Discord에 /kb 명령을 한 번 등록합니다(명령이 바뀔 때 다시 실행해도 안전합니다). 봇 토큰은 이 요청에만 쓰고 서버에 저장하지 않으며, 입력란은 곧바로 비워집니다.','small muted'));
  const app=field(f,'애플리케이션 ID (개발자 포털 → General Information)','application_id','text',mem('app'));app.inputMode='numeric';app.autocomplete='off';
  const guild=field(f,'서버 ID (비우면 모든 서버와 DM에 등록 · 반영에 최대 1시간)','guild_id','text',mem('guild'));guild.required=false;guild.inputMode='numeric';guild.autocomplete='off';
  const tok=field(f,'봇 토큰 (개발자 포털 → Bot → Reset Token)','bot_token','password');tok.autocomplete='off';tok.maxLength=200;
  formSubmit(f,'명령 등록',async()=>{
    try{
      const r=await api('/api/admin/discord/register-commands',{application_id:app.value.trim(),guild_id:guild.value.trim(),bot_token:tok.value});
      keep('app',app.value.trim());keep('guild',guild.value.trim());
      notify('등록했습니다: /'+(r.commands||[]).join(', /')+(r.scope==='guild'?' (그 서버에 바로 반영)':' (모든 서버와 DM, 반영에 최대 1시간)'));
    }finally{tok.value='';}
  });
  return f;
}
