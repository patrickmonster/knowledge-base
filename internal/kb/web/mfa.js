'use strict';
// Two-step verification (TOTP): login second step, account settings, admin reset.
let mfaRecoveryMode=false;
function setMFAMode(recovery){
  mfaRecoveryMode=recovery;
  const input=$('#mfa-form input[name=code]');
  input.value='';
  if(recovery){input.removeAttribute('pattern');input.maxLength=11;input.inputMode='text';input.autocomplete='off';}
  else{input.pattern='[0-9 ]{6,7}';input.maxLength=7;input.inputMode='numeric';input.autocomplete='one-time-code';}
  $('#mfa-label').firstChild.textContent=recovery?'복구 코드':'인증 코드';
  $('#mfa-hint').textContent=recovery?'처음 설정할 때 받은 복구 코드 하나를 입력하세요 (예: abcde-fghjk). 한 번 쓴 코드는 다시 쓸 수 없습니다.':'인증 앱에 표시된 6자리 코드를 입력하세요.';
  $('#mfa-toggle').textContent=recovery?'인증 앱 코드 사용':'복구 코드 사용';
  input.focus();
}
function showMFAStep(){$('#login-form').hidden=true;$('#mfa-form').hidden=false;setMFAMode(false);}
function hideMFAStep(){$('#mfa-form').hidden=true;$('#mfa-form').reset();$('#login-form').hidden=false;}
$('#mfa-toggle').addEventListener('click',()=>setMFAMode(!mfaRecoveryMode));
$('#mfa-cancel').addEventListener('click',hideMFAStep);
$('#mfa-form').addEventListener('submit',async e=>{
  e.preventDefault();
  const b=e.target.querySelector('button:not([type])');b.disabled=true;
  try{
    await api('/api/login/mfa',{code:e.target.code.value});
    hideMFAStep();
    await loadMe();
  }catch(err){
    notify(err.message,true);
    // An expired challenge needs the password step again.
    if(/expired|Enter your email/i.test(err.message))hideMFAStep();
  }finally{b.disabled=false;}
});
function showRecoveryCodes(codes,lead){
  secret('복구 코드',codes.join('\n'),lead+' 각 코드는 한 번만 쓸 수 있습니다. 지금 안전한 곳(비밀번호 관리자 등)에 보관하세요. 이 화면을 닫으면 다시 볼 수 없습니다.');
}
async function mfaCard(){
  const c=card('2단계 인증 (OTP)'),st=await api('/api/mfa');
  if(st.enabled){
    c.append(el('p','사용 중 · '+new Date(st.enabled_at).toLocaleString()+' 설정 · 남은 복구 코드 '+st.recovery_codes_left+'개'));
    if(!st.available)c.append(el('p','서버의 MFA 설정(MFA_ENCRYPTION_KEY)이 없어 다음 로그인이 실패합니다. 관리자에게 알려 주세요.','small muted'));
    if(st.recovery_codes_left<=3)c.append(el('p','복구 코드가 얼마 남지 않았습니다. 새로 만들어 두세요.','small muted'));
    const regen=el('form');regen.append(el('h2','복구 코드 다시 만들기'));
    const rc=field(regen,'인증 앱의 6자리 코드','code');rc.autocomplete='one-time-code';rc.inputMode='numeric';
    regen.append(el('p','새로 만들면 기존 복구 코드는 모두 쓸 수 없게 됩니다.','small muted'));
    formSubmit(regen,'복구 코드 다시 만들기',async d=>{const r=await api('/api/mfa/recovery-codes',{code:d.code});await render('account');showRecoveryCodes(r.recovery_codes,'새 복구 코드입니다.');});
    const off=el('form');off.append(el('h2','2단계 인증 끄기'));
    field(off,'현재 비밀번호','password','password').autocomplete='current-password';
    field(off,'인증 앱 코드 또는 복구 코드','code').autocomplete='off';
    formSubmit(off,'2단계 인증 끄기',async d=>{if(!confirm('2단계 인증을 끌까요? 이후에는 비밀번호만으로 로그인됩니다.'))return;await api('/api/mfa/disable',d);notify('2단계 인증을 껐습니다.');await render('account');});
    c.append(regen,off);
    return c;
  }
  c.append(el('p','로그인할 때 비밀번호와 함께 인증 앱(Google Authenticator, Microsoft Authenticator, 1Password 등)의 6자리 코드를 입력합니다. MCP 접속 키는 영향을 받지 않습니다.','muted'));
  if(!st.available){c.append(el('p','이 서버에는 MFA_ENCRYPTION_KEY가 설정되지 않아 아직 사용할 수 없습니다. 관리자에게 문의하세요.','small muted'));return c;}
  const area=el('div');
  area.append(btn('설정 시작',async()=>{
    const s=await api('/api/mfa/setup',{});
    const box=el('div');
    box.append(el('p','1. 인증 앱에서 “설정 키 입력(수동 입력)”을 고르고 아래 키를 등록합니다. 계정 이름은 이메일, 종류는 시간 기반입니다.','small'));
    const key=el('div',undefined,'mcp-address');key.append(el('code',s.secret.replace(/(.{4})/g,'$1 ').trim()),btn('설정 키 복사',async()=>{await navigator.clipboard.writeText(s.secret);notify('설정 키를 복사했습니다.');}));
    box.append(key);
    const uri=el('details',undefined,'mcp-guide-details');uri.append(el('summary','otpauth 링크 (링크를 지원하는 앱·비밀번호 관리자용)'));const u=el('div',undefined,'mcp-address');u.append(el('code',s.otpauth_uri),btn('링크 복사',async()=>{await navigator.clipboard.writeText(s.otpauth_uri);notify('링크를 복사했습니다.');}));uri.append(u);box.append(uri);
    box.append(el('p','2. 앱에 나온 6자리 코드와 현재 비밀번호를 입력해 켭니다. '+s.expires_in_minutes+'분 안에 완료하세요.','small'));
    const f=el('form');
    field(f,'인증 앱의 6자리 코드','code').autocomplete='one-time-code';
    field(f,'현재 비밀번호','password','password').autocomplete='current-password';
    f.append(el('p','켜면 이 브라우저를 뺀 다른 로그인 세션은 모두 종료됩니다.','small muted'));
    formSubmit(f,'2단계 인증 켜기',async d=>{const r=await api('/api/mfa/enable',d);await render('account');showRecoveryCodes(r.recovery_codes,'2단계 인증을 켰습니다. 기기를 잃어버렸을 때 쓰는 복구 코드입니다.');});
    box.append(f);area.replaceChildren(box);
  }));
  c.append(area);
  return c;
}
function mfaResetButton(u){
  return btn('2단계 인증 초기화',async()=>{if(!confirm(u.name+'의 2단계 인증을 초기화할까요? 해당 사용자의 로그인 세션이 종료되고, 다음 로그인은 비밀번호만으로 됩니다. MCP 키는 유지됩니다.'))return;await api('/api/admin/users/mfa-reset',{user_id:u.id});notify('2단계 인증을 초기화했습니다.');await render('users');},'danger');
}
