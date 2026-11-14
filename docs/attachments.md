# Railway 버킷 첨부 문서

파일 원본은 비공개 Railway 버킷에 저장합니다. PostgreSQL에는 프로젝트·문서 연결, 원래 파일명, 크기, SHA-256, 작성자, 저장 상태를 기록합니다. 최대 25 MiB이며 빈 파일은 받지 않습니다. UI는 편의상 25MB로 표시합니다.

## 화면에서 사용

**첨부 문서** 상단에서 버킷 연결 상태와 마지막 확인 시각을 확인합니다. `연결 다시 확인`은 파일을 만들거나 삭제하지 않고 서버의 버킷 접근 여부를 조회합니다. 연결 설정 없음·접근 실패·조회 실패를 구분하며 연결 확인 전에는 업로드를 비활성화합니다. 연결 확인은 파일 쓰기 성공을 보장하지 않으므로 업로드 후 목록의 `저장 완료`도 확인하세요. 버킷명·접속 주소·자격 증명은 화면에 노출하지 않습니다.

목록에는 이 서비스에서 해당 프로젝트에 등록한 첨부만 표시합니다. 버킷에 직접 업로드한 파일은 자동으로 가져오지 않습니다. 연결 오류가 있어도 기존 첨부 목록은 조회할 수 있습니다.

**첨부 문서** → 프로젝트 선택 → 파일 선택 → 업로드. 기존 지식 문서에 연결하려면 문서 ID를 입력합니다. 생략하면 프로젝트 첨부가 됩니다. 연결 당시 문서 revision을 함께 보관합니다. 원본 문서의 본문·버전은 수정하지 않습니다.

소유자·편집자는 추가·삭제할 수 있고 조회자는 목록·다운로드만 사용할 수 있습니다. 전체 관리자라도 프로젝트 참여 권한이 없으면 파일에 접근할 수 없습니다. 업로드를 예약한 사용자만 해당 예약을 완료할 수 있습니다.

## MCP 클라이언트에서 사용

1. `prepare_attachment_upload` 호출:

```json
{"project_id":"PROJECT_UUID","document_id":"OPTIONAL_DOCUMENT_UUID","filename":"기획서.pdf","size_bytes":12345}
```

응답의 `attachment.id`, `upload_url`, `upload_path`를 보관합니다. `document_id`는 생략할 수 있지만, 파일 본문은 검색되지 않으므로 내용을 요약한 지식 문서에 연결하는 것을 권장합니다. 예약은 1시간 유효합니다. 파일 내용이나 비밀키를 MCP JSON에 넣지 않습니다.

2. 원본 바이트를 PUT 합니다. 방법은 두 가지입니다.
   - **1회용 업로드 링크 (MCP 키로 예약한 경우):** 응답의 `upload_url`은 `PUBLIC_URL` 기준 절대 주소이며 이 파일 하나에만, 1시간 동안, 한 번만 쓸 수 있습니다. `Authorization` 헤더가 필요 없어서 키 원문을 읽을 수 없는 클라이언트(호스팅 커넥터, 샌드박스)도 올릴 수 있습니다.

     ```sh
     curl --fail-with-body -T 정리표.xlsx -H "Content-Type: application/octet-stream" "<upload_url>"
     ```

     서버에는 링크 토큰의 SHA-256만 저장하며 업로드가 시작되면 지웁니다. 예약한 키가 폐기·만료되거나 계정이 비활성화되면 링크도 무효입니다. 권한·업로더·크기 검사는 Bearer 업로드와 동일합니다. 링크는 응답·로그·지식 문서·커밋에 남기지 마세요. `Authorization` 헤더와 함께 보내면 거절합니다.
   - **Bearer 키:** `upload_path`(상대 경로)에 `Authorization: Bearer <예약에 사용한 사용자의 MCP 키>`로 보냅니다.

   두 방법 모두 `Content-Type: application/octet-stream`이 필요합니다. 웹 화면은 로그인 쿠키와 CSRF 토큰을 사용하며 업로드 링크를 받지 않습니다. 버킷 자격 증명은 반환하지 않습니다.
3. PUT 응답의 `status: ready`와 SHA-256을 확인합니다. 응답이 끊겼다면 `list_attachments`로 상태부터 확인하세요. 예약은 한 번만 사용되며 같은 ID에 다시 PUT해서 파일을 덮어쓸 수 없습니다. 실패한 경우 새 예약을 생성합니다. `prepare_attachment_upload` 자체는 멱등 호출이 아니므로 응답이 불확실할 때 목록부터 확인하세요.
4. `list_attachments`로 조회합니다. `project_id`, 선택적 `document_id`, `limit`(최대 100), `offset`을 지원합니다.
5. `get_attachment_download`에 `project_id`, `attachment_id`를 전달하면 5분 유효 다운로드 링크를 받습니다.
6. `delete_attachment`에 같은 식별자를 전달하면 접근을 차단하고 버킷 삭제 대기 상태로 바꿉니다. 진행 중인 업로드는 삭제할 수 없습니다.

## 저장과 권한

버킷 키는 서버가 만든 UUID 경로로 정하며 사용자 파일명을 경로로 사용하지 않습니다. 파일은 서버 임시 파일에 받아 길이를 확인한 후 전송하며 정상 종료 시 임시 파일을 지웁니다. 최대 업로드 요청 시간은 120초입니다. 업로드 이후 권한을 다시 확인한 다음 저장 완료를 확정합니다. 그 사이 권한이 취소되면 다운로드 가능 상태로 전환하지 않습니다.

원본은 항상 `application/octet-stream`, `Content-Disposition: attachment`로 내려받습니다. HTML 등 능동 콘텐츠를 앱 화면 안에서 실행하지 않습니다. PDF 미리보기·OCR·본문 추출은 포함하지 않습니다(악성 파일 검사는 아래 "총량 제한과 악성 파일 검사" 참고). 검색할 내용은 별도 지식 문서로 작성하고 첨부와 연결합니다.

다운로드 링크는 유효 기간 동안 링크를 가진 사람이 사용할 수 있습니다. 발급 후 권한을 취소해도 최대 5분 동안 기존 링크가 남을 수 있습니다. 링크를 지식 본문이나 공개 기록에 저장하지 마세요. API 응답과 감사 로그에 버킷 키·비밀번호를 기록하지 않으며 감사 로그에는 파일 식별자와 작업 결과만 남깁니다.

## 실패 복구와 삭제

DB에 예약을 먼저 기록하고 버킷에 전송합니다. 파일 저장 후 서버가 중단되거나 응답을 잃어도 원본을 추적할 수 있습니다. 정리 작업은 매분 삭제 대기 파일과 1시간 이상 지난 미완료 업로드를 제거합니다. 버킷 삭제가 실패하면 DB 기록을 남겨 다음 실행에서 재시도합니다. 완료 파일은 이 정리 대상이 아닙니다. 버킷 연결을 해제하면 정리도 멈춥니다.

DB 트랜잭션과 버킷 PUT은 하나의 원자적 작업이 아닙니다. 중단된 파일은 위 정리 작업으로 수습합니다. 프로세스 강제 종료로 남은 운영체제 임시 파일은 컨테이너 재생성 시 정리됩니다. 서버 운영 시 임시 디스크 사용량을 관리하세요. 프로젝트 총량 제한과 악성 파일 검사는 아래 절을 따릅니다.

## 총량 제한과 악성 파일 검사

| 변수 | 기본값 / 의미 |
| --- | --- |
| `ATTACHMENT_PROJECT_QUOTA_MB` | `1024`. 프로젝트별 첨부 총량(MiB). `0`이면 제한 없음, 지정 시 25~1048576 |
| `CLAMAV_ADDRESS` | 비어 있으면 백신 검사 없음. `host:port`(예: Railway 내부 주소의 clamd 3310)를 지정하면 모든 업로드를 검사 |
| `ATTACHMENT_ALLOW_EXECUTABLES` | `false`. `true`면 아래 실행 파일 차단을 끔 |

- **총량**: 저장 완료 파일과 1시간 안의 진행 중 예약(`pending`·`uploading`)을 합산합니다. `prepare_attachment_upload`에서 초과하면 `QUOTA_EXCEEDED`(HTTP 413)입니다. 같은 프로젝트의 동시 예약은 프로젝트 행 잠금으로 순서대로 처리합니다. 사용량은 `get_attachment_usage`와 웹 첨부 목록에 표시됩니다.
- **실행 파일 차단(기본)**: `.exe .dll .scr .com .msi .msp .bat .cmd .ps1 .psm1 .vbs .vbe .jse .wsf .wsh .hta .lnk .pif .cpl .jar .apk .appx .msix .reg` 이름은 예약 단계에서, 이름을 바꾼 Windows PE·ELF·Mach-O 실행 파일은 업로드 본문 앞부분으로 거부합니다(`BLOCKED_FILE`, HTTP 422).
- **백신 검사(ClamAV)**: 앱이 임시 파일을 clamd `INSTREAM`으로 보내고, 깨끗하다는 응답을 받은 뒤에만 버킷에 저장합니다. 감염이면 `MALWARE_DETECTED`(422), clamd 연결 실패·오류·크기 초과면 `SCAN_UNAVAILABLE`(503)로 **저장하지 않습니다(fail closed)**. clamd의 `StreamMaxLength`는 26M 이상이어야 합니다(현재 clamd 기본값은 100M이며, 제공 설정은 26M으로 고정).
- 거부된 예약은 즉시 삭제 대기로 바뀌어 총량에서 빠집니다. 감사 로그 `attachment_rejected`에 사유와 백신 시그니처 이름만 남기며 파일명·내용은 남기지 않습니다.
- 검사는 새 업로드에만 적용됩니다. 기능 도입 전 저장된 파일은 다시 검사하지 않습니다.
- **검사 서비스 상태**: 첨부 화면의 연결 상태 카드와 `GET /api/attachments/storage`의 `scanner`가 clamd `PING` 결과를 `connected`·`not_configured`·`unavailable`로 보여 줍니다(주소는 응답하지 않음). `unavailable`이면 업로드 버튼이 비활성화됩니다.

### ClamAV 서비스 구성

| 환경 | 방법 |
| --- | --- |
| Railway | `.railway/deployment.local.json` 환경에 `"clamav": {"name": "ClamAV", "volumeName": "clamav-signatures"}`를 추가하거나 설정 생성기에 `--clamav`를 붙입니다. `railway config plan`/`apply` 시 clamd 서비스·1 GiB 시그니처 볼륨이 생기고 앱에 `CLAMAV_ADDRESS=${{ClamAV.RAILWAY_PRIVATE_DOMAIN}}:3310`이 연결됩니다. |
| 로컬 Compose | `.env`에 `CLAMAV_ADDRESS=clamav:3310`을 넣고 `docker compose --profile clamav up`. |

- 이미지 `clamav/clamav:1.4_base`(시그니처 미포함). 첫 시작 때 시그니처를 볼륨에 내려받은 뒤 clamd가 열리므로 **수 분간 `SCAN_UNAVAILABLE`** 입니다. 이후 재시작은 볼륨의 시그니처를 사용합니다.
- 설정은 이미지 진입점의 `CLAMD_CONF_<키>` 환경변수로 넣습니다: `TCPAddr ::`(Railway 내부망 IPv6), `StreamMaxLength 26M`, `ConcurrentDatabaseReload no`. freshclam은 하루 12회(`FRESHCLAM_CHECKS`) 갱신을 확인합니다.
- 메모리: 엔진 약 1.2~1.5 GiB. `ConcurrentDatabaseReload no`로 시그니처 갱신 중 메모리 2배 사용을 피하는 대신, 갱신 적용 동안(수십 초) 검사가 대기하며 앱 검사 제한 60초를 넘으면 `SCAN_UNAVAILABLE`입니다. Railway 사용량 요금이 늘어납니다.
- 이미지 진입점은 clamd가 비정상 종료돼도 컨테이너를 유지합니다. 첨부 화면 검사 상태가 계속 `unavailable`이면 ClamAV 서비스를 재배포합니다.
- 공개 도메인·TCP 프록시를 만들지 않습니다. clamd에는 인증이 없으므로 내부망에만 둡니다.


## 설치

[Railway 설치 안내](../.railway/README.md)의 버킷 변수와 `006_attachments.sql` 마이그레이션, 새 앱 배포가 필요합니다. 버킷만 만들거나 연결 변수만 넣어서는 기존 앱에 첨부 화면이 나타나지 않습니다. 새 프로젝트 설치용 준비 파일은 실제 배포 시점에 적용합니다.

## 검증 기록 — 2026-11-14

실제 PostgreSQL 통합·동시성·권한·실패 복구 테스트와 전체 `go test -race ./...`, 정적 검사, 빌드가 통과했습니다. 내부 화면을 로컬에서 확인하며 목록 요청의 페이지 크기 오류를 수정했습니다. 사용자 승인 후 운영 Attachments 버킷에 비민감 테스트 파일 하나를 전송하고 내용·다운로드 헤더·삭제 후 404 응답을 확인했습니다. 기존 파일·운영 DB는 검사에서 변경하지 않았습니다. 새 앱의 운영 배포와 실제 사용자 화면에서의 전체 흐름 검사는 별도입니다.
