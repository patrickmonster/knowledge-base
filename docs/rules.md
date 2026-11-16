# 작업 규칙 (소유자 규칙 · 개별 규칙)

AI가 프로젝트에서 커밋하거나 작업할 때 따를 규칙을 저장한다. 웹 **작업 규칙** 메뉴(사이드바의 현재 프로젝트 기준)와 MCP 도구로 관리한다.

| 종류 | 누가 바꾸나 | 누구에게 적용 | 변경 경로 |
| --- | --- | --- | --- |
| 소유자 규칙 (`scope: project`) | 소유자, 프로젝트 관리자 | 모든 참여자 | 웹 화면 전용(MCP 키는 FORBIDDEN). 프로젝트 생성 시 초기 규칙 입력 가능 |
| 개별 규칙 (`scope: personal`) | 본인 | 본인만(다른 사람에게 보이지 않음) | 웹 화면, 쓰기 가능한 MCP 키 |

- 종류(`category`): `commit`(커밋), `work`(작업, 기본), `other`(기타). 제목 1–200자, 내용 1–4000자.
- 개수 제한: 프로젝트당 소유자 규칙 50개, 참여자당 개별 규칙 50개.
- 충돌 시 소유자 규칙이 우선한다.
- `list_rules`의 `can_manage_project_rules`는 **이번 호출로** 소유자 규칙을 바꿀 수 있는지를 뜻한다. 소유자·관리자가 웹 세션으로 호출할 때만 `true`이고, 같은 사람이라도 MCP 키로 호출하면 `false`다(키는 소유자 규칙 변경이 거부되므로). 웹 화면의 편집 버튼은 이 값을 따른다.
- 수정은 `rule_id`와 `expected_revision`이 필요하며, 다른 사람이 먼저 바꿨으면 `CONFLICT`.
- 다른 참여자의 개별 규칙은 `NOT_FOUND`로 처리해 존재 여부를 드러내지 않는다.
- 감사 로그에는 규칙 ID·scope·category만 남고 제목·내용은 복사하지 않는다.

## 사용 / 미사용
규칙을 지우지 않고 잠시 끌 수 있다. 웹 규칙 표의 **사용** 열 스위치로 바꾸며, 권한은 삭제와 같다(소유자 규칙은 소유자·관리자가 웹 화면에서, 개별 규칙은 본인이 웹 화면이나 쓰기 가능한 MCP 키로).

- `set_rule_enabled`(`project_id`, `rule_id`, `enabled`). 상태가 바뀌면 revision이 1 올라가므로 열려 있던 수정 창은 `CONFLICT`가 난다. 같은 값을 다시 보내면 그대로 돌려준다.
- 미사용 규칙(`enabled: false`)은 `start_task`/`get_task_context`의 `context.rules`와 `list_rules` 기본 결과에서 빠진다. AI는 따르지 않는다.
- 웹 화면은 `list_rules`의 `include_disabled: true`로 미사용 규칙도 받아 흐리게 표시한다.
- 미사용 규칙도 개수 제한(50개)에 포함된다.

## 프로젝트 관리자 역할
소유자는 **프로젝트 → 참여자**에서 `관리자 — 편집 + 소유자 규칙 관리`를 부여한다(`set_project_member`, `role: admin`). 관리자는 편집자 권한에 소유자 규칙 관리를 더한 역할이며, 참여자 관리·재색인·소유자 이전은 할 수 없다. 소유자를 관리자에게 이전할 수 있고, 이전 소유자는 관리자로 남을 수도 있다.

## 프로젝트 생성 시 초기 규칙
웹 **프로젝트 만들기**의 "소유자 규칙" 칸에 한 줄에 하나씩 쓴다. `커밋:`/`작업:`/`기타:` 접두사로 종류를 정한다(생략 시 작업). MCP `create_project`의 `rules`는 웹 세션에서만 허용한다.

## AI 사용
- `resolve_project`가 `matched: true`이면 응답의 `rules`에 같은 내용이 담긴다(규칙이 없으면 생략). 프로젝트를 찾는 첫 호출에서 규칙을 받으므로 `start_task` 전에 브랜치를 만들거나 커밋해도 규칙을 본 상태가 된다.
- `start_task`/`get_task_context`의 `context.rules`에 `project_rules`, `personal_rules`, `guidance`가 담긴다(규칙이 없으면 생략).
- `list_rules`: 현재 사용 중인 규칙 다시 읽기. `save_rule`, `delete_rule`, `set_rule_enabled`: 개별 규칙 관리(소유자 규칙은 웹 화면에서).
- 규칙은 권한 있는 참여자가 쓴 지시이므로 검색 결과(sources)와 달리 따른다. 규칙이 불명확하거나 작업을 막으면 사용자에게 묻는다.

### 규칙 전달 경로
규칙은 서버가 밀어 넣지 않고 AI가 `resolve_project`·`start_task`·`get_task_context`·`list_rules`를 호출할 때만 전달된다. 이미 진행 중인 세션은 그 뒤에 추가된 규칙을 모르므로 아래 세 곳에서 다시 읽도록 안내한다.

- **MCP 서버 instructions**(`internal/kb/instructions.go`): 연결 시(initialize) 모든 클라이언트에 "변경 작업마다, 같은 세션의 후속 요청이라도 `start_task`를 새로 호출하거나 `list_rules`를 읽으라"고 알린다. 커밋·push·PR·브랜치 생성만 하는 요청도 변경 작업이므로 실행 전에 규칙을 읽으라고 명시한다. 프로젝트 선택(`resolve_project`, 일치하지 않는 서버는 건너뜀)과 저장소에 흔적을 남기지 않는 규칙도 여기 있어 AGENTS.md가 없는 저장소에서도 전체 절차가 동작한다.
- **사용자 수준 지침 / `get_agents_template`**(선택): 같은 절차를 서버·프로젝트 이름 없이 Markdown으로 준다. 저장소에 커밋하지 않고 `~/.claude/CLAUDE.md` 등 사용자 수준 지침에 둔다.
- **Claude Code 훅**(`.claude/settings.json` UserPromptSubmit): 매 메시지마다 `[rules-first]` 리마인더를 주입한다.

모두 안내일 뿐 호출을 강제하지는 않는다.

## 마이그레이션
`migrations/013_project_rules.sql`: `project_members.role`에 `admin` 허용, `project_rules` 테이블 추가.
`migrations/014_rule_enabled.sql`: `project_rules.enabled`(기본 true) 추가. 기존 규칙은 모두 사용 상태로 남는다.
