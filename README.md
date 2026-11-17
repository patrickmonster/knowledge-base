# 팀 공용 지식베이스 MCP

Go로 만든 팀 공용 지식 서버입니다. 프로젝트별 기획·결정·작업 히스토리를 MCP로 저장하고 검색합니다. 웹 관리 화면에서 이메일·비밀번호 로그인, 사용자 초대, 프로젝트 권한과 접속 키 관리, 감사 로그 조회를 제공합니다.

## 구현된 기능

- 이메일·비밀번호 로그인, 로그아웃, 비밀번호 변경
- 2단계 인증(TOTP 인증 앱, 복구 코드 10개, 관리자 초기화). 사용자가 직접 켜며 MCP 키는 영향 없음([안내](docs/mfa.md), [Google OTP 설정 가이드](docs/mfa-google-authenticator.md))
- 관리자 초대·비밀번호 재설정 링크: 24시간 유효, 1회 사용, 직접 전달 방식
- 전역 관리자 / 일반 사용자, 프로젝트 소유자 / 편집자 / 조회자 구분
- 사용자 비활성화, 전역 역할 변경, 프로젝트 참여자 역할 변경·제외
- MCP 키 발급·폐기: 조회 전용 / 편집 가능, 특정 프로젝트 제한, 1~90일 또는 만료 없음
- 감사 로그: 작업자, 작업 이름, 프로젝트·대상 ID, 권한 변경 값, 결과, 오류 코드, 소요 시간
- 운영 현황: 최근 24시간의 기록된 작업·차단·오류·로그인 실패와 활성 사용자·키 수
- 프로젝트·문서 추가·수정·조회, 원본 버전 보관, 동시 수정 충돌 방지
- 태그 정규화: 저장 시 앞뒤 공백·빈 값 제거, 대소문자 무시 중복 제거(처음 표기와 순서 유지). 기존 문서는 `008_normalize_tags.sql`로 정리
- Ollama 로컬 `bge-m3`로 1,024차원 임베딩 생성, pgvector 저장·의미 검색
- 한국어 키워드 검색과 의미 검색을 결합한 하이브리드 검색
- 문서 저장·수정 시 자동 임베딩 대기열, 실패 재시도·상태 조회·소유자 재색인
- 이슈·질의 베이스: 공통 작업 영역과 담당자, 질의·이슈 등록·답변·해결, 작업 시작 시 관련 질의 표시, 질의응답의 지식 문서화, GitHub 저장소 이슈 가져오기([안내](docs/issues.md#github-이슈-가져오기))
- 할 일 보드([안내](docs/todos.md)): 프로젝트 할 일을 상태(시작 전·진행 중·완료·아이디어) 열과 우선순위(높음·보통·낮음) 행으로 보는 웹 보드, 분류·진행률·담당자·마감일, 드래그로 이동, AI도 MCP로 추가·이동, 진행 중 작업과 연결해 시작·완료·PR 병합에 따라 자동 이동, 질의·이슈를 연결해 카드에 질의·보류 수 표시
- Discord 슬래시 명령([안내](docs/discord.md)): Interactions Endpoint(요청·응답, 봇 토큰 없음)로 `/kb link`·`unlink`·`projects`·`todos`. 웹 내 계정에서 만든 단회 코드로 연결하고 연결된 계정의 프로젝트 역할로만 읽기 조회, 메시지 우클릭 → 앱 → 이슈로 등록으로 포럼 글 등을 이슈로 등록(채널→프로젝트 연결은 관리자 화면, 편집자 이상, 같은 메시지 중복 방지), 본인에게만 보이는 답변, Ed25519 서명 검증, 설정(공개 키·켜기)과 명령 등록(봇 토큰은 저장하지 않고 요청 한 번에만 사용)은 관리자 웹 화면
- 팀 확산([안내](docs/team-rollout.md)): 이슈 담당자 알림과 선택형 문서 생성·승격 알림, 재확인 요약 주기 발송(웹훅·Slack·이메일), 웹 문서 관계 보기·편집, 문서 버전 비교, 검토 대기 사이드바 배지, 공통 작업 영역 삭제·정리, 다른 저장소용 AGENTS.md 생성, 프로젝트 내보내기·가져오기(JSON·Markdown)
- 작업 PR 병합·배포 자동 기록: `finish_task`에 남긴 PR을 GitHub에서 확인해 병합·배포(서버 자신의 저장소)되면 history에 상태 갱신 블록 추가, 미기록 PR은 작업 시작 맥락에 표시
- 지식 품질과 AI 작업 흐름: 중복·충돌 결정 감지, 재확인 담당자·주기 요약, start_task 맥락 활용도 피드백과 검색 품질 지표, finish_task 결정 후보 추출, 오래 열린 작업 정리, 용어집(glossary)과 검색 동의어 확장([안내](docs/knowledge-quality.md))
- 사용 통계 대시보드: 프로젝트별 도구 호출·활성 사용자·검색·문서 열람·작성·작업 수의 일별 추이, 문서 종류별 증가 추이, 결과 0건 검색어, 검색 방식별 검색 시간(HNSW 도입 판단용), 많이 쓴 도구·활동 사용자·많이 열람한 문서, 보유 현황, 검색 품질(`get_usage_stats`, 웹 '사용 통계')
- 재확인 · 맥락 피드백 화면: 오래된 문서·담당자·주기·마지막 확인과 담당 지정·확인 기록(편집자 이상), 검색 품질 지표와 작업별 피드백(웹 '재확인 · 피드백', 조회자는 읽기 전용)
- 공식 Go MCP SDK 기반 Streamable HTTP `/mcp`
- Railway용 Dockerfile, 배포 전 마이그레이션, `/healthz`, 배포 커밋·스키마 버전 확인 `/version`

Railway 개발·운영 앱을 배포했습니다. `develop`과 `main`을 각각 개발·운영에 연결했고, DB·Ollama와 사용자 데이터는 환경별로 분리됩니다.

## 기술 구성

| 영역 | 사용 기술 |
| --- | --- |
| 서버 | Go 1.26 이상, `net/http` (검증: Go 1.27.4) |
| MCP | 공식 `github.com/modelcontextprotocol/go-sdk` v1.8.0 |
| DB | PostgreSQL 18 + pgvector, pgx v5 |
| 비밀번호 | Argon2id, 사용자별 임의 salt |
| 웹 로그인 | DB 세션, HttpOnly / SameSite=Strict 쿠키, HTTPS에서 Secure |
| MCP 인증 | Authorization Bearer API 키; 원문 대신 SHA-256 해시 저장 |
| 웹 화면 | Go 바이너리에 포함한 HTML / CSS / JavaScript; 별도 Node 빌드 불필요 |
| 임베딩 | 자체 호스팅 Ollama, 기본 `bge-m3`, 1,024차원 |
| 검색 | pgvector 코사인 유사도 + `tsvector` / GIN, 한국어 bigram, RRF 순위 결합 |
| 운영 | Docker, Railway, DB 기반 요청 제한과 감사 로그 |

MCP는 호출 인터페이스이고, 검색은 Go 서버가 수행합니다. 연결된 AI 클라이언트가 검색 결과를 근거로 작업물을 작성합니다. 임베딩은 자체 호스팅한 Ollama에서 실행하며 외부 AI API 키가 필요하지 않습니다. 문서와 질문은 설정된 `OLLAMA_BASE_URL` 서버로 전송됩니다. Railway에서는 같은 환경의 비공개 Ollama 서비스에서 처리합니다.

## 권한 정책

| 작업 | 소유자 | 관리자 | 편집자 | 조회자 |
| --- | --- | --- | --- | --- |
| 문서·검색·과거 버전 조회, 작업 규칙 조회 | 가능 | 가능 | 가능 | 가능 |
| 개별 규칙 추가·수정·삭제(본인 것만) | 가능 | 가능 | 가능 | 가능 |
| 문서 추가·수정·보관 처리 | 가능 | 가능 | 가능 | 불가 |
| draft 검토(승인·반려, 웹 화면 전용) | 가능 | 가능 | 가능 | 불가 |
| 소유자 규칙 추가·수정·삭제(웹 화면 전용) | 가능 | 가능 | 불가 | 불가 |
| 참여자 역할 변경·제외 | 가능 | 불가 | 불가 | 불가 |
| 소유자 이전(웹 화면 전용) | 가능 | 불가 | 불가 | 불가 |

- 프로젝트 **관리자**(`admin`)는 편집자 권한에 소유자 규칙 관리를 더한 역할입니다. 전역 관리자(사용자 관리)와는 별개이며, 참여자 관리·재색인·소유자 이전은 소유자만 합니다.
- 작업 규칙(커밋·작업 규칙)은 [docs/rules.md](docs/rules.md)를 참고하세요.

- 모든 활성 사용자는 프로젝트를 생성할 수 있고, 생성자가 소유자가 됩니다.
- 전역 관리자는 사용자 초대·비활성화·역할 변경, 팀 키 폐기, 전체 감사 로그·운영 통계에 접근합니다.
- **전역 관리자도 참여하지 않은 프로젝트 문서를 자동으로 읽을 수 없습니다.** 프로젝트 자료 접근은 별도로 검사합니다.
- 소유자는 웹 **프로젝트 → 참여자** 화면에서 활성 참여자(편집자·조회자)에게 소유권을 넘길 수 있습니다(`transfer_project_owner`). 확인을 위해 프로젝트 주소용 이름(slug)을 다시 입력해야 하며, 이전 소유자는 편집자(기본) 또는 조회자로 남습니다. 프로젝트 소유자 필드와 참여자 역할이 한 트랜잭션에서 바뀌어 소유자는 항상 한 명입니다. 로그인한 사람만 할 수 있고 MCP 도구 목록에 노출되지 않으며 MCP 키 호출은 거부됩니다. 감사 로그에 새 소유자(target)와 이전 소유자의 남는 역할이 기록됩니다.
- 프로젝트에 참여하지 않은 사용자에게는 바로 이전할 수 없습니다. 먼저 참여자로 추가하세요. 소유자가 비활성화되면 이전도 할 수 없으므로, 관리자가 소유자를 다시 활성화해 이전한 뒤 비활성화해야 합니다.
- 자기 자신의 관리자 역할이나 활성 상태를 변경할 수 없습니다. 다른 관리자가 처리해야 합니다.
- MCP 키 범위와 현재 프로젝트 권한을 모두 만족해야 요청이 허용됩니다. 키로 웹 관리자 API에 접근할 수 없습니다.
- 계정 비활성화 또는 전역 역할 변경 시 기존 세션·키·초대 링크를 무효화합니다. 재활성화해도 이전 접속 자격은 살아나지 않습니다.
- 비밀번호 변경·재설정 시 모든 기존 세션·키를 무효화합니다. 다시 로그인해서 새 키를 발급해야 합니다.
- 2단계 인증을 켠 사용자는 비밀번호 다음에 인증 앱 코드나 복구 코드를 입력해야 세션이 발급됩니다. 켜면 다른 세션이 종료되고, 전역 관리자는 다른 사용자의 2단계 인증을 초기화할 수 있습니다(해당 사용자의 세션 종료, 키 유지). 서버에 `MFA_ENCRYPTION_KEY`가 필요합니다([안내](docs/mfa.md)).
- 차단은 다음 요청부터 적용됩니다. 이미 시작된 작업은 권한 변경과 DB 잠금 순서에 따라 먼저 완료될 수 있습니다.

## 로컬 실행

Docker가 필요합니다. `.env`는 커밋하지 않습니다.

```sh
cp .env.example .env
```

`.env`의 `POSTGRES_PASSWORD`를 URL에 사용할 수 있는 충분히 긴 임의 값(예: 32자리 hex)으로 바꾸고, `DATABASE_URL`의 비밀번호도 동일하게 바꿉니다. 최초 관리자용 `BOOTSTRAP_EMAIL`, `BOOTSTRAP_NAME`, `BOOTSTRAP_PASSWORD`도 설정합니다. 비밀번호는 12~128자입니다.

```sh
docker compose up -d postgres ollama
docker compose exec ollama ollama pull bge-m3
docker compose up --build -d
docker compose --profile setup run --rm bootstrap
```

마지막 bootstrap 명령은 관리자가 아직 없을 때만 성공합니다. 이 명령 대신 웹에서 만들 수도 있습니다. 관리자가 없는 서버는 첫 화면에 **최초 관리자 만들기**를 보여 줍니다([설치 안내](.railway/README.md#최초-관리자와-연결)). 모델은 최초 한 번 내려받아 전용 볼륨에 보관합니다. 이후 `.env`에서 `BOOTSTRAP_PASSWORD`를 제거합니다. [http://localhost:8080](http://localhost:8080)에서 설정한 계정으로 로그인합니다. 일반 앱 컨테이너에는 bootstrap 비밀번호를 전달하지 않습니다.

Go로 직접 개발하려면 환경변수를 설정한 상태에서 다음 명령을 실행합니다. 프로그램은 `.env`를 자동으로 읽지 않습니다.

```sh
go run ./cmd/knowledge-base migrate
go run ./cmd/knowledge-base bootstrap
go run ./cmd/knowledge-base serve
```

## 사용 순서

1. 최초 관리자가 웹에 로그인합니다.
2. **사용자 관리**에서 이름·이메일로 초대 링크를 발급합니다. 링크는 해당 사용자에게 직접 전달하며 메일은 자동 발송하지 않습니다.
3. 사용자가 링크에서 비밀번호를 설정하고 로그인합니다.
4. **프로젝트**에서 프로젝트를 생성하고, 참여자의 사용자 ID로 편집자·조회자 권한을 부여합니다. 사용자 ID는 **내 계정** 또는 관리자 사용자 목록에서 확인합니다. 같은 화면의 **저장소**에서 이 프로젝트의 git 저장소 주소를 여러 개(최대 20개) 등록·해제합니다. AI는 작업 중인 저장소의 git remote로 프로젝트를 찾습니다.
5. **MCP 접속 키**에서 작업·프로젝트 범위와 유효 기간(1~90일 또는 만료 없음)을 선택합니다. 키 원문은 발급 직후 한 번만 표시됩니다.
6. AI 클라이언트에 아래 정보를 등록합니다.

```text
URL: https://<your-domain>/mcp
Authorization: Bearer <발급받은 MCP 키>
```

클라이언트는 Streamable HTTP와 직접 설정하는 Authorization 헤더를 지원해야 합니다. 웹 로그인 세션 자체를 MCP 자격 증명으로 사용하지 않습니다. OAuth 연결만 지원하는 클라이언트는 현재 지원하지 않습니다.

영구 키는 새 키 발급 시 **만료 없음 (영구)**을 선택합니다. 기존 키의 만료일은 자동으로 바뀌지 않습니다. 영구 키도 폐기, 비밀번호 변경·재설정, 계정 비활성화 시 차단되며 현재 프로젝트 권한을 따릅니다.

접속 키 화면의 **MCP 연결 가이드**에서 현재 환경의 주소와 Codex·Cursor 설정 예시를 복사할 수 있습니다. 실제 키 대신 `KNOWLEDGE_BASE_API_KEY` 환경변수를 참조하므로 실행 중인 클라이언트에 해당 변수를 전달해야 합니다. GUI 앱은 터미널의 변수나 프로젝트 `.env` 파일을 자동으로 읽지 않을 수 있습니다.

[공식 OpenAI MCP 안내](https://learn.chatgpt.com/docs/extend/mcp)의 `bearer_token_env_var`와 [Cursor 공식 안내](https://cursor.com/docs/mcp)의 헤더 환경변수 참조 방식을 사용합니다. OAuth 전용 연결은 지원하지 않습니다.

키 발급 API는 `never_expires: true`와 `days: 0`(또는 생략)을 받으면 만료 없음 키를 생성합니다. `never_expires`가 없거나 `false`이면 기존 동작대로 `days` 기본값은 30, 허용 범위는 1~90입니다. 만료 없음과 양수 일수를 함께 지정하면 거절합니다. 영구 키는 목록의 `expires_at`과 발급 응답의 `expires_in_days`가 `null`입니다.

## MCP 도구

| 도구 | 역할 |
| --- | --- |
| `whoami` | 인증된 사용자 확인 |
| `get_server_version` | 실행 중인 배포 커밋과 DB 스키마 버전(적용/기대 마이그레이션 수) 확인 |
| `create_project`, `list_projects` | 프로젝트 생성·조회. `list_projects`는 프로젝트별 `repositories`를 함께 내려주고, `repository`(git remote)를 넘기면 그 저장소에 등록된 프로젝트만 돌려준다 | |
| `resolve_project` | git remote로 이 서버에서 저장소가 등록된 프로젝트를 찾는다. `matched: true`면 `project`와 작업 규칙 `rules`(`list_rules`와 같은 내용, 없으면 생략), 여러 개면 `candidates`, 없으면 `matched: false`(이 서버는 건너뛰라는 `guidance`). 읽기 전용이라 연결된 모든 knowledge 서버에 불러도 안전하다 | |
| `list_project_upstreams`, `request_project_upstream`, `remove_project_upstream` | 상위 프로젝트(디자인 시안·기획·공통 명세) 연결 조회·요청·해제. 하위 소유자·관리자가 요청하고 상위 소유자·관리자가 웹에서 승인(`approve_project_upstream`, 웹 전용)한다. 연결된 하위 참여자는 상위를 읽기 전용으로 참조하고 이슈만 올린다([상위 프로젝트](docs/upstreams.md)) |
| `add_project_repository`, `remove_project_repository` | 소유자·관리자: 프로젝트의 소스 저장소 등록·해제. `host/owner/name` 소문자로 정규화해 저장(인증 정보·포트 제거), 프로젝트당 20개. GitHub 웹훅이 걸린 저장소는 마이그레이션 029에서 자동 등록 |
| `set_project_member`, `list_project_members` | 참여자 권한 관리 |
| `create_document`, `update_document` | 지식 추가·버전 기반 수정 |
| `get_document`, `list_documents` | 본문 조회, 종류·상태·태그·기간·작성자 필터 목록과 재확인 필요 표시 |
| `get_document_history` | 최근 50개 수정 버전의 작성자·이유 조회 |
| `link_documents`, `unlink_documents`, `get_document_links` | 문서 간 관계(`supersedes`, `implements`, `related`) 추가·삭제·조회 |
| `search_knowledge` | 프로젝트 내 하이브리드·의미·키워드 검색(관련 질의·이슈는 `issues`로 따로) |
| `search_across_projects` | 참여 중인 여러 프로젝트를 한 번에 검색(`project_ids` 생략 시 전체, 최대 50개) |
| `get_embedding_status` | 프로젝트별 임베딩 대기·처리·완료·실패 건수 조회 |
| `reindex_project_embeddings` | 소유자 전용: 현재 본문으로 청크를 다시 만들고 벡터 재생성 예약(새 버전은 만들지 않음) |
| `get_task_context` | 작업에 필요한 발췌문·출처·최근 결정 탐색 정보 구성. 선택 입력 `related_project_ids`로 참여 중인 다른 프로젝트 자료를 `related_sources`에 따로 함께 조회 |
| `start_task`, `finish_task` | 작업 시작 맥락 저장·종료 결과와 문서 일괄 저장. `start_task`에 `branch`·`worktree`·`paths`를 넘기면 같은 곳의 다른 진행 중 작업을 `run.context.concurrent_runs` 경고로 알려 준다(차단 없음, 7일 넘은 작업은 `stale`; [안내](docs/task-workflow.md#동시-작업-경고-concurrent_runs)) |
| `get_task_run`, `list_task_runs` | 작업 상태·출처·완료 문서 조회 및 중단된 작업 ID 복구 |
| `check_task_deployments` | `finish_task`의 `pull_requests`로 남긴 PR의 병합·배포를 지금 확인하고 history에 상태 갱신 블록 추가(서버도 20분마다 자동 확인) |
| `get_document_templates`, `create_templated_document` | 기획·결정·히스토리 양식 조회·필수 항목 검증 후 저장 |
| `list_work_areas`, `set_work_area` | 공통 작업 영역과 담당자 조회·관리 |
| `create_issue`, `list_issues`, `get_issue` | 질의·이슈 등록·목록(상태·담당자·검색어 필터)·상세 |
| `answer_issue`, `comment_issue`, `update_issue` | 답변·댓글·해결/닫기/다시 열기, 질의응답 지식 문서화(`publish_answer`) |
| `import_github_issues` | GitHub 저장소 이슈를 질의·이슈로 가져오기(단방향, 다시 가져오면 갱신). 서버 `GITHUB_TOKEN`(선택)·`GITHUB_API_URL`(선택, GitHub Enterprise). 이후 변경은 웹 콘솔의 GitHub 웹훅(`/api/github/webhooks/<id>`, 웹훅별 Secret)으로 자동 반영 |
| `list_todos`, `save_todo`, `move_todo`, `delete_todo` | 할 일 보드 조회(상태·우선순위·분류·담당자 필터, 연결 작업 수·PR 상태 포함)·추가·수정(`expected_revision`)·칸 이동(`before_id`)·삭제. 쓰기는 편집자 이상 |
| `link_issue_todo` | 질의·이슈를 할 일에 연결·해제(편집자 이상). `create_issue`의 `todo_id`로도 연결하고, `task_id`만 넘기면 그 작업의 할 일을 따른다([안내](docs/todos.md#이슈질의와-연결)) |
| `link_task_todo` | 열린 작업을 할 일에 연결·해제. `start_task`의 `todo_id`와 같이 할 일을 진행 중으로 옮기고, 작업 완료·PR 병합 때 완료로 옮긴다([안내](docs/todos.md#진행-중-작업과-연결)) |
| `get_usage_stats` | 프로젝트 사용 통계(최근 1~365일, 기본 30일, 한국 시간 일 단위): 합계·일별 추이·종류별 증가·결과 0건 검색어·검색 시간·HNSW 검토 판정(`hnsw_review`)·상위 도구·사용자·열람 문서·보유 현황 |
| `set_search_thresholds` | 소유자·관리자: 프로젝트의 HNSW 검토 기준(`vector_p95_ms`, `scanned_chunks`) 설정. 호출마다 두 값을 바꾸며 0이나 생략은 서버 기본값(`HNSW_REVIEW_*`) |

문서 종류는 `brief`, `decision`, `history`, `artifact`, `reference`입니다. 상태는 `draft`, `active`, `archived`이고 기본 검색에는 활성 문서의 최신 버전만 포함됩니다. `update_document`는 `expected_revision`과 수정 이유 `reason`이 필요합니다. 충돌하면 최신 내용을 읽고 변경을 병합해야 합니다. 긴 본문의 일부만 고칠 때는 `patch.content`로 전체를 다시 보내지 않고 `edits`(`old_str`/`new_str` 쌍 배열, 최대 50개)를 보냅니다. 편집은 순서대로 적용되고, 각 `old_str`는 그 시점 본문에서 정확히 한 번 나와야 합니다(없거나 여러 번이면 `EDIT_NOT_APPLIED`로 아무것도 저장하지 않음). `new_str`가 비어 있으면 삭제이고, `patch.content`와 함께 쓸 수 없습니다. 제목·태그 등 다른 `patch` 필드와는 함께 쓸 수 있으며 응답에 `edits_applied`가 붙습니다.

`list_documents`는 `kind`, `status` 외에 다음 필터를 받습니다. 모든 필터는 함께 적용(AND)됩니다.

- `tags`: 나열한 태그를 **모두** 가진 문서(대소문자 무시, 최대 10개)
- `updated_after`, `updated_before`: 최종 수정 시각 범위. `YYYY-MM-DD`는 한국 시간(UTC+9) 기준이며 끝 날짜는 그날 전체를 포함합니다. RFC3339 시각은 그대로 쓰고 `updated_before`는 그 시각을 포함하지 않습니다.
- `author`: 사용자 ID. 문서를 만든 사람 또는 마지막으로 수정한 사람이 일치하면 포함합니다.
- `stale_only`, `stale_days`: 재확인 필요 문서만 보기, 기준 일수(기본 90, 1~3650)

목록의 각 문서에는 `needs_recheck`, `days_since_update`, `stale_after_days`가 붙습니다. `needs_recheck`는 **active 상태의 `reference`·`brief` 문서**가 기준 일수 넘게 갱신되지 않았을 때만 `true`입니다. `decision`·`history`는 특정 시점의 기록이라 표시하지 않고, draft·archived 문서도 제외합니다. AI는 이 표시가 있는 문서를 근거로 쓰기 전에 현재 상태를 확인하고, 여전히 맞으면 `update_document`로 갱신해 표시를 해제합니다. 같은 판정(기본 90일)은 `get_document`(현재 버전만), `search_knowledge`, `search_across_projects`, `start_task`·`get_task_context`의 출처 발췌에도 `recheck` 객체(`needs_recheck`, `days_since_update`, `stale_after_days`, `message`)로 붙습니다. 검색 순위는 바꾸지 않고 경고만 붙입니다. 웹 **지식 문서** 화면에서 같은 필터로 문서를 찾고 본문을 열어 볼 수 있으며 재확인 필요 문서에는 배지가 표시됩니다. 마이그레이션은 필요 없습니다.

문서 간 관계는 `link_documents`로 같은 프로젝트의 두 문서를 잇습니다. 관계는 현재 상태 메타데이터라 새 버전을 만들지 않고 청크·벡터도 바꾸지 않습니다. 같은 요청을 반복해도 한 번만 저장됩니다(`009_document_links` 마이그레이션 필요).

| 관계 | source → target | 효과 |
| --- | --- | --- |
| `supersedes` | 새 문서 → 대체된 옛 문서(같은 종류, history 제외) | source가 **active**인 동안 target은 `search_knowledge`·작업 맥락에서 현재 문서들 뒤로 밀리고 `superseded_by`가 붙습니다. 작업 맥락의 최근 결정 목록에서도 빠집니다. draft 대체안은 승인 전까지 순위에 영향을 주지 않습니다. 순환 연결은 `CONFLICT`입니다. |
| `implements` | history → 실행한 decision·brief | 결정 문서의 `implemented_by`로 "이 결정이 어떤 작업으로 실행됐나"를 거꾸로 찾습니다. `finish_task`의 `implements`에 문서 ID를 넣으면 히스토리 저장과 함께 연결되고 본문에 `## 실행한 기획·결정` 목록이 붙습니다. |
| `related` | 방향 없음 | 양쪽 어느 방향으로 요청해도 한 건으로 저장·삭제됩니다. |

`get_document`(현재 버전)와 `get_document_links`는 `supersedes`, `superseded_by`, `implements`, `implemented_by`, `related`로 묶어 돌려줍니다. 과거 버전 조회에는 관계를 붙이지 않습니다. 검색에서는 결과 목록 안에서만 순서를 바꾸며 대체된 문서를 숨기지 않습니다.

웹 **진행 중 작업** 화면은 `start_task`로 시작해 아직 `finish_task`로 마치지 않은 작업을 보여 줍니다. 상태·내 작업 필터, 7일 넘게 열린 작업 표시, 시작 자료 스냅샷, 완료 기록과 PR 병합·배포 상태, 연결된 미해결 질의 수를 확인하고, 작업 카드에서 연결된 질의·이슈(이슈 · 질의 화면에서 그 작업의 질의만 표시)와 완료 기록의 history 문서(문서 화면에서 열림)로 바로 이동하며, 중단 처리(`close_task_run`)·오래된 작업 일괄 정리(소유자)를 할 수 있습니다. 사이드바에 진행 중 작업 수 배지가 붙습니다. 시작 때 넘긴 브랜치·worktree·경로가 카드에 표시되고, 시작 자료에서 동시 작업 경고를 볼 수 있습니다.

웹 **할 일 보드** 화면은 프로젝트 할 일을 상태 열 × 우선순위 행으로 보여 줍니다. 카드를 끌어(휴대폰에서는 길게 눌러 집은 뒤) 다른 칸이나 카드 사이로 옮기고, “숨기기” 메뉴로 상태 열·우선순위 행을 숨기고(브라우저에만 저장, 카드 데이터는 그대로) 완료 체크로 완료 열로 보내며, 카드를 눌러 제목·내용·분류·진행률·담당자·마감일을 고칩니다. 자세한 내용은 [docs/todos.md](docs/todos.md)를 참고하세요(`025_todos` 마이그레이션 필요).

웹 **검토 대기** 화면은 프로젝트의 draft 문서를 모아 보여 줍니다. 소유자·편집자가 본문을 확인하고 사유와 함께 승인(`active`) 또는 반려(`archived`)하면 사유가 `검토 승인: …` / `검토 반려: …` 형식으로 버전 기록에 남고 감사 로그에 결정이 기록됩니다(사유 본문은 감사 로그에 복사하지 않음). 이 동작(`review_document`)은 로그인한 사람만 할 수 있으며 MCP 도구 목록에 노출되지 않고 MCP 키 호출은 거부됩니다. AI가 만든 미확정 지식을 같은 AI가 승격하지 않도록 하기 위한 제한입니다. 단, `update_document`로 상태를 바꾸는 기존 경로는 그대로 남아 있습니다.

원본 문서는 최대 100,000자입니다. 작업 맥락은 기본 8,000자의 발췌문 예산을 사용하며 메타데이터는 예산에서 제외합니다. 기본 검색은 의미·키워드 검색을 결합합니다. 모델의 의미 판단과 검색 결과는 실제 프로젝트 자료로 평가해야 합니다. 작업물 본문·링크 외에 [Railway 버킷 첨부 파일](docs/attachments.md)을 지원합니다. 파일당 최대 25 MiB이며 PDF·DOCX·XLSX는 업로드 때 본문을 추출해 artifact 문서로 색인합니다(스캔본 OCR은 미지원).

## 로컬 임베딩 동작

문서 저장 → 청크 생성·대기열 저장 → Go 백그라운드 작업자 → Ollama `/api/embed` → pgvector `vector(1024)` 저장 순서입니다. 검색할 때도 같은 모델로 질문을 임베딩하고, 접근 가능한 프로젝트의 최신 문서에서 코사인 유사도를 계산합니다. 기본 `hybrid` 모드는 키워드 결과와 RRF로 순위를 합칩니다. 이때 `history` 문서는 두 순위 목록에서 각각 10단계 아래에 있는 것으로 계산합니다. history는 여러 주제를 길게 담은 시점 기록이고 작업이 끝날 때마다 늘어나서, 그대로 두면 reference·brief·decision·glossary 같은 현재 지식을 밀어내고 새 기록이 생길 때마다 상위 결과가 바뀌기 때문입니다. 숨기지는 않으므로 다른 문서가 가까이 일치하지 않으면 history가 그대로 상위에 옵니다(`semantic`·`lexical` 모드는 이 보정을 하지 않습니다). `start_task`·`get_task_context`의 출처 8건 중 history는 최대 3건이고, 나머지 자리는 다른 문서로 채우며 다른 문서가 모자라면 남은 history로 채웁니다.

- 저장 요청은 원문·수정 이력·청크·대기열을 하나의 트랜잭션으로 보관합니다. 임베딩은 비동기이므로 완료 전에는 키워드 검색만 가능합니다. 기존 문서도 마이그레이션 후 자동 대기열에 들어갑니다.
- 청크에는 검색에 의미 없는 생성 메타데이터를 넣지 않습니다. `finish_task`가 history 본문 끝에 붙이는 `## 작업 연결 정보` 블록(작업 ID, 시작 시각, 시작 시 검색 자료 목록)과 `kb://` 인용 URL은 원문에는 그대로 남지만 키워드·의미 검색 대상에서 빠집니다. 규칙 변경 전에 저장된 문서는 `reindex_project_embeddings`로 청크를 다시 만들어야 적용됩니다.
- 문서를 수정하면 새 청크 ID를 발급합니다. 이전 작업이 뒤늦게 완료되어도 새 내용에 오래된 벡터를 붙이지 않습니다. 과거 버전은 보관하지만 의미 검색은 현재 버전을 대상으로 합니다.
- 모델 호출은 최대 8개 청크를 묶어서 처리하며 문서 저장 트랜잭션 밖에서 실행합니다. DB 작업 임대로 여러 앱 인스턴스가 중복 처리하지 않도록 합니다.
- 실패하면 30초부터 간격을 늘려 최대 5회 시도합니다. 원문은 유지합니다. `get_embedding_status`의 오류 코드와 `retries_exhausted`를 확인하고 모델 복구 후 소유자가 `reindex_project_embeddings`를 호출할 수 있습니다.
- 검색 질문 임베딩은 최대 20초 기다립니다. `hybrid`에서 실패하면 `retrieval: "lexical-fallback"`과 `warning`을 반환합니다. `semantic`은 실패를 오류로 반환합니다. 권한 검사를 통과한 요청만 모델을 호출합니다.
- 현재 코사인 검색은 프로젝트 범위를 제한한 정확 검색입니다. 대규모 자료용 근사 검색 인덱스(HNSW)는 아직 없으므로 데이터 증가 시 검색 시간을 측정해야 합니다. `get_usage_stats`의 `hnsw_review`가 측정값을 HNSW 검토 기준(벡터 검색 p95, 비교 청크 수)과 비교해 `review`·`ok`·`insufficient_data`로 알려 줍니다. 기준은 아래 서버 기본값을 쓰고, 프로젝트 소유자·관리자가 `set_search_thresholds`나 웹 '사용 통계'의 '검색 시간' 카드에서 프로젝트별로 바꿀 수 있습니다.

| 변수 | 기본값 / 의미 |
| --- | --- |
| `OLLAMA_BASE_URL` | `http://localhost:11434`; Compose는 `http://ollama:11434`로 지정 |
| `EMBEDDING_MODEL` | `bge-m3`; 현재 DB는 1,024차원 모델만 허용 |
| `EMBEDDING_NUM_THREADS` | `4`; 요청마다 Ollama `num_thread`로 전달. `0`이면 Ollama가 정하며 컨테이너 CPU 한도보다 많은 호스트 코어를 잡아 타임아웃이 날 수 있음 (허용 0~64) |
| `OLLAMA_NUM_PARALLEL`, `OLLAMA_MAX_LOADED_MODELS` | Ollama 서비스에 `1` 지정; 동시 요청과 적재 모델을 하나로 제한 |
| `EMBEDDING_ENABLED` | `true`; `false`이면 생성 작업을 멈추고 기본 검색을 키워드 방식으로 수행 |
| `OLLAMA_NO_CLOUD` | Ollama 서비스에 `true` 지정; 클라우드 모델 비활성화 |
| `HNSW_REVIEW_VECTOR_P95_MS` | `100`(1~60000). 벡터 검색 p95가 이 값(ms) 이상이면 HNSW 검토 필요. 벡터 검색 20회 이상일 때만 판정. 프로젝트 설정이 우선 |
| `HNSW_REVIEW_SCANNED_CHUNKS` | `20000`(100~100000000). 한 번의 벡터 검색이 이만큼 청크를 비교하면 HNSW 검토 필요. 프로젝트 설정이 우선 |

모든 앱 인스턴스에 같은 모델 설정을 적용합니다. 모델 이름이 바뀌면 기존 완료 청크를 자동 재생성하고, 새 모델과 다른 벡터는 검색에서 제외합니다. 같은 이름의 모델 파일을 갱신했거나 재시도가 소진된 경우 모든 해당 프로젝트를 명시적으로 재색인합니다. 모델 차원을 바꾸려면 DB 마이그레이션과 코드 변경도 필요합니다. 운영에서는 검증한 Ollama 이미지와 모델 버전을 고정해 사용합니다.

`search_knowledge` 응답은 이전 배열 형식에서 아래 객체 형식으로 변경했습니다. `get_task_context`도 검색 방식·경고·미완료 청크 수를 전달합니다.

```json
{
  "hits": [],
  "retrieval": "hybrid",
  "warning": "임베딩 미완료 또는 실패 시에만 포함되는 안내",
  "unindexed_chunks": 0
}
```

관련 질의·이슈가 있으면 `issues`(최대 5건)와 벡터가 아직 없는 질의 조각 수 `unindexed_issue_chunks`가 더해집니다. 질의 본문·답변도 문서와 같은 방식으로 청크·임베딩합니다([안내](docs/issues.md#검색)).

`mode`는 `hybrid`, `semantic`, `lexical` 중 선택합니다. `warning`의 실제 메시지는 영어입니다. 검색 점수는 모드마다 다르며 확률이나 신뢰도 백분율이 아닙니다.

### 프로젝트 횡단 검색

`search_across_projects`는 `search_knowledge`와 같은 `query`, `mode`, `status`, `limit`을 받고 `project_id` 대신 선택적 `project_ids`를 받습니다. 생략하면 참여 중인 모든 프로젝트(오래된 순 최대 50개, 초과분은 `omitted_projects`와 `warning`으로 알림)를, 지정하면 그 프로젝트만 검색합니다. 질의 임베딩은 한 번만 만들고 모든 프로젝트의 결과를 하나의 순위로 합칩니다.

- 응답은 `search_knowledge` 형식에 검색한 `projects`(id, slug, name, role)를 더하고, 각 결과에 `project_id`, `project_slug`, `project_name`을 붙입니다.
- 참여하지 않은 프로젝트를 지정하면 단일 프로젝트 검색과 같이 `NOT_FOUND`로 거부하고 다른 프로젝트도 검색하지 않습니다.
- 특정 프로젝트로 제한된 MCP 키는 그 프로젝트만 검색합니다. 다른 프로젝트를 지정하면 `FORBIDDEN`입니다.
- 웹 **지식 검색** 화면에서 프로젝트를 골라(기본: 전체) 같은 검색을 실행하고 결과 본문을 바로 열람합니다.
- 승인된 상위 프로젝트가 있으면 `start_task`·`get_task_context`가 상위의 active 자료를 `upstream_sources`로 자동으로 함께 돌려줍니다([상위 프로젝트](docs/upstreams.md)).
- `start_task`·`get_task_context`에 `related_project_ids`(최대 5개)를 넘기면 같은 권한 검사로 관련 프로젝트를 함께 조회해 `related_sources`에 프로젝트 이름과 함께 따로 담습니다([작업 흐름](docs/task-workflow.md#관련-프로젝트-자료-related_sources)).

모델 준비 후 MCP에서 문서를 저장하고 `get_embedding_status`의 `ready` 증가를 확인한 다음, `search_knowledge`에 `mode: "semantic"`을 지정해 검사합니다. `/healthz`는 DB 연결 상태만 검사하며 모델 준비 여부를 보장하지 않습니다.

배포 확인은 `GET /version`(인증 불필요) 또는 MCP `get_server_version`을 사용합니다. 응답의 `commit`이 배포한 커밋이고, `schema_version`이 `schema_expected`와 같으면(`schema_current: true`) 모든 마이그레이션이 적용된 상태입니다. 커밋은 Docker 빌드 인자 `RAILWAY_GIT_COMMIT_SHA`(`commit_source: build`), 런타임 변수(`railway`), 로컬 Go VCS 정보(`vcs`) 순으로 찾고 없으면 `unknown`입니다. 응답에는 커밋 해시·스키마 번호·프로세스 시작 시각만 담기며 비밀값은 포함하지 않습니다.

참고: [Ollama 임베딩 API](https://docs.ollama.com/api/embed), [bge-m3 모델](https://ollama.com/library/bge-m3), [Ollama 로컬 설정](https://docs.ollama.com/faq), [pgvector](https://github.com/pgvector/pgvector).

## Railway 배포

현재는 `production` / `main` 단일 서버로 운영합니다. 기존 개발 앱·DB·Ollama 서버는 제거했고, 개발 DB 볼륨은 백업한 뒤 삭제했으며 모델 볼륨도 삭제했습니다. 로컬 Compose와 자동 테스트는 계속 사용할 수 있습니다.

[설치·운영 안내](.railway/README.md)와 [배포 설정](.railway/railway.ts)에 앱·DB·Ollama·첨부 버킷 구성이 포함되어 있습니다. 나중에 다른 Railway 프로젝트에 설치할 때 새 체크아웃에서 아래 명령으로 로컬 설정만 준비할 수 있습니다. 현재 서버를 복제하거나 새 프로젝트를 즉시 생성하지 않습니다.

```sh
npm ci --prefix .railway
npm --prefix .railway run setup -- --project your-team-knowledge --repo your-org/your-repository
```

실제 배포 정보는 Git에서 제외한 `.railway/deployment.local.json`에 보관하며 생성기는 기존 파일을 덮어쓰지 않습니다. 새 환경 설치 시 도메인 생성, 마이그레이션, 최초 관리자 생성과 업로드·검색 검증이 필요합니다. 기존 계정·DB·파일은 자동 이관되지 않습니다.

## 감사·운영 범위

- 성공한 업무 변경과 감사 기록을 같은 트랜잭션에 저장합니다. 감사 저장이 실패하면 해당 변경도 되돌립니다.
- 업무 권한 거부, 로그인 실패, MCP 인증 실패를 기록합니다. DB 장애 때는 감사 저장 자체가 실패할 수 있습니다.
- 비밀번호·키 원문·초대 토큰·문서 본문을 감사 로그에 저장하지 않습니다. 사용자 ID, 문서 ID 등 메타데이터는 포함됩니다.
- 감사 로그는 애플리케이션 API로 수정·삭제할 수 없지만, DB 운영자의 변경까지 막는 별도 WORM 저장소는 아닙니다.
- 관리자 통계는 **기록된 업무·인증 이벤트**를 집계합니다. 모든 HTTP 요청, 정적 파일 조회, 프로토콜 검증 실패를 포함하는 네트워크 지표는 아닙니다.
- 화면은 수동 새로고침 방식입니다. 외부 알림은 이슈 담당자 알림, 문서 생성·승격 알림(채널별로 켜야 함), 재확인 요약 주기 발송(웹훅·Discord 웹훅·Slack·SMTP 이메일, `RECHECK_DIGEST_SCHEDULE`·`RECHECK_DIGEST_HOUR`, [안내](docs/knowledge-quality.md#재확인-요약-발송))만 제공합니다. 초대·비밀번호 재설정 메일 발송, 자동 이상 탐지, MFA 강제 적용, SSO는 아직 제공하지 않습니다. MFA는 사용자가 선택해 켭니다.
- 사용자·키·프로젝트 참여자 관리 목록은 현재 최대 200개, 프로젝트 목록은 페이지당 최대 50개입니다. 감사 로그는 ID 커서로 100개씩 조회합니다.
- 로그인은 이메일당 15분에 10회, IP당 15분에 50회로 제한합니다. 비밀번호 변경은 사용자당 15분에 10회, 일반 인증 요청은 사용자당 분당 240회입니다. 웹 세션은 12시간 후 만료됩니다.
- 만료된 세션과 요청 제한 버킷은 주기적으로 정리합니다.

### 감사 로그·문서 버전 보관 정책

기본값은 **무기한 보관**입니다. 전역 관리자가 웹 **운영 현황 → 보관 정책**에서 기간을 저장해야 삭제가 시작됩니다(웹 세션 전용, MCP 키 거부, 변경은 `update_retention` 감사 이벤트로 기록). 저장하면 재시작 없이 다음 정리부터 적용됩니다. 앱이 1시간마다 실행하며, 한 번에 종류별 최대 5,000건만 지우므로 쌓인 기록은 여러 번에 나눠 정리됩니다. 여러 앱 인스턴스가 있어도 DB 잠금으로 한 곳만 실행합니다.

화면에서 비워 둔 항목은 아래 환경변수(서버 기본값)를 따릅니다. 환경변수는 필수가 아니며 하위 호환용 기본값입니다. 화면에 저장한 값이 환경변수보다 우선합니다.

| 변수 | 기본값 / 의미 |
| --- | --- |
| `AUDIT_RETENTION_DAYS` | `0`(무기한). 지정 시 90~3650일. 이보다 오래된 감사 로그 삭제 |
| `REVISION_RETENTION_DAYS` | `0`(무기한). 지정 시 30~3650일. 이보다 오래된 문서 이전 버전 삭제 |
| `REVISION_KEEP_LATEST` | `20`(1~1000). 기간이 지나도 문서마다 최신 N개 버전은 유지 |

결과 0건 검색어(`search_misses`)와 검색 시간 표본(`search_timings`)은 위 변수와 별도로 최대 365일(감사 로그 기간이 더 짧으면 그 기간)이 지나면 매시간 정리합니다.

- 문서의 현재 버전, 최신 `REVISION_KEEP_LATEST`개 버전, 첨부 파일이 연결된 버전은 삭제하지 않습니다. 삭제된 버전을 `get_document`의 `revision`으로 조회하면 `NOT_FOUND`입니다. 오래된 `start_task` 맥락이나 history에 남은 revision 번호도 조회되지 않을 수 있습니다.
- 정리할 때마다 `retention_purge` 감사 이벤트에 삭제 건수와 적용 정책만 남깁니다(행위자 없음).
- 관리자 **운영 현황** 화면(`GET /api/admin/retention`)에서 현재 정책, 남은 건수, 가장 오래된 기록, 마지막 정리 결과를 확인하고, 같은 화면에서 기간을 바꿉니다(`POST /api/admin/retention`: `audit_days`·`revision_days`·`revision_keep_latest`, null이면 서버 기본값, 0일이면 무기한).
- 삭제는 되돌릴 수 없습니다. 보관 기간을 줄이기 전에 DB 백업을 확인하세요.

## 검증

```sh
gofmt -l .        # 출력이 있으면 gofmt -w 로 정리 (CI에서 검사)
go vet ./...
go test ./...
```

웹 화면(`internal/kb/web`) 테스트는 Node로 실행합니다. 스모크 테스트(`scripts/web-smoke.test.js`)는 jsdom에서 모든 화면을 목 API(`scripts/web-fixtures.js`)로 열고 버튼을 눌러, 예외·오류 알림·화면에 찍힌 `undefined`·`NaN`을 잡습니다. 서버 응답 형태를 바꾸면 `web-fixtures.js`도 함께 고칩니다.

```sh
npm ci --prefix scripts
npm --prefix scripts test
```

실제 PostgreSQL + pgvector 통합 테스트는 **테스트 전용 DB** 연결을 지정해야 실행됩니다. 각 테스트는 임의의 전용 스키마를 만들고 종료 시 제거합니다. 미지정 시 통합 테스트가 건너뛰어집니다.

```sh
TEST_DATABASE_URL='postgres://<user>:<password>@localhost:55432/<test-db>?sslmode=disable' go test -race ./...
```

GitHub Actions는 pgvector 포함 PostgreSQL 서비스를 실행하고 통합 테스트를 포함해 검증합니다. 테스트 범위: 비밀번호 해싱, 초대·재설정·만료, 계정 비활성화, 세션·키 폐기, 프로젝트 격리, 조회 전용 권한, 관리자 권한, 동시 수정, 버전 보관, 한국어 검색, 보관 자료 제외, 감사 저장 실패 시 롤백, CSRF·Origin 검사, 실제 MCP 클라이언트의 HTTP 연결.

실제 로컬 모델까지 검증하려면 `bge-m3`를 설치한 Ollama 주소를 함께 지정합니다. 기본 통합 테스트는 제어 가능한 대체 임베딩으로 권한·재시도·동시 수정을 검사하고, 아래 테스트는 실제 모델의 생성·저장·한국어 의미 검색을 검사합니다.

```sh
TEST_DATABASE_URL='<test-only PostgreSQL URL>' TEST_OLLAMA_URL=http://localhost:11434 go test -v ./internal/kb -run TestLiveOllamaStorageAndRetrieval
```

검증 기록: 실제 Ollama 0.40.16 + `bge-m3`에서 한국어 문서 2개를 1,024차원 벡터로 생성·저장하고, 다른 표현의 환불 질문으로 해당 정책을 검색했습니다. pgvector 통합 테스트, `go test -race ./...`, `go vet ./...`, Go 빌드와 Railway 설정 타입 검사도 통과했습니다. 실제 Railway 배포 성능은 별도 검증 대상입니다.

## 코드 위치

- `cmd/knowledge-base`: 서버·마이그레이션·최초 관리자 실행 명령
- `internal/kb/auth.go`, `admin.go`, `audit.go`: 로그인·권한·운영 관리
- `internal/kb/mfa.go`, `web/mfa.js`, `migrations/016_mfa.sql`: 2단계 인증(TOTP·복구 코드·관리자 초기화)
- `internal/kb/http.go`, `web/`: 웹 API 및 관리 화면
- `internal/kb/tools.go`, `documents.go`, `search.go`: MCP와 지식 저장·검색
- `internal/kb/embeddings.go`: Ollama 호출과 벡터 응답 검증
- `internal/kb/embedding_worker.go`: 비동기 생성·pgvector 저장·재시도
- `internal/kb/vector_search.go`: 질문 임베딩·권한 범위 내 의미 검색·하이브리드 순위
- `internal/kb/issues.go`, `web/issues.js`: 이슈·질의 베이스와 작업 시작 연동
- `internal/kb/todos.go`, `web/todos.js`: 할 일 보드(`025_todos.sql`)
- `internal/kb/discord.go`, `discord_register.go`, `web/discord.js`, `discord_issue.go`, `migrations/037_discord.sql`, `039_discord_issue_channels.sql`: Discord 슬래시 명령 수신·서명 검증·계정 연결·읽기 명령·명령 등록
- `internal/kb/github_issues.go`: GitHub 이슈 가져오기(`import_github_issues`)
- `internal/kb/github_webhooks.go`: GitHub 웹훅 설정(웹 전용)과 수신·서명 검증, 이슈·PR 이벤트 반영
- `internal/kb/deployments.go`: 작업 PR의 병합·배포 확인과 history 상태 갱신 블록(`check_task_deployments`, 주기 확인)
- `internal/kb/usage.go`, `web/usage.js`: 사용 통계 집계와 대시보드
- `web/rechecks.js`: 재확인·맥락 피드백 화면(기존 재확인·검색 품질 MCP 도구를 웹에서 호출)
- `migrations/003_embeddings.sql`: 벡터 컬럼과 작업 대기열
- `migrations/`: 버전 관리되는 DB 스키마

참고: [공식 MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk), [Railway PostgreSQL](https://docs.railway.com/databases/postgresql), [Railway 배포 전 명령](https://docs.railway.com/deployments/pre-deploy-command), [OWASP 비밀번호 저장](https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html).

## 내부 화면의 벡터 탐색

로그인 후 왼쪽 **벡터 탐색** 메뉴에서 프로젝트와 문서 상태를 선택합니다.

- 프로젝트 전체의 임베딩 완료·대기·처리·실패 현황을 확인합니다.
- 선택한 문서 상태에서 현재 모델로 생성된 벡터를 최대 500개까지 표시합니다. 500개를 넘으면 최근 수정한 문서 순으로 표시하며 전체 개수와 표시 범위를 안내합니다.
- 문서 종류별 색상, 제목·발췌문 필터, 확대·축소·이동을 제공합니다. 점이나 키보드로 접근 가능한 조각 목록을 선택하면 본문과 해당 버전의 원문을 확인할 수 있습니다. 본문과 원문은 Markdown(제목·목록·표·코드·인용·링크)으로 표시합니다. 렌더러(`web/markdown.js`)는 DOM 노드만 만들고 HTML을 해석하지 않으며 링크는 http(s)·mailto만 허용합니다. 단, ```` ```html preview ```` 코드 블록은 격리된 iframe에서 디자인 시안으로 렌더링합니다([안내](docs/design-preview.md)).
- 유사한 조각 5개는 같은 프로젝트·문서 상태의 전체 준비된 벡터에서 실제 코사인 유사도로 조회합니다. 지도에 없는 조각도 상세 내용을 열 수 있습니다.

분포도는 L2 정규화한 벡터에 서버 측 PCA를 적용한 3차원 근사 표현입니다. 기본 3D 보기는 드래그·방향키로 회전하고 휠로 확대하며, 2D 보기(성분 1·2)로 전환할 수 있습니다. 표시된 표본의 분산 중 얼마를 표현하는지 성분별로 안내하며, 점 사이 거리 자체를 실제 유사도나 신뢰도로 해석하지 않습니다. 표본·문서 상태가 바뀌거나 새로고침하면 위치가 달라질 수 있고, 동일 벡터는 겹쳐 표시됩니다. 원본 벡터 배열은 브라우저에 전달하지 않습니다.

조회는 웹 로그인 세션과 CSRF 검사를 거치며, 관리자도 참여하지 않은 프로젝트의 벡터와 본문을 읽을 수 없습니다. 지도·유사 조각 조회 및 권한 거부는 감사 로그에 기록합니다. 모델 서버를 호출하지 않고 이미 저장된 벡터를 읽습니다. 자동 갱신은 없으며 **새로고침**으로 최신 상태를 확인합니다.

관련 코드: `internal/kb/vector_map.go`(권한·조회·투영), `internal/kb/web/vectors.js`(시각화), `internal/kb/vector_map_test.go`(기하·권한·응답 상한·웹 인증 검증).

## 작업 전후 지식 활용

`start_task`로 관련 자료와 시작 시점의 문서 버전을 모으고, 작업 후 `finish_task`로 히스토리와 추가 기획·결정을 함께 저장합니다. 같은 입력 재시도는 중복 기록을 만들지 않습니다. 기획·결정은 기본 초안이고, 원문 저장과 의미 검색 준비는 구분됩니다.

[호출 예시·작성 양식·AI 클라이언트 적용 규칙](docs/task-workflow.md)을 참고하세요. 작업 후에는 `finish_task`의 `used_sources`로 실제 사용한 근거를 남기면 `get_retrieval_metrics`로 검색 품질을 측정할 수 있습니다. `pull_requests`에 PR 링크를 남기면 서버가 GitHub에서 병합·배포를 확인해 history 제목 아래에 상태 갱신 블록을 덧붙입니다(작성 시점 본문은 그대로, [PR 병합·배포 기록](docs/task-workflow.md#pr-병합배포-기록)). 결정 후보·유사 결정 제안, 재확인 담당자, 오래 열린 작업 정리, 용어집은 [지식 품질 안내](docs/knowledge-quality.md)를 참고하세요. 이 저장소의 `AGENTS.md`에도 시작 조회·완료 저장 규칙을 반영했습니다. 다른 프로젝트에는 해당 클라이언트 지침에 규칙을 추가해야 하며 MCP 서버가 작업 시작·종료를 자동으로 감지하지는 않습니다. 새 도구는 새 버전과 마이그레이션을 배포한 후 제공됩니다.

## 첨부 파일

내부 화면 **첨부 문서**에서 버킷 연결 상태·마지막 확인 시각·연결 재확인과 프로젝트별 파일 업로드·미리보기(이미지·텍스트·PDF·HTML, Word·Excel은 추출 본문)·다운로드·삭제와 지식 문서 연결을 지원합니다. MCP는 `prepare_attachment_upload`, `upload_attachment_chunk`, `list_attachments`, `get_attachment_usage`, `get_attachment_download`, `reextract_attachment`, `delete_attachment`를 제공합니다. 프로젝트별 총량(기본 1 GiB), 실행 파일 차단, 선택적 ClamAV 검사가 적용됩니다. 업로드 본문은 HTTP PUT으로 전송합니다. MCP 키로 예약하면 1시간 유효한 1회용 `upload_url`을 함께 받아 키 원문 없이 `curl -T`로 올릴 수 있습니다(`007_attachment_upload_tokens.sql` 필요). 네트워크가 `upload_url`에 닿지 않는 클라이언트는 `upload_attachment_chunk`로 512 KiB 이하 base64 조각을 MCP로 보낼 수 있습니다(`017_attachment_chunks.sql` 필요). `.pdf`·`.docx`·`.xlsx` 파일은 저장 직후 별도 자식 프로세스에서 본문을 추출해 `첨부 본문: <파일명>` artifact 문서로 만들고 기존 청크·임베딩·검색 경로로 색인합니다(`018_attachment_text.sql` 필요, `ATTACHMENT_TEXT_EXTRACTION=false`로 끔). 기능 도입 전에 올렸거나 추출에 실패한 파일은 `reextract_attachment`로 다시 추출합니다. 사용자가 첨부한 파일이나 엑셀 정리 결과를 AI가 버킷에 보관하는 절차는 `AGENTS.md`의 "첨부 파일과 산출 파일 보관"을 따릅니다. 기존 프로젝트 권한·키 범위·읽기 전용 제한과 감사 기록을 그대로 적용합니다. 버킷 연결 설정, 실패 복구와 검색 제한은 [첨부 안내](docs/attachments.md)를 참고하세요.
