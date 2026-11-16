'use strict';

// Discord 연동 (슬래시 명령). Two cards:
//  - discordLinkCard: any person links their own Discord account with a
//    single-use code (내 계정).
//  - discordAdminCard: a global administrator saves the application public key
//    and the allowed servers (운영 현황). The public key is not a secret.
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
    c.append(el('p','Discord에서 `/kb projects`, `/kb todos`를 사용할 수 있습니다. 답변은 본인에게만 보이고, 프로젝트 권한은 이 계정의 역할을 따릅니다.','small muted'),btn('연결 해제',async()=>{if(!confirm('Discord 연결을 해제할까요?'))return;await api('/api/discord/unlink',{});await render('account');},'danger'));
  }
  return c;
}

async function discordAdminCard(){
  const c=card('Discord 연동'),s=await api('/api/admin/discord');
  c.append(el('p','Discord 슬래시 명령을 받는 설정입니다(요청·응답 방식, 봇 토큰은 서버에 저장하지 않습니다). 순서: ① 아래에 공개 키와 서버 ID를 저장하고 켭니다 ② Discord 개발자 포털 → General Information → Interactions Endpoint URL에 아래 주소를 입력합니다.','small muted'));
  c.append(el('p','Interactions Endpoint URL'),el('code',s.endpoint_url||''));
  const f=el('form');
  const key=field(f,'애플리케이션 공개 키 (Public Key, 64자리 16진수)','public_key','text',s.public_key||'');key.required=false;key.maxLength=64;key.autocomplete='off';
  const guilds=el('textarea');guilds.name='allowed_guild_ids';guilds.rows=3;guilds.value=(s.allowed_guild_ids||[]).join('\n');
  const gl=el('label','허용할 Discord 서버 ID (한 줄에 하나, 최대 20개)');gl.append(guilds);f.append(gl);
  const cl=el('label','켜기'),cb=el('input');cb.type='checkbox';cb.name='enabled';cb.checked=!!s.enabled;cl.prepend(cb);f.append(cl);
  f.append(el('p','켜려면 공개 키와 서버를 하나 이상 저장해야 합니다. 허용하지 않은 서버에서는 명령이 동작하지 않습니다.','small muted'));
  formSubmit(f,'저장',async()=>{await api('/api/admin/discord',{enabled:cb.checked,public_key:key.value.trim(),allowed_guild_ids:guilds.value.split(/\s+/).filter(Boolean)});notify('저장했습니다.');await render('overview');});
  c.append(f);
  if(s.updated_at)c.append(el('p','마지막 저장: '+new Date(s.updated_at).toLocaleString(),'small muted'));
  return c;
}
