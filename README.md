# 팀 공용 지식베이스 MCP

Go로 만든 팀 공용 지식 서버입니다. 프로젝트별 기획·결정·작업 히스토리를 MCP로 저장하고 검색합니다. 웹 관리 화면에서 이메일·비밀번호 로그인, 사용자 초대, 프로젝트 권한과 접속 키 관리, 감사 로그 조회를 제공합니다.

## 구현된 기능

- 이메일·비밀번호 로그인, 로그아웃, 비밀번호 변경
- 관리자 초대·비밀번호 재설정 링크: 24시간 유효, 1회 사용, 직접 전달 방식
- 전역 관리자 / 일반 사용자, 프로젝트 소유자 / 편집자 / 조회자 구분
- 사용자 비활성화, 전역 역할 변경, 프로젝트 참여자 역할 변경·제외
- MCP 키 발급·폐기: 조회 전용 / 편집 가능, 특정 프로젝트 제한, 1~90일 만료
- 감사 로그: 작업자, 작업 이름, 프로젝트·대상 ID, 권한 변경 값, 결과, 오류 코드, 소요 시간
- 운영 현황: 최근 24시간의 기록된 작업·차단·오류·로그인 실패와 활성 사용자·키 수
- 프로젝트·문서 추가·수정·조회, 원본 버전 보관, 동시 수정 충돌 방지
- Ollama 로컬 `bge-m3`로 1,024차원 임베딩 생성, pgvector 저장·의미 검색
- 한국어 키워드 검색과 의미 검색을 결합한 하이브리드 검색
- 문서 저장·수정 시 자동 임베딩 대기열, 실패 재시도·상태 조회·소유자 재색인
- 공식 Go MCP SDK 기반 Streamable HTTP `/mcp`
- Railway용 Dockerfile, 배포 전 마이그레이션, `/healthz`

Railway 개발·운영 프로젝트 리소스와 DB·Ollama를 생성했습니다. 앱 배포는 GitHub 저장소 지정과 공개 접근 범위 확인 후 진행합니다.

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

| 작업 | 소유자 | 편집자 | 조회자 |
| --- | --- | --- | --- |
| 문서·검색·과거 버전 조회 | 가능 | 가능 | 가능 |
| 문서 추가·수정·보관 처리 | 가능 | 가능 | 불가 |
| 참여자 역할 변경·제외 | 가능 | 불가 | 불가 |

- 모든 활성 사용자는 프로젝트를 생성할 수 있고, 생성자가 소유자가 됩니다.
- 전역 관리자는 사용자 초대·비활성화·역할 변경, 팀 키 폐기, 전체 감사 로그·운영 통계에 접근합니다.
- **전역 관리자도 참여하지 않은 프로젝트 문서를 자동으로 읽을 수 없습니다.** 프로젝트 자료 접근은 별도로 검사합니다.
- 소유자 변경은 현재 제공하지 않습니다. 소유자를 비활성화하면 다른 편집자는 기존 권한을 유지하지만 참여자 관리를 위해 소유자를 다시 활성화해야 합니다.
- 자기 자신의 관리자 역할이나 활성 상태를 변경할 수 없습니다. 다른 관리자가 처리해야 합니다.
- MCP 키 범위와 현재 프로젝트 권한을 모두 만족해야 요청이 허용됩니다. 키로 웹 관리자 API에 접근할 수 없습니다.
- 계정 비활성화 또는 전역 역할 변경 시 기존 세션·키·초대 링크를 무효화합니다. 재활성화해도 이전 접속 자격은 살아나지 않습니다.
- 비밀번호 변경·재설정 시 모든 기존 세션·키를 무효화합니다. 다시 로그인해서 새 키를 발급해야 합니다.
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

마지막 bootstrap 명령은 관리자가 아직 없을 때만 성공합니다. 모델은 최초 한 번 내려받아 전용 볼륨에 보관합니다. 이후 `.env`에서 `BOOTSTRAP_PASSWORD`를 제거합니다. [http://localhost:8080](http://localhost:8080)에서 설정한 계정으로 로그인합니다. 일반 앱 컨테이너에는 bootstrap 비밀번호를 전달하지 않습니다.

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
4. **프로젝트**에서 프로젝트를 생성하고, 참여자의 사용자 ID로 편집자·조회자 권한을 부여합니다. 사용자 ID는 **내 계정** 또는 관리자 사용자 목록에서 확인합니다.
5. **MCP 접속 키**에서 작업·프로젝트 범위와 만료일을 선택합니다. 키 원문은 발급 직후 한 번만 표시됩니다.
6. AI 클라이언트에 아래 정보를 등록합니다.

```text
URL: https://<your-domain>/mcp
Authorization: Bearer <발급받은 MCP 키>
```

클라이언트는 Streamable HTTP와 직접 설정하는 Authorization 헤더를 지원해야 합니다. 웹 로그인 세션 자체를 MCP 자격 증명으로 사용하지 않습니다. OAuth 연결만 지원하는 클라이언트는 현재 지원하지 않습니다.

## MCP 도구

| 도구 | 역할 |
| --- | --- |
| `whoami` | 인증된 사용자 확인 |
| `create_project`, `list_projects` | 프로젝트 생성·조회 |
| `set_project_member`, `list_project_members` | 참여자 권한 관리 |
| `create_document`, `update_document` | 지식 추가·버전 기반 수정 |
| `get_document`, `list_documents` | 본문·목록 조회 |
| `get_document_history` | 최근 50개 수정 버전의 작성자·이유 조회 |
| `search_knowledge` | 프로젝트 내 하이브리드·의미·키워드 검색 |
| `get_embedding_status` | 프로젝트별 임베딩 대기·처리·완료·실패 건수 조회 |
| `reindex_project_embeddings` | 소유자 전용: 벡터 초기화 후 재생성 예약 |
| `get_task_context` | 작업에 필요한 발췌문·출처·최근 결정 탐색 정보 구성 |

문서 종류는 `brief`, `decision`, `history`, `artifact`, `reference`입니다. 상태는 `draft`, `active`, `archived`이고 기본 검색에는 활성 문서의 최신 버전만 포함됩니다. `update_document`는 `expected_revision`과 수정 이유 `reason`이 필요합니다. 충돌하면 최신 내용을 읽고 변경을 병합해야 합니다.

원본 문서는 최대 100,000자입니다. 작업 맥락은 기본 8,000자의 발췌문 예산을 사용하며 메타데이터는 예산에서 제외합니다. 기본 검색은 의미·키워드 검색을 결합합니다. 모델의 의미 판단과 검색 결과는 실제 프로젝트 자료로 평가해야 합니다. 작업물은 본문·링크로 저장하며 파일 업로드는 아직 지원하지 않습니다.

## 로컬 임베딩 동작

문서 저장 → 청크 생성·대기열 저장 → Go 백그라운드 작업자 → Ollama `/api/embed` → pgvector `vector(1024)` 저장 순서입니다. 검색할 때도 같은 모델로 질문을 임베딩하고, 접근 가능한 프로젝트의 최신 문서에서 코사인 유사도를 계산합니다. 기본 `hybrid` 모드는 키워드 결과와 RRF로 순위를 합칩니다.

- 저장 요청은 원문·수정 이력·청크·대기열을 하나의 트랜잭션으로 보관합니다. 임베딩은 비동기이므로 완료 전에는 키워드 검색만 가능합니다. 기존 문서도 마이그레이션 후 자동 대기열에 들어갑니다.
- 문서를 수정하면 새 청크 ID를 발급합니다. 이전 작업이 뒤늦게 완료되어도 새 내용에 오래된 벡터를 붙이지 않습니다. 과거 버전은 보관하지만 의미 검색은 현재 버전을 대상으로 합니다.
- 모델 호출은 최대 8개 청크를 묶어서 처리하며 문서 저장 트랜잭션 밖에서 실행합니다. DB 작업 임대로 여러 앱 인스턴스가 중복 처리하지 않도록 합니다.
- 실패하면 30초부터 간격을 늘려 최대 5회 시도합니다. 원문은 유지합니다. `get_embedding_status`의 오류 코드와 `retries_exhausted`를 확인하고 모델 복구 후 소유자가 `reindex_project_embeddings`를 호출할 수 있습니다.
- 검색 질문 임베딩은 최대 20초 기다립니다. `hybrid`에서 실패하면 `retrieval: "lexical-fallback"`과 `warning`을 반환합니다. `semantic`은 실패를 오류로 반환합니다. 권한 검사를 통과한 요청만 모델을 호출합니다.
- 현재 코사인 검색은 프로젝트 범위를 제한한 정확 검색입니다. 대규모 자료용 근사 검색 인덱스(HNSW)는 아직 없으므로 데이터 증가 시 검색 시간을 측정해야 합니다.

| 변수 | 기본값 / 의미 |
| --- | --- |
| `OLLAMA_BASE_URL` | `http://localhost:11434`; Compose는 `http://ollama:11434`로 지정 |
| `EMBEDDING_MODEL` | `bge-m3`; 현재 DB는 1,024차원 모델만 허용 |
| `EMBEDDING_ENABLED` | `true`; `false`이면 생성 작업을 멈추고 기본 검색을 키워드 방식으로 수행 |
| `OLLAMA_NO_CLOUD` | Ollama 서비스에 `true` 지정; 클라우드 모델 비활성화 |

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

`mode`는 `hybrid`, `semantic`, `lexical` 중 선택합니다. `warning`의 실제 메시지는 영어입니다. 검색 점수는 모드마다 다르며 확률이나 신뢰도 백분율이 아닙니다.

모델 준비 후 MCP에서 문서를 저장하고 `get_embedding_status`의 `ready` 증가를 확인한 다음, `search_knowledge`에 `mode: "semantic"`을 지정해 검사합니다. `/healthz`는 DB 연결 상태만 검사하며 모델 준비 여부를 보장하지 않습니다.

참고: [Ollama 임베딩 API](https://docs.ollama.com/api/embed), [bge-m3 모델](https://ollama.com/library/bge-m3), [Ollama 로컬 설정](https://docs.ollama.com/faq), [pgvector](https://github.com/pgvector/pgvector).

## Railway 배포

개발은 `development` / `develop`, 운영은 `production` / `main`으로 분리합니다. 각 환경에 앱과 PostgreSQL을 따로 만들고, `DATABASE_URL`은 **같은 환경의 Postgres 서비스 참조**로 주입합니다. 비밀번호를 코드에 기록하지 않습니다.

[개발·운영 배포 절차](.railway/README.md)와 [실제 배포 설정](.railway/railway.ts)을 사용합니다. Docker 빌드, 배포 전 마이그레이션, `/healthz`, 재시작 정책, GitHub 브랜치 연결 설정이 포함되어 있습니다. GitHub 자동 배포는 설정 파일의 `repository`에 실제 저장소를 지정한 뒤 두 환경에 적용해야 활성화됩니다.

새 배포는 마이그레이션과 상태 확인을 통과해야 합니다. 앱 롤백이 DB를 되돌리지는 않으므로 스키마 변경은 이전 앱과 호환되게 추가합니다. 개발·운영의 최초 관리자도 각각 한 번 생성합니다.

[Railway 프로젝트](https://railway.com/project/ee74d312-3e76-4908-b039-c290ce21d064)의 개발·운영 환경과 독립 DB·Ollama, 앱 배포 설정을 생성했습니다. 앱 소스 연결과 공개 도메인 생성은 아직 완료하지 않았으며, 실제 앱 배포·최초 관리자 생성·접속 검증이 남아 있습니다. 현재 상태와 서비스 이름은 [배포 기록](.railway/README.md)을 참고합니다.

## 감사·운영 범위

- 성공한 업무 변경과 감사 기록을 같은 트랜잭션에 저장합니다. 감사 저장이 실패하면 해당 변경도 되돌립니다.
- 업무 권한 거부, 로그인 실패, MCP 인증 실패를 기록합니다. DB 장애 때는 감사 저장 자체가 실패할 수 있습니다.
- 비밀번호·키 원문·초대 토큰·문서 본문을 감사 로그에 저장하지 않습니다. 사용자 ID, 문서 ID 등 메타데이터는 포함됩니다.
- 감사 로그는 애플리케이션 API로 수정·삭제할 수 없지만, DB 운영자의 변경까지 막는 별도 WORM 저장소는 아닙니다.
- 관리자 통계는 **기록된 업무·인증 이벤트**를 집계합니다. 모든 HTTP 요청, 정적 파일 조회, 프로토콜 검증 실패를 포함하는 네트워크 지표는 아닙니다.
- 화면은 수동 새로고침 방식입니다. 외부 알림, 자동 이상 탐지, 이메일 발송, MFA, SSO는 아직 제공하지 않습니다.
- 사용자·키·프로젝트 참여자 관리 목록은 현재 최대 200개, 프로젝트 목록은 페이지당 최대 50개입니다. 감사 로그는 ID 커서로 100개씩 조회합니다.
- 로그인은 이메일당 15분에 10회, IP당 15분에 50회로 제한합니다. 비밀번호 변경은 사용자당 15분에 10회, 일반 인증 요청은 사용자당 분당 240회입니다. 웹 세션은 12시간 후 만료됩니다.
- 만료된 세션과 요청 제한 버킷은 주기적으로 정리합니다. 감사 로그와 문서 버전은 자동 삭제하지 않습니다. 운영 규모에 맞춘 보관 정책이 필요합니다.

## 검증

```sh
go vet ./...
go test ./...
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
- `internal/kb/http.go`, `web/`: 웹 API 및 관리 화면
- `internal/kb/tools.go`, `documents.go`, `search.go`: MCP와 지식 저장·검색
- `internal/kb/embeddings.go`: Ollama 호출과 벡터 응답 검증
- `internal/kb/embedding_worker.go`: 비동기 생성·pgvector 저장·재시도
- `internal/kb/vector_search.go`: 질문 임베딩·권한 범위 내 의미 검색·하이브리드 순위
- `migrations/003_embeddings.sql`: 벡터 컬럼과 작업 대기열
- `migrations/`: 버전 관리되는 DB 스키마

참고: [공식 MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk), [Railway PostgreSQL](https://docs.railway.com/databases/postgresql), [Railway 배포 전 명령](https://docs.railway.com/deployments/pre-deploy-command), [OWASP 비밀번호 저장](https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html).

## 내부 화면의 벡터 탐색

로그인 후 왼쪽 **벡터 탐색** 메뉴에서 프로젝트와 문서 상태를 선택합니다.

- 프로젝트 전체의 임베딩 완료·대기·처리·실패 현황을 확인합니다.
- 선택한 문서 상태에서 현재 모델로 생성된 벡터를 최대 500개까지 표시합니다. 500개를 넘으면 최근 수정한 문서 순으로 표시하며 전체 개수와 표시 범위를 안내합니다.
- 문서 종류별 색상, 제목·발췌문 필터, 확대·축소·이동을 제공합니다. 점이나 키보드로 접근 가능한 조각 목록을 선택하면 본문과 해당 버전의 원문을 확인할 수 있습니다.
- 유사한 조각 5개는 같은 프로젝트·문서 상태의 전체 준비된 벡터에서 실제 코사인 유사도로 조회합니다. 지도에 없는 조각도 상세 내용을 열 수 있습니다.

분포도는 L2 정규화한 벡터에 서버 측 PCA를 적용한 2차원 근사 표현입니다. 표시된 표본의 분산 중 얼마를 표현하는지 안내하며, 점 사이 거리 자체를 실제 유사도나 신뢰도로 해석하지 않습니다. 표본·문서 상태가 바뀌거나 새로고침하면 위치가 달라질 수 있고, 동일 벡터는 겹쳐 표시됩니다. 원본 벡터 배열은 브라우저에 전달하지 않습니다.

조회는 웹 로그인 세션과 CSRF 검사를 거치며, 관리자도 참여하지 않은 프로젝트의 벡터와 본문을 읽을 수 없습니다. 지도·유사 조각 조회 및 권한 거부는 감사 로그에 기록합니다. 모델 서버를 호출하지 않고 이미 저장된 벡터를 읽습니다. 자동 갱신은 없으며 **새로고침**으로 최신 상태를 확인합니다.

관련 코드: `internal/kb/vector_map.go`(권한·조회·투영), `internal/kb/web/vectors.js`(시각화), `internal/kb/vector_map_test.go`(기하·권한·응답 상한·웹 인증 검증).
