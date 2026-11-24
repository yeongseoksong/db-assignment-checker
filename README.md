# DB 과제 채점기

Spring Boot + jsoup 로 `books.toscrape.com` 을 스크레이핑해 Neon PostgreSQL 에 저장하고 `GET /books` 로 조회하는 과제(`REQUIRE.md`)의 채점 프로그램입니다.
학생은 자기 서버를 띄운 채 채점기를 실행하고, 생성된 `.enc` 파일을 제출합니다. 교수자는 `instructor` 도구로 복호화하고 병합합니다.

점수는 없고 검사별 통과/실패만 기록합니다. 표준 라이브러리만 쓰며, Postgres 드라이버 `github.com/jackc/pgx/v5` 하나만 예외입니다.

## 검사 항목

| ID | 그룹 | 내용 |
|---|---|---|
| D1 | DB | Neon 연결 성공 (15초 제한, 읽기 전용 트랜잭션) |
| D2 | DB | public 스키마의 텍스트 컬럼 중 하나가 데이터셋 책 제목 1000권의 95% 이상과 일치 |
| D3 | DB | 텍스트 컬럼 하나에 카테고리 50개가 모두 존재 (대소문자·공백 무시) |
| A1 | API | `category=Travel&minPrice=10&maxPrice=30` 결과가 기대 집합과 정확히 같음 |
| A2 | API | 무작위 카테고리와 가격 범위의 결과가 기대 집합과 정확히 같음 |
| A3 | API | 무작위 책 하나의 가격으로 `minPrice=maxPrice` 조회 시 그 책이 포함됨 (이상/이하) |
| E1 | 오류처리 | 없는 카테고리 → JSON 의 `message` 가 `존재하지 않는 카테고리입니다` 와 정확히 일치 |
| E2 | 오류처리 | `minPrice > maxPrice` → `message` 가 `minPrice가 maxPrice보다 높아서는 안됩니다` 와 정확히 일치 |

- A 검사는 JSON 필드명이 달라도 됩니다. 원소의 모든 문자열 값을 제목 후보로 보고, 소문자화, HTML 엔티티 해제, 공백 정리, 끝의 `...` 제거 후 기대 제목과 같거나 기대 제목의 10자 이상 접두사이면 일치로 봅니다. 응답이 `{"books":[...]}` 처럼 배열 필드가 하나인 객체여도 됩니다.
- 실패하면 Detail 에 누락된 제목과 불필요하게 들어온 원소를 적습니다.
- E 검사의 상태코드는 무엇이든 허용하고(로그에만 기록), 문구는 앞뒤 공백만 걷고 그대로 비교합니다.
- 서버에 연결할 수 없으면 API/오류처리 검사는 전부 실패로 기록되고, 결과 파일은 그래도 만들어집니다.
- 검사를 추가하려면 `internal/checks/check.go` 의 `All` 표에 한 줄을 더합니다.

## 교수자 준비 절차

필요한 것은 Go 1.27 입니다.

```sh
make keys      # keys/private.key, public.key, hmac.secret 생성 (이미 있으면 에러, 덮어쓰지 않음)
make dist      # dist/ 에 4개 플랫폼 바이너리 생성 (공개키와 HMAC 비밀키가 주입됨)
```

1. `keys/` 는 git 에 올라가지 않습니다. `private.key` 를 잃으면 받은 `.enc` 를 열 수 없으니 따로 백업하세요.
2. `dist/` 의 바이너리를 학생에게 배포합니다 (`checker-macos` (Apple Silicon·Intel 공용), `checker-windows-amd64.exe`, `checker-linux-amd64`).
3. 제출 기한 뒤 복호화·병합합니다.

```sh
go run ./cmd/instructor decrypt -key keys/private.key -hmac keys/hmac.secret -out merged.csv results/*.enc
```

`merged.csv` 는 UTF-8 BOM 이 붙어 엑셀에서 한글이 깨지지 않습니다. 열은 아래와 같고 마지막에 `integrity`, `source_file` 이 붙습니다.

`student_id, check_id, group, title, passed, detail, checked_at, checker_version, hostname`

검사마다 한 줄이고, 학생마다 `TOTAL` 행(detail 은 `통과 n / 전체 m`)이 있습니다.
복호화에 실패했거나 HMAC 이 맞지 않는 파일도 병합하되 `integrity` 열이 `INVALID: 사유` 이고 표준에러에 경고가 나옵니다. 정상은 `OK` 입니다.

기타 명령: `make snapshot`(books.toscrape.com 에서 `internal/dataset/books.json` 재생성, 50개 카테고리 1000권이 아니면 실패), `make test`.

## 학생 사용법

스프링 서버를 먼저 실행(기본 8080 포트)한 뒤 채점기를 실행합니다.

### 창 모드 (기본)

`checker` 를 더블클릭하거나 인자 없이 실행하면 브라우저에 입력 창이 열립니다. 입력은 아래 다섯 칸입니다.

1. 학번 (영문, 숫자만)
2. Neon 주소: `application.properties` 의 `spring.datasource.url` 값 (`jdbc:postgresql://ep-xxx.neon.tech/neondb?sslmode=require`). `jdbc:` 접두사, `postgresql://` 스킴, 스킴 없는 `호스트/DB이름` 모두 가능하고 `sslmode` 가 없으면 `require` 를 붙입니다.
3. DB 사용자명 (`spring.datasource.username`)
4. DB 비밀번호 (`spring.datasource.password`)
5. 로컬 포트 (기본 8080)

[검사 시작]을 누르면 검사별 ✔/✘ 와 이유, 생성된 `.enc` 와 `.log` 경로가 같은 화면에 나옵니다. 끝나면 [종료]를 누르거나 창을 닫으세요. 10분 동안 아무 입력이 없으면 자동으로 종료됩니다. 입력 창은 `127.0.0.1` 에서만 열립니다. 브라우저가 열리지 않으면 콘솔에 나온 주소를 직접 붙여넣으세요 (`-no-browser` 로 자동 열기를 끌 수 있습니다).

### 명령줄 모드

`-id`, `-db`, `-db-user`, `-port` 를 모두 주거나 `-cli` 를 주면 브라우저 없이 콘솔에서 실행합니다. 빠진 값은 물어봅니다.

```sh
./checker-macos -id 20231234 -db "jdbc:postgresql://ep-xxx.neon.tech/neondb?sslmode=require" -db-user neondb_owner -port 8080 -out ./results
```

비밀번호는 `-db-password` 로 주거나 `PGPASSWORD` 환경변수로 넘깁니다. 둘 다 없으면 물어보며, 이때 입력이 화면에 보입니다. 명령줄 모드는 종료 전에 "엔터를 누르면 종료합니다" 를 띄웁니다 (`-no-pause` 로 끔).

### 결과 파일

`-out`(기본 `./results`)에 두 파일이 생깁니다.

- `result_<학번>_<yyyyMMdd-HHmmss>.enc`: **이 파일을 제출합니다.** 암호화되어 있어 학생은 열 수 없습니다.
- `checker_<학번>_<yyyyMMdd-HHmmss>.log`: 평문 로그. 시작 정보, 요청 URL·상태코드·응답 본문(2KB까지), 검사 결과, 무작위 시드가 있습니다.

DB 비밀번호는 화면, 로그, 결과 파일 어디에도 남지 않습니다. 사용자명은 남을 수 있습니다.

### macOS 에서 실행이 막힐 때 (Gatekeeper)

내려받은 파일은 격리 속성이 붙어 실행이 거부될 수 있습니다.

```sh
xattr -d com.apple.quarantine ./checker-macos
chmod +x ./checker-macos
./checker-macos
```

### Windows

`checker-windows-amd64.exe` 를 더블클릭합니다. "Windows 의 PC 보호" 창이 뜨면 [추가 정보], [실행]을 누르세요.

## 보안 한계

- 학생은 공개키만 가지므로 `.enc` 내용을 읽을 수 없습니다 (X25519 ECDH, HKDF-SHA256, AES-256-GCM).
- 위조를 줄이려고 CSV 끝에 HMAC-SHA256 을 붙입니다. 그런데 이 HMAC 비밀키가 배포 바이너리 안에 들어 있어, 작정하면 추출해서 결과를 위조할 수 있습니다. 공개키만으로는 누구나 `.enc` 를 만들 수 있다는 점도 같은 이유입니다.
- 위조가 의심되면 `.log` 의 시드와 요청 기록을 확인하고, 학생이 서버를 띄운 상태에서 교수자가 직접 다시 채점하세요.
- 로컬 입력 창은 요청마다 무작위 토큰을 확인하고 `localhost` 외 Host 헤더를 거부합니다.
