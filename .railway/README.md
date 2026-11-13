# Railway 개발·운영 배포

2026-11-13에 [knowledge-base 프로젝트](https://railway.com/project/ee74d312-3e76-4908-b039-c290ce21d064)와 개발·운영 환경을 생성했습니다. 리소스 ID는 [deployment.json](deployment.json)에 기록했습니다.

- 생성·설정 완료: 환경별 DB, 앱 서비스, Ollama, 영구 볼륨, 내부 연결 변수, 배포 전 마이그레이션·상태 확인 설정.
- 앱 소스 연결 대기: GitHub 저장소와 `develop` / `main` 브랜치가 아직 지정되지 않았습니다. 앱 자체는 아직 배포되지 않았으므로 원격 DB의 앱 테이블도 아직 생성하지 않았습니다.
- 공개 도메인: 미생성. 공개할 환경의 사용자 승인 대기입니다.
- 최초 관리자 생성·앱 로그인·MCP 연결·원격 임베딩 요청: 앱 배포 후 검증합니다.
- DB 자동 백업 일정: 아직 설정하지 않았습니다.

## 환경 구성

| 대상 | Railway 환경 | GitHub 브랜치 | DB / 도메인 |
| --- | --- | --- | --- |
| 로컬 | 해당 없음 | 작업 브랜치 | Docker Compose 전용 |
| 개발 | `development` | `develop` | 개발 환경의 Postgres / 개발 도메인 |
| 운영 | `production` | `main` | 운영 환경의 Postgres / 운영 도메인 |

| 환경 | 앱 | DB | 임베딩 |
| --- | --- | --- | --- |
| production | `knowledge-base` | `Postgres` | `Ollama` |
| development | `knowledge-base-dev` | `Postgres-m91O` | `Ollama-QdV8` |

각 환경은 별도 서비스와 별도 볼륨을 사용합니다. DB 데이터·비밀번호·사용자·세션·MCP 키는 환경마다 분리됩니다. 운영 DB URL을 개발 환경에 복사하지 않습니다. Ollama는 공개 도메인 없이 내부 네트워크로만 연결합니다.

`railway.ts`는 현재 [Railway IaC](https://docs.railway.com/infrastructure-as-code) 형식입니다. 기존 `railway.json` / `railway.toml`은 신규 서비스에 사용하지 않습니다. 설정 파일 변경은 `railway config apply`로 별도 적용해야 하며 앱 코드 푸시만으로 인프라 설정이 갱신되지는 않습니다.

## 기존 프로젝트 연결 및 배포

Node.js 22 이상, [Railway CLI](https://docs.railway.com/cli) 5.74.0 이상과 Railway 로그인이 필요합니다. Node는 인프라 설정을 평가할 때만 사용하고 앱 이미지에는 포함하지 않습니다.

저장소 루트에서 실행합니다.

```sh
npm ci --prefix .railway
npm --prefix .railway run check
railway login
railway link --project ee74d312-3e76-4908-b039-c290ce21d064

railway environment development
railway config plan
railway config apply
# 공개 접근 승인을 받은 경우에만 도메인 생성
railway domain --service knowledge-base-dev --port 8080
railway up --service knowledge-base-dev --environment development

railway environment production
railway config plan
railway config apply
# 공개 접근 승인을 받은 경우에만 도메인 생성
railway domain --service knowledge-base --port 8080
railway up --service knowledge-base --environment production
```

프로젝트와 두 환경은 이미 있으므로 다시 생성하지 않습니다. 위 명령은 앱 소스와 공개 접근 범위가 결정된 뒤 실행합니다. 각 `plan`에서 프로젝트·환경·리소스를 확인합니다. 운영을 복제하지 않고 빈 개발 환경에 설정을 적용하므로 운영 데이터를 개발로 복사하지 않습니다.

생성된 도메인은 환경별로 다릅니다. 도메인을 만든 뒤 배포하며, `/healthz`가 HTTP 200과 `ok`를 반환하고 로그인 화면이 열리는지 확인합니다. 도메인이 없는 상태에서는 앱이 정상 기동하지 않습니다.

`ollama-start.sh`가 서버 기동 후 없는 모델을 자동으로 다운로드합니다. 재시작 시 저장된 모델을 재사용합니다. 로그의 `knowledge-base embedding model bge-m3 is ready` 메시지 이후 벡터 검색을 사용할 수 있으며 모델은 환경별 4 GiB 볼륨에 보관합니다. `/api/tags` 상태 확인은 서버 가용성만 확인하므로 `ollama list`에서 `bge-m3`도 확인합니다. CPU 기반 추론 성능·메모리 사용량은 실제 Railway 배포에서 확인해야 합니다.

## 적용하는 설정

| 항목 | 값 |
| --- | --- |
| DB | Railway PostgreSQL 18 + pgvector (`ghcr.io/railwayapp-templates/postgres-ssl:18`) |
| 빌드 | 루트 `Dockerfile` |
| 시작 | `/app/knowledge-base serve` |
| 배포 전 | `/app/knowledge-base migrate`, 최대 300초 |
| 상태 확인 | `/healthz`, 최대 60초 |
| 인스턴스 | 환경별 1개, 자동 수면 꺼짐 |
| 재시작 | 실패 시 최대 5회 |
| `APP_ENV` | 적용 대상 환경 이름 |
| `DATABASE_URL` | 운영 `${{Postgres.DATABASE_URL}}`, 개발 `${{Postgres-m91O.DATABASE_URL}}` |
| `PUBLIC_URL` | `https://${{RAILWAY_PUBLIC_DOMAIN}}` |
| `PORT` | `8080` |
| `TRUST_PROXY` | `true` |
| `OLLAMA_BASE_URL` | 운영 `Ollama`, 개발 `Ollama-QdV8`의 내부 도메인, 포트 11434 |
| `EMBEDDING_MODEL` / `EMBEDDING_ENABLED` | `bge-m3` / `true` |

DB 비밀번호를 코드나 `.env.example`에 넣지 않습니다. Railway가 생성한 자격 증명을 서비스 참조로 연결합니다. 앱은 내부 DB URL을 사용하며, DB 공개 TCP 프록시와 Ollama 공개 도메인은 생성하지 않았습니다. DB 볼륨은 환경별로 분리되어 있으며 자동 백업 일정은 별도 설정이 필요합니다.

벡터 검색 마이그레이션에는 `vector` 확장이 필요합니다. 원격은 pgvector를 포함한 [Railway 공식 이미지](https://github.com/railwayapp-templates/postgres-ssl)를 사용하고 볼륨은 `/var/lib/postgresql/data`에 연결합니다. 로컬 Compose와 CI는 `pgvector/pgvector:pg18`을 사용합니다. 일반 `postgres` 이미지로 바꾸면 마이그레이션이 실패할 수 있습니다.

커스텀 도메인을 쓰면 `railway.ts`의 `PUBLIC_URL`도 해당 HTTPS origin으로 변경합니다. 계정별 비밀키 같은 추가 변수를 IaC 밖에서 설정할 때는 이후 `plan`에서 삭제되지 않는지 확인하고, 필요하면 SDK의 `preserve()`로 선언합니다.

## GitHub 자동 배포

1. `.railway/railway.ts`의 `repository`에 실제 `owner/repository`를 지정합니다. 빈 값이면 로컬 `railway up` 배포용입니다.
2. GitHub 저장소에 코드와 `develop`, `main` 브랜치를 올리고 Railway GitHub App에 해당 저장소 접근을 허용합니다.
3. 두 환경에 각각 `railway config plan` / `railway config apply`를 실행합니다.
4. 개발은 `develop`, 운영은 `main`에 연결되고 GitHub 검사 완료를 기다리도록 설정됩니다.
5. 작업 브랜치 → `develop`에서 검증 → PR로 `main`에 반영합니다. 운영 브랜치 보호와 필수 `test` 검사는 GitHub에서 설정합니다.

DB 변경은 기존 마이그레이션 수정 대신 새 버전으로 추가하고 마이그레이션 목록에 등록합니다. 배포 전 명령이 실패하면 새 앱 배포가 중단됩니다. 마이그레이션 성공 뒤 앱 배포가 실패할 수 있으므로 이전 앱과 새 스키마가 함께 동작하도록 단계적으로 변경합니다. 앱 롤백은 DB 스키마를 되돌리지 않습니다.

## 최초 관리자

개발·운영에서 각각 한 번 생성합니다. 해당 환경의 실행 컨테이너에 `railway ssh --environment development --service knowledge-base-dev` 또는 운영 환경을 명시해 접속한 뒤, 그 셸에만 `BOOTSTRAP_EMAIL`, `BOOTSTRAP_NAME`, `BOOTSTRAP_PASSWORD`를 공급하고 `/app/knowledge-base bootstrap`을 실행합니다. 비밀번호는 12~128자이며 채팅·소스·셸 기록에 남기지 않는 방식으로 입력합니다.

이미 관리자가 존재하면 명령이 실패하도록 되어 있습니다. bootstrap을 매 배포 명령으로 등록하지 않습니다. 완료 후 임시 환경변수를 지우고 로그인·MCP 접속을 확인합니다.

## 배포 전 검증

```sh
go vet ./...
TEST_DATABASE_URL='<test-only PostgreSQL URL>' go test -race ./...
npm ci --prefix .railway
npm --prefix .railway run check
docker build -t knowledge-base:check .
```

테스트 DB에도 pgvector가 필요합니다. 통합 테스트는 임의의 전용 스키마를 만들고 지우므로 운영 DB를 지정하지 않습니다.
