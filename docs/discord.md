# Discord 슬래시 명령과 메시지 명령

Discord에서 `/kb` 명령으로 할 일을 조회합니다. 이 버전은 **요청·응답 방식**입니다. Discord가 명령을 서버로 POST(`/api/discord/interactions`)하고 응답이 곧 HTTP 응답이라서, 봇 프로세스·게이트웨이 연결·봇 토큰이 서버에 필요하지 않습니다. 채널 메시지를 읽거나 서버가 먼저 Discord로 말을 거는 동작(실시간 푸시)은 이 버전에 없습니다. 알림은 기존 [웹훅 채널](team-rollout.md)을 씁니다.

## 명령

| 명령 | 동작 |
| --- | --- |
| `/kb link code:ABCD-EFGH` | 웹 **내 계정 → Discord 연결**에서 만든 코드로 내 Discord 계정을 연결 |
| `/kb unlink` | 연결 해제(웹 **내 계정**에서도 가능) |
| `/kb projects` | 내가 참여한 프로젝트와 slug·역할 |
| `/kb todos [project] [status] [mine]` | 할 일 목록. `project`는 slug(참여 프로젝트가 하나면 생략). `status`를 생략하면 완료를 뺀 목록. `mine`은 내 담당만 |

- 답변은 **본인에게만 보이는 메시지**(ephemeral)이고 `@` 멘션은 동작하지 않습니다(`allowed_mentions` 비움). 제목·상태·우선순위·담당자·마감일만 담고, 할 일 본문과 문서 본문은 보내지 않습니다. 2,000자에서 자르고 최대 15건을 나열합니다.
- 명령은 연결된 **내 계정의 프로젝트 역할**로 실행됩니다. 참여하지 않은 프로젝트는 찾지 못합니다. 계정이 비활성화되면 바로 거부됩니다.
- 슬래시 명령은 읽기 전용입니다. 쓰기는 아래 메시지 명령 "이슈로 등록"뿐입니다.

## 메시지 명령: 이슈로 등록

메시지(예: 포럼 글의 첫 게시물)를 우클릭 → **앱 → 이슈로 등록**을 누르면 그 메시지가 이슈가 됩니다. 슬래시 명령과 같은 Interactions Endpoint를 쓰므로 봇·게이트웨이·봇 토큰이 필요 없고, 메시지 본문은 Discord가 요청에 담아 보냅니다. 게시물이 올라올 때 자동으로 만드는 기능은 아닙니다(그건 게이트웨이 봇이 필요합니다).

- **누가**: `/kb link`로 계정을 연결한 사람이 그 계정의 프로젝트 역할로 실행합니다. 이슈를 만들 수 있는 편집자·관리자·소유자만 됩니다(조회자는 거부). 이슈 작성자는 그 사람입니다.
- **어느 프로젝트**: 관리자가 웹 **운영 현황 → Discord 연동 → 이슈로 등록**에서 Discord 채널 ID(포럼 채널)를 프로젝트 slug에 연결합니다. 포럼 글은 포럼 ID로, 없으면 스레드 ID로 찾습니다. 연결이 없으면 누른 사람의 프로젝트가 하나일 때만 그 프로젝트를 쓰고, 여러 개면 안내 메시지와 함께 거절합니다. 채널 ID는 개발자 모드를 켜고 채널을 우클릭해 복사합니다. 설정 변경은 전역 관리자 웹 세션 전용이며 감사 로그(`update_discord_channel`)에 채널 ID와 slug가 남습니다.
- **이슈 내용**: 종류 `issue`, 태그 `discord`. 제목은 포럼 글 제목(스레드 이름), 스레드가 아니면 메시지 첫 줄(200자). 본문은 메시지 내용과 작성자 이름, 메시지 링크, 첨부 파일 링크(https만, 최대 10개)입니다. 첨부는 Discord 링크로만 남기고 내려받지 않으며, Discord 링크는 시간이 지나면 열리지 않을 수 있습니다. 포럼 태그(CSS·오버레이 등)는 아직 반영하지 않습니다.
- **중복 방지**: 같은 메시지는 한 번만 등록합니다(`discord_issue_messages`). 다시 누르면 "이미 이슈로 등록된 메시지"로 알려 줍니다. 동시에 눌러도 이슈는 하나만 만들어집니다.
- **답변**: 본인에게만 보이는 메시지이고 `@` 멘션은 동작하지 않습니다. 감사 로그에는 `discord_issue`(프로젝트·이슈 ID·메시지 ID)가 남습니다.
- **켜는 순서**: 위 '켜는 순서' 5번의 **슬래시 명령 등록**을 다시 실행해야 Discord에 이 메뉴가 나타납니다(전체 덮어쓰기라 안전합니다). 서버 ID를 넣으면 바로, 비우면 최대 1시간 뒤 반영됩니다. 메시지 명령 이름은 `이슈로 등록`이며 등록 JSON(`discord-commands.json`)에도 들어 있습니다. 터미널로 등록할 때 이 항목을 빼면 PUT이 메시지 명령을 지웁니다.

## 켜는 순서

1. Discord 개발자 포털에서 애플리케이션을 만들고 **General Information**의 **Public Key**를 복사합니다. 공개 키는 서명 확인용이라 비밀이 아닙니다. 봇 토큰은 서버에 넣지 않습니다.
2. 애플리케이션을 Discord 서버(길드)에 초대합니다(`applications.commands` 범위만 있으면 됩니다). 명령을 등록할 서버의 ID는 개발자 모드를 켜고 서버를 우클릭해 복사합니다(아래 5번에서만 씁니다).
3. 전역 관리자가 웹 **운영 현황 → Discord 연동**에 공개 키를 저장하고 **켜기**를 체크합니다. 켜려면 공개 키가 있어야 합니다. 환경변수는 필요 없습니다.
4. 같은 카드에 표시된 **Interactions Endpoint URL**을 포털 General Information의 *Interactions Endpoint URL*에 입력합니다. Discord가 서명된 PING을 보내 확인하므로 **3번을 먼저 저장·켜야** 합니다.
5. 명령을 한 번 등록합니다. 두 가지 방법 중 하나를 고르세요.
   - **웹 화면(권장)**: 같은 **Discord 연동** 카드의 **슬래시 명령 등록**에 애플리케이션 ID, 서버 ID(비우면 모든 서버), 봇 토큰(개발자 포털 → Bot → Reset Token)을 입력하고 **명령 등록**을 누릅니다. 서버가 이 요청 한 번에만 토큰을 써서 Discord에 등록하고, 토큰은 저장·기록하지 않으며 입력란은 바로 비워집니다. 같은 명령을 다시 등록해도 안전합니다. 서버 ID를 넣으면 그 서버에 바로 반영되고, 비우면 모든 서버에 등록되며 반영에 최대 1시간 걸립니다.
   - **터미널**: 아래 `curl`(봇 토큰은 이 요청에서만 쓰고 서버에는 저장하지 않습니다).

```bash
curl -X PUT "https://discord.com/api/v10/applications/<APPLICATION_ID>/guilds/<GUILD_ID>/commands" \
  -H "Authorization: Bot <BOT_TOKEN>" -H "Content-Type: application/json" \
  -d @discord-commands.json
```

서버 ID를 비운 **전체 등록**은 DM에서도 동작하도록 명령마다 `integration_types`(0 서버 설치, 1 사용자 설치)와 `contexts`(0 서버, 1 봇 DM, 2 그 밖의 DM)를 함께 보냅니다. 웹 화면은 등록 직전에 `GET /applications/@me`의 `integration_types_config`로 개발자 포털 **Installation → Installation Contexts**에서 켠 설치 방식을 읽어 맞춥니다(서버 설치 → `contexts` `[0,1]`, 사용자 설치도 켜짐 → `integration_types` `[0,1]`, `contexts` `[0,1,2]`). 읽지 못하면 서버 설치만 있다고 보고 `[0]`·`[0,1]`로 등록합니다. 터미널로 전체 등록할 때는 아래 JSON의 각 명령에 같은 두 필드를 직접 넣으세요. 서버 등록은 그 서버에서만 보이므로 두 필드를 보내지 않습니다.

`discord-commands.json`(서버 등록용):

```json
[{
  "name": "kb", "description": "지식베이스",
  "options": [
    {"type": 1, "name": "link", "description": "내 계정과 연결",
     "options": [{"type": 3, "name": "code", "description": "웹 내 계정에서 만든 코드", "required": true}]},
    {"type": 1, "name": "unlink", "description": "연결 해제"},
    {"type": 1, "name": "projects", "description": "내 프로젝트"},
    {"type": 1, "name": "todos", "description": "할 일 목록",
     "options": [
       {"type": 3, "name": "project", "description": "프로젝트 slug"},
       {"type": 3, "name": "status", "description": "상태",
        "choices": [{"name": "시작 전", "value": "not_started"}, {"name": "진행 중", "value": "in_progress"},
                    {"name": "검토대기", "value": "in_review"},
                    {"name": "완료", "value": "completed"}, {"name": "아이디어", "value": "idea"}]},
       {"type": 5, "name": "mine", "description": "내 담당만"}]}
  ]
}, {"name": "이슈로 등록", "type": 3, "description": ""}]
```

### "알 수 없는 연동"(Unknown Integration)이 뜰 때

명령을 눌렀는데 Discord가 빨간 글씨로 **알 수 없는 연동**을 보여 주면 요청이 서버에 오기 전에 Discord가 거절한 것입니다(서버 로그·감사 로그에 남지 않습니다). 흔한 원인은 명령 정의에 `integration_types`·`contexts`가 없는 상태로 전체 등록된 명령을 **봇 DM**이나 **사용자 설치**(내 앱에 추가)로 쓰는 경우입니다.

1. 배포된 서버에서 **운영 현황 → Discord 연동 → 슬래시 명령 등록**을 서버 ID를 **비운 채** 다시 실행합니다(전체 덮어쓰기라 안전). 반영까지 최대 1시간 걸리며, Discord 앱을 새로 고치면(Ctrl+R) 빨리 보이기도 합니다.
2. DM이나 다른 서버에서도 쓰려면 개발자 포털 **Installation → Installation Contexts**에서 **User Install**을 켠 뒤 1번을 다시 실행합니다. 서버에서만 쓸 거라면 봇을 그 서버에 초대(`applications.commands`)하고 서버 채널에서 명령을 씁니다.
3. 예전에 서버 ID로 등록한 명령과 전체 등록한 명령이 함께 있으면 같은 `/kb`가 두 번 보일 수 있습니다. 서버 등록을 지우려면 터미널에서 그 서버로 빈 목록(`[]`)을 PUT 합니다.

## 계정 연결

1. 사람이 웹 **내 계정 → Discord 연결 → 연결 코드 만들기**(웹 로그인 세션 전용, MCP 키는 거부).
2. 10분 안에 Discord에서 `/kb link code:<코드>`. 코드는 한 번만 쓸 수 있고 해시로만 저장됩니다. 새 코드를 만들면 이전 코드는 무효가 됩니다.
3. Discord 계정 하나는 한 사람에게만, 한 사람은 Discord 계정 하나에만 연결됩니다. 이미 연결돼 있으면 먼저 해제합니다.
4. 코드 입력은 Discord 사용자당 10분에 5번, 서버 전체 10분에 120번으로 제한합니다.

## 보안

- 모든 요청은 `X-Signature-Ed25519`·`X-Signature-Timestamp`를 저장된 공개 키로 검증합니다. 실패하면 401이고 본문을 읽지 않습니다. 시각이 ±5분을 벗어나면 거부하고, 같은 interaction id는 두 번 처리하지 않습니다(409). IP당 분당 120건으로 제한합니다.
- 서버 허용 목록은 없습니다. 봇이 들어 있는 모든 서버와 DM에서 명령이 동작하지만, `/kb link`로 연결한 사람만 데이터를 볼 수 있고 답변은 본인에게만 보입니다. 아무나 봇을 자기 서버에 초대하지 못하게 하려면 개발자 포털에서 애플리케이션의 **Public Bot**을 끄세요.
- 명령 등록(`POST /api/admin/discord/register-commands`)도 전역 관리자의 웹 세션 전용(MCP 키 거부)입니다. 봇 토큰은 서버가 `discord.com`에 보내는 요청 한 번에만 쓰고 DB·감사 로그·로그·오류 메시지에 남기지 않으며(Discord가 되돌려 준 문구에 토큰이 들어 있어도 가립니다), 감사 로그에는 서버/전체 등록 여부(`scope`)만 남습니다. 같은 사용자는 10분에 10번까지 시도할 수 있습니다.
- 설정 변경은 전역 관리자의 웹 세션 전용(MCP 키 거부)이고 입력 범위를 검증하며 감사 로그(`update_discord_settings`, 켜짐 여부만)를 남깁니다.
- 감사 로그에는 `discord_link`·`discord_unlink`·`discord_projects`·`discord_todos`가 사용자 기준으로 남습니다(할 일 내용은 남기지 않음).
- 서버 설정은 DB에 저장하며 관리자 화면에서 바꿉니다. 비밀값은 서버에 없습니다.

## 운영 메모

- 마이그레이션 `037_discord.sql`: `discord_settings`(서버 전체 1행), `discord_links`, `discord_link_codes`, `discord_interactions`(1일 지난 행은 요청 때 정리). `039_discord_issue_channels.sql`은 `discord_channel_projects`(채널→프로젝트)와 `discord_issue_messages`(메시지→이슈, 중복 방지)를 만듭니다. `038_discord_drop_guild_ids.sql`은 예전 허용 서버 목록 열(`allowed_guild_ids`)을 지웁니다.
- 끄면(`켜기` 해제) 엔드포인트는 404를 돌려주고 연결 코드는 만들 수 없습니다. 연결 정보는 남습니다.
- Discord는 3초 안에 응답을 요구합니다. 지금 명령은 한 번의 DB 조회라 충분하지만, 시간이 걸리는 명령을 추가할 때는 지연 응답(type 5)과 후속 메시지가 필요합니다.
- 다음 단계 후보: 포럼 태그를 이슈 태그로 반영, 게시물 자동 등록(게이트웨이 봇 필요), 할 일 추가·이동 쓰기 명령, 프로젝트 slug 자동완성, 마감·담당 알림과 연계.
