# Railway 개발·운영 배포

개발은 `development` / `develop`, 운영은 `production` / `main`으로 분리합니다. 각 환경은 별도 앱·PostgreSQL·Ollama와 영구 볼륨을 사용합니다. DB 데이터·비밀번호·사용자·세션·MCP 키도 환경별로 분리합니다.

## 공개 코드와 실제 배포 정보

- 공개 저장소: 앱 코드, Dockerfile, 일반적인 배포 절차, 값이 비어 있거나 예시인 설정.
- 로컬 전용: `.railway/deployment.local.json`의 실제 프로젝트·서비스 이름, 리소스 ID, 도메인. Git과 Docker 빌드에서 제외합니다.
- 비밀정보: DB 비밀번호와 토큰은 Railway 변수 또는 별도 비밀정보 저장소에 보관합니다. 배포 설정 JSON에도 비밀번호를 넣지 않습니다.
- 최초 로그인 정보 파일 `.env.railway-access.json`도 로컬 전용이며 Git과 Docker 빌드에서 제외합니다.

공개 HTTPS 주소, `/api`와 `/mcp` 경로는 인증 수단이 아닙니다. 자료는 세션 또는 MCP 키 인증으로 보호하며 DB와 Ollama에는 공개 도메인이나 TCP 프록시를 만들지 않습니다. `.gitignore`는 이미 커밋된 기록을 지우지 않습니다.

## 로컬 배포 설정 준비

Node.js 22 이상과 Railway CLI 5.74.0 이상을 사용합니다. Node는 인프라 설정 평가에만 필요합니다.

```sh
npm ci --prefix .railway
npm --prefix .railway run check
# 처음 사용하는 컴퓨터에서만 복사합니다. 기존 로컬 설정을 덮어쓰지 않습니다.
cp -n .railway/deployment.example.json .railway/deployment.local.json
railway login
railway link
```

로컬 JSON에 실제 저장소, 프로젝트 이름, 환경별 앱·DB·Ollama·모델 볼륨 이름과 배포 브랜치를 입력합니다. 기존 프로젝트에서는 Railway 화면에 표시된 이름을 정확히 사용합니다. 예시 이름을 그대로 적용하면 별도 리소스가 생길 수 있습니다. 이 컴퓨터의 기존 실제 설정은 로컬 JSON에 보존되어 있습니다.

`railway.ts`는 로컬 설정이 없거나 필수 값이 빠지면 실패합니다. [Railway IaC](https://docs.railway.com/infrastructure-as-code) 설정 변경은 대상 환경에서 계획을 검토한 뒤 별도로 적용합니다.

```sh
railway environment development
railway config plan
railway config apply
# 운영 반영 시 같은 절차에서 production을 선택합니다.
```

프로젝트·환경·리소스의 삭제 또는 재생성이 계획에 포함되면 적용 전에 원인을 확인합니다. 앱 코드 푸시만으로 인프라 설정이 변경되지는 않습니다.

## 앱 반영

각 앱 서비스는 로컬 설정에 지정한 GitHub 브랜치를 추적하며 `Wait for CI`를 사용합니다. 일반적인 흐름은 작업 브랜치 → `develop`에서 검증 → PR로 `main`에 반영입니다.

```sh
# develop 브랜치에서 개발 환경에 반영
git push origin develop
# 검증한 변경을 main에 병합한 뒤 운영에 반영
git push origin main
```

직접 업로드할 때는 `railway up --environment development --service <개발-앱-이름>`처럼 환경과 서비스를 명시합니다. 직접 업로드는 GitHub 검사 대기를 거치지 않으므로 먼저 테스트합니다. 배포 뒤 `/healthz`가 HTTP 200을 반환하는지, 로그인과 MCP 인증이 동작하는지 확인합니다.

## 적용하는 설정

| 항목 | 값 |
| --- | --- |
| DB | PostgreSQL 18 + pgvector (`ghcr.io/railwayapp-templates/postgres-ssl:18`) |
| 빌드 | 루트 `Dockerfile` |
| 시작 | `/app/knowledge-base serve` |
| 배포 전 | `/app/knowledge-base migrate`, 최대 300초 |
| 상태 확인 | `/healthz`, 최대 60초 |
| 인스턴스 | 환경별 1개, 자동 수면 꺼짐 |
| 재시작 | 실패 시 최대 5회 |
| `APP_ENV` | 대상 환경 이름 |
| `DATABASE_URL` | 같은 환경 DB 서비스의 변수 참조 |
| `PUBLIC_URL` | Railway 공개 HTTPS 도메인 참조 |
| `PORT` / `TRUST_PROXY` | `8080` / `true` |
| `OLLAMA_BASE_URL` | 같은 환경 Ollama의 내부 도메인, 포트 11434 |
| `EMBEDDING_MODEL` / `EMBEDDING_ENABLED` | `bge-m3` / `true` |

원격 DB 볼륨은 `/var/lib/postgresql/data`에 연결합니다. 로컬 Compose와 CI도 pgvector를 포함한 이미지를 사용합니다. DB 자동 백업 일정과 GitHub 운영 브랜치 보호 규칙은 별도 설정 대상입니다.

`ollama-start.sh`는 모델이 없을 때 다운로드하고, 환경별 4 GiB 볼륨에 저장해 재사용합니다. `/api/tags`는 서버 가용성만 확인합니다. 모델 다운로드 완료 뒤 실제 임베딩 요청도 확인합니다. CPU 추론의 첫 요청은 모델을 메모리에 올리는 시간이 추가됩니다.

DB 변경은 기존 마이그레이션 수정 대신 새 버전으로 추가합니다. 배포 전 명령이 실패하면 새 앱 배포가 중단되지만, 앱 롤백이 DB 스키마를 되돌리지는 않습니다. 이전 앱과 새 스키마가 함께 동작하도록 변경합니다.

## 최초 관리자

이미 관리자가 있는 환경에서는 다시 생성하지 않습니다. 새 환경을 만들 때 해당 앱의 실행 컨테이너에 연결하고, 그 셸에만 `BOOTSTRAP_EMAIL`, `BOOTSTRAP_NAME`, `BOOTSTRAP_PASSWORD`를 공급해 `/app/knowledge-base bootstrap`을 한 번 실행합니다. 비밀번호는 12~128자이며 채팅·소스·셸 기록에 남기지 않는 방식으로 입력합니다.

bootstrap을 매 배포 명령으로 등록하지 않습니다. 초기 설정 뒤 임시 변수와 접속키를 정리합니다.

## 검증

```sh
go vet ./...
TEST_DATABASE_URL='<test-only PostgreSQL URL>' go test -race ./...
npm --prefix .railway run check
docker build -t knowledge-base:check .
```

통합 테스트에는 운영 DB를 지정하지 않습니다. GitHub 검사는 코드·DB 통합 테스트와 함께 비밀정보 및 로컬 전용 파일의 추적 여부를 검사합니다.
