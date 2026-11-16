# Discord 슬래시 명령

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
- 이 버전은 읽기 전용입니다. 할 일 추가·이동 같은 쓰기 명령은 아직 없습니다.

## 켜는 순서

1. Discord 개발자 포털에서 애플리케이션을 만들고 **General Information**의 **Public Key**를 복사합니다. 공개 키는 서명 확인용이라 비밀이 아닙니다. 봇 토큰은 서버에 넣지 않습니다.
2. 애플리케이션을 Discord 서버(길드)에 초대합니다(`applications.commands` 범위만 있으면 됩니다). 명령을 등록할 서버의 ID는 개발자 모드를 켜고 서버를 우클릭해 복사합니다(아래 5번에서만 씁니다).
3. 전역 관리자가 웹 **운영 현황 → Discord 연동**에 공개 키를 저장하고 **켜기**를 체크합니다. 켜려면 공개 키가 있어야 합니다. 환경변수는 필요 없습니다.
4. 같은 카드에 표시된 **Interactions Endpoint URL**을 포털 General Information의 *Interactions Endpoint URL*에 입력합니다. Discord가 서명된 PING을 보내 확인하므로 **3번을 먼저 저장·켜야** 합니다.
5. 명령을 한 번 등록합니다(봇 토큰은 이 요청에서만 쓰고 서버에는 저장하지 않습니다).

```bash
curl -X PUT "https://discord.com/api/v10/applications/<APPLICATION_ID>/guilds/<GUILD_ID>/commands" \
  -H "Authorization: Bot <BOT_TOKEN>" -H "Content-Type: application/json" \
  -d @discord-commands.json
```

`discord-commands.json`:

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
}]
```

## 계정 연결

1. 사람이 웹 **내 계정 → Discord 연결 → 연결 코드 만들기**(웹 로그인 세션 전용, MCP 키는 거부).
2. 10분 안에 Discord에서 `/kb link code:<코드>`. 코드는 한 번만 쓸 수 있고 해시로만 저장됩니다. 새 코드를 만들면 이전 코드는 무효가 됩니다.
3. Discord 계정 하나는 한 사람에게만, 한 사람은 Discord 계정 하나에만 연결됩니다. 이미 연결돼 있으면 먼저 해제합니다.
4. 코드 입력은 Discord 사용자당 10분에 5번, 서버 전체 10분에 120번으로 제한합니다.

## 보안

- 모든 요청은 `X-Signature-Ed25519`·`X-Signature-Timestamp`를 저장된 공개 키로 검증합니다. 실패하면 401이고 본문을 읽지 않습니다. 시각이 ±5분을 벗어나면 거부하고, 같은 interaction id는 두 번 처리하지 않습니다(409). IP당 분당 120건으로 제한합니다.
- 서버 허용 목록은 없습니다. 봇이 들어 있는 모든 서버와 DM에서 명령이 동작하지만, `/kb link`로 연결한 사람만 데이터를 볼 수 있고 답변은 본인에게만 보입니다. 아무나 봇을 자기 서버에 초대하지 못하게 하려면 개발자 포털에서 애플리케이션의 **Public Bot**을 끄세요.
- 설정 변경은 전역 관리자의 웹 세션 전용(MCP 키 거부)이고 입력 범위를 검증하며 감사 로그(`update_discord_settings`, 켜짐 여부만)를 남깁니다.
- 감사 로그에는 `discord_link`·`discord_unlink`·`discord_projects`·`discord_todos`가 사용자 기준으로 남습니다(할 일 내용은 남기지 않음).
- 서버 설정은 DB에 저장하며 관리자 화면에서 바꿉니다. 비밀값은 서버에 없습니다.

## 운영 메모

- 마이그레이션 `037_discord.sql`: `discord_settings`(서버 전체 1행), `discord_links`, `discord_link_codes`, `discord_interactions`(1일 지난 행은 요청 때 정리).
- `discord_settings.allowed_guild_ids` 열은 예전 허용 목록의 흔적으로 더 이상 읽거나 쓰지 않습니다. 다음 마이그레이션에서 지웁니다.
- 끄면(`켜기` 해제) 엔드포인트는 404를 돌려주고 연결 코드는 만들 수 없습니다. 연결 정보는 남습니다.
- Discord는 3초 안에 응답을 요구합니다. 지금 명령은 한 번의 DB 조회라 충분하지만, 시간이 걸리는 명령을 추가할 때는 지연 응답(type 5)과 후속 메시지가 필요합니다.
- 다음 단계 후보: 할 일 추가·이동 쓰기 명령, 프로젝트 slug 자동완성, 마감·담당 알림과 연계.
