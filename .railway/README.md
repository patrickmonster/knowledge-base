# Railway 설치와 운영

현재 서버는 `production` / `main`을 사용합니다. 기존 `development`의 앱·DB·Ollama 서버는 제거했습니다. 분리돼 남아 있던 DB 볼륨은 데이터를 백업한 뒤 삭제했고, 모델 볼륨은 `ollama-start.sh`가 다시 내려받을 수 있어 백업 없이 삭제했습니다. 따라서 development 볼륨 보관 비용은 더 이상 발생하지 않습니다. 배포 설정은 폐기한 `development`에 다시 적용하지 못하도록 차단합니다. 로컬 Compose와 테스트 DB는 계속 사용할 수 있습니다.

## 나중에 별도 프로젝트 설치하기

아래 설정 생성은 로컬 파일만 만듭니다. Railway 프로젝트 생성·배포·결제 리소스 추가는 하지 않습니다. 현재 설치와 별개인 **새 체크아웃**에서 실행하세요. 기존 `deployment.local.json`은 덮어쓰지 않습니다.

```sh
npm ci --prefix .railway
npm --prefix .railway run setup -- --project your-team-knowledge --repo your-org/your-repository
npm --prefix .railway run check
npm --prefix .railway test
```

프로젝트 이름과 GitHub 저장소를 바꾸면 앱·PostgreSQL·Ollama·모델 볼륨·첨부 버킷 이름이 포함된 `.railway/deployment.local.json`이 생성됩니다. 기본 환경은 `production`, 브랜치는 `main`, 버킷 지역은 `sjc`입니다. `--environment staging`, `--region sin`처럼 변경할 수 있습니다. `--clamav`를 붙이면 업로드 악성 파일 검사용 ClamAV 서비스(`ClamAV`, 볼륨 `clamav-signatures`)도 포함합니다(메모리 약 1.5 GiB 추가). 버킷 지역은 생성 후 바꿀 수 없습니다. 생성기는 이름과 지역을 검사하며 실제 리소스 생성 여부나 계정 권한까지 검사하지 않습니다.

**실제 설치가 필요해졌을 때만** Railway에서 새 프로젝트를 만든 후 다음 절차를 진행합니다. Node.js 22 이상과 Railway CLI 5.74.0 이상을 사용합니다.

```sh
railway login
railway link
# 새 프로젝트와 production 환경을 선택했는지 확인
railway environment production
railway config plan
# 계획에서 프로젝트·리소스와 삭제/재생성 항목 검토 후 실행
railway config apply
```

설정 파일의 프로젝트 이름은 선택한 새 프로젝트 이름과 맞아야 합니다. 앱의 GitHub 저장소에 Railway 설치 권한이 필요합니다. 새 프로젝트에는 기존 지식·계정·접속 키가 복사되지 않습니다. 이미 운영 중인 프로젝트를 복제하려면 DB와 버킷 데이터를 별도로 이관해야 합니다.

설정 적용 후 앱 서비스에 Railway 공개 도메인을 생성합니다. `PUBLIC_URL`은 그 도메인을 참조합니다. DB·Ollama·ClamAV에는 공개 도메인이나 TCP 프록시를 만들지 않습니다. 앱 상태 확인에 실패하면 도메인 생성 여부, DB 마이그레이션, 환경변수 참조를 확인합니다.

## 최초 관리자와 연결

새 앱의 실행 컨테이너 셸에 `BOOTSTRAP_EMAIL`, `BOOTSTRAP_NAME`, `BOOTSTRAP_PASSWORD`를 일시적으로 공급하고 `/app/knowledge-base bootstrap`을 한 번 실행합니다. 비밀번호는 12~128자이며 코드·채팅·셸 기록에 남기지 마세요. 이미 관리자가 있으면 다시 생성하지 않습니다. 초기 설정 후 임시 변수를 제거합니다. bootstrap을 매 배포 명령에 넣지 않습니다.

1. 공개 HTTPS 주소에서 로그인합니다.
2. 프로젝트를 만들고 참여자 권한을 부여합니다.
3. **MCP 접속 키**에서 필요한 프로젝트 범위와 읽기/쓰기 권한으로 키를 발급합니다.
4. 같은 화면의 연결 주소와 클라이언트 설정 예시를 사용합니다.
5. **첨부 문서**에서 작은 파일의 업로드·다운로드·삭제를 검사합니다.
6. Ollama 모델 다운로드가 끝난 뒤 문서 임베딩과 의미 검색을 확인합니다.

## 첨부 버킷 연결

[Railway Buckets](https://docs.railway.com/storage-buckets)는 비공개 저장소이며 S3 호환 API로 통신합니다. AWS 계정이나 AWS S3 버킷은 만들지 않습니다. 공식 Go S3 SDK는 Railway 엔드포인트에만 연결합니다.

배포 설정은 `Attachments` 버킷을 만들고 아래 값을 앱에 참조로 연결합니다. 실제 키를 복사하거나 Git에 기록하지 않습니다. API용 이름은 표시 이름과 다르므로 반드시 `.BUCKET`을 참조합니다.

| 앱 변수 | 같은 환경 버킷의 참조 |
| --- | --- |
| `BUCKET_ENDPOINT` | `${{Attachments.ENDPOINT}}` |
| `BUCKET_NAME` | `${{Attachments.BUCKET}}` |
| `BUCKET_REGION` | `${{Attachments.REGION}}` |
| `BUCKET_ACCESS_KEY_ID` | `${{Attachments.ACCESS_KEY_ID}}` |
| `BUCKET_SECRET_ACCESS_KEY` | `${{Attachments.SECRET_ACCESS_KEY}}` |
| `BUCKET_FORCE_PATH_STYLE` | `false` (기존 버킷은 Credentials의 URL style 확인) |

`deployment.local.json`의 `attachments.name`을 바꾸면 참조도 해당 이름으로 바뀝니다. 다섯 연결 값이 모두 비어 있으면 첨부 저장만 비활성화됩니다. 일부만 설정되거나 HTTPS 엔드포인트가 잘못되면 서버가 시작하지 않습니다. 기존 버킷이 path 스타일이면 로컬 JSON의 `attachments.forcePathStyle`을 `true`로 설정합니다.

업로드는 앱을 경유하므로 버킷 CORS 설정이 필요 없습니다. 다운로드는 권한 확인 후 5분 유효 링크를 발급합니다. DB와 버킷은 함께 백업·복구해야 합니다. DB 백업만으로 파일 원본은 복원되지 않습니다. 자세한 사용·제약은 [첨부 파일 안내](../docs/attachments.md)를 확인하세요.

## 배포 설정

| 항목 | 값 |
| --- | --- |
| DB | PostgreSQL 18 + pgvector (`postgres` Railway 템플릿) |
| 앱 | 루트 Dockerfile, `/app/knowledge-base serve` |
| 배포 전 | `/app/knowledge-base migrate`, 최대 300초 |
| 상태 확인 | `/healthz`, 최대 60초; DB 연결만 검사 |
| 배포 확인 | `/version`: 배포 커밋(`commit`)과 마이그레이션 적용 여부(`schema_version`/`schema_expected`) |
| 인스턴스 | 앱·Ollama 각각 1개, 자동 수면 꺼짐 |
| `APP_ENV` | `production` (임의 환경명도 HTTPS 설정 적용) |
| `DATABASE_URL` | 같은 환경 DB 서비스의 변수 참조 |
| `PUBLIC_URL` | `https://${{RAILWAY_PUBLIC_DOMAIN}}` |
| `PORT` / `TRUST_PROXY` | `8080` / `true` |
| 임베딩 | 같은 환경 내부 Ollama, `bge-m3`, 4 GiB 모델 볼륨 |
| 파일 | 같은 환경의 Railway 버킷, DB에 연결 정보·SHA-256 저장 |
| 악성 파일 검사 (선택) | `clamav` 설정 시 `clamav/clamav:1.4_base` 서비스 + 1 GiB 시그니처 볼륨, 앱 `CLAMAV_ADDRESS`=내부 주소:3310. 기존 운영 환경은 `deployment.local.json`에 `clamav`를 추가한 뒤 plan/apply해야 생깁니다. 자세한 내용은 [첨부 안내](../docs/attachments.md#clamav-서비스-구성) |

`ollama-start.sh`는 모델이 없을 때 다운로드합니다. `/api/tags`는 모델의 실제 추론까지 검증하지 않습니다. CPU 첫 요청은 모델 로딩 시간이 추가될 수 있습니다. DB 자동 백업, 버킷 백업과 GitHub 브랜치 보호는 별도 운영 설정입니다.

현재 앱은 `main`의 CI 통과 후 배포됩니다. 인프라 코드는 푸시만으로 자동 적용되지 않으므로 해당 환경에서 `railway config plan` / `apply`를 별도로 수행합니다. DB 변경은 새 마이그레이션으로 추가하고 이전 앱과 호환되게 유지합니다. 앱 롤백은 DB 스키마를 되돌리지 않습니다.

## 공개 코드와 비공개 값

실제 이름·ID·도메인은 Git과 Docker 빌드에서 제외한 `.railway/deployment.local.json`에 둡니다. 예시 파일에는 실서비스 정보를 넣지 않습니다. DB 비밀번호와 버킷 키는 Railway 변수에, 최초 로그인 정보 `.env.railway-access.json`은 로컬에만 보관합니다. 공개 주소는 인증 수단이 아니며 세션 또는 MCP 키와 프로젝트 권한이 필요합니다.

## 검증

```sh
go vet ./...
TEST_DATABASE_URL='<test-only PostgreSQL URL>' go test -race ./...
npm --prefix .railway run check
npm --prefix .railway test
docker build -t knowledge-base:check .
```

통합 테스트에는 운영 DB를 지정하지 않습니다. 설정 생성·타입 검사 통과가 실제 다른 계정의 신규 배포 성공을 의미하지는 않습니다. 새 프로젝트 실배포는 필요할 때 위 절차로 검증합니다.
