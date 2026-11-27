# DB 과제 채점기

`REQUIRE.md` 과제(Spring Boot + jsoup + Neon PostgreSQL + `GET /books`)의 채점 프로그램입니다.
학생이 자기 서버를 띄운 채 실행하면 암호화된 제출 zip 이 생기고, 교수자가 모아서 복호화합니다.

## 검사 항목

| ID | 내용 |
|---|---|
| D1 | Neon DB 연결 |
| D2 | 책 제목 1000권 중 95% 이상이 DB 에 있음 |
| D3 | 카테고리 50개가 DB 에 모두 있음 |
| A1 | `Travel`, 10~30 조회 결과가 정답과 같음 |
| A2 | 무작위 카테고리·가격 범위 10건이 모두 정답과 같음 |
| A3 | 무작위 책 10권으로 `minPrice = maxPrice` 조회 시 모두 그 책이 포함됨 |
| E1 | 없는 카테고리 → `{"message": "존재하지 않는 카테고리입니다"}` |
| E2 | `minPrice > maxPrice` → `{"message": "minPrice가 maxPrice보다 높아서는 안됩니다"}` |

점수 없이 통과/실패만 기록합니다. JSON 필드 이름은 보지 않습니다. 제목은 잘린 형태(`...`)도, 가격은 숫자·문자열(`"£36.94"`)도 인정합니다.

## 교수자

```sh
make keys    # 키 생성 (처음 한 번. keys/private.key 는 따로 백업)
make dist    # dist/ 에 배포 파일 생성
```

`dist/` 의 파일을 학생에게 나눠 줍니다.

| OS | 파일 |
|---|---|
| macOS | `checker-macos.zip` |
| Windows | `checker-windows-amd64.zip` |
| Linux | `checker-linux-amd64.zip` |

모두 zip 으로 묶습니다. macOS·Linux 는 실행 권한이 유지되고, Windows 는 `.exe` 업로드를 막는 LMS 에도 올릴 수 있습니다.
Windows 에서 zip 을 풀지 않고 안의 `.exe` 를 바로 실행하면 채점기가 압축을 먼저 풀라고 안내하고 멈춥니다.
각 zip 에는 학생 사용 안내 `GUIDE.md` 와 캡처(`images/`)가 함께 들어갑니다. 원본은 `docs/guide.md` 이고, 고친 뒤 `make dist` 를 다시 하면 반영됩니다.
LMS 공지용 글은 `docs/notice.md` 입니다 (마감, 제출물, OS별 대응). OS별 대응은 두 문서에 똑같이 들어가며 `make test` 가 어긋나면 실패합니다.

제출받은 zip 을 한 폴더에 모아 복호화합니다.

```sh
go run ./cmd/instructor decrypt -out merged.csv submissions/*.zip
```

- `merged.csv`: 학생별·검사별 결과. `integrity` 가 `INVALID` 면 변조되었거나 파일이 빠진 제출입니다.
- `logs/`: 학생별 로그.
- 서로 다른 학번의 MAC 주소가 겹치면 경고가 나옵니다 (같은 기기 의심).

## 학생

1. 스프링 서버를 먼저 실행합니다.
2. 받은 zip 을 풀고(Windows 는 오른쪽 클릭 → [압축 풀기]), 풀린 채점기를 더블클릭하면 브라우저에 입력 창이 열립니다.
3. 학번, Neon 주소, DB 사용자명·비밀번호, 스키마(선택), 포트를 넣고 [검사 시작]을 누릅니다.
4. `results/submit_<학번>_<시각>.zip` 을 제출합니다.

처음 실행할 때 경고창이 뜨면 한 번만 허용합니다.

- macOS: 경고창에서 [완료] → 시스템 설정 → 개인정보 보호 및 보안 → "checker-macos 이(가) 차단되었습니다" 옆 [그래도 열기] → 암호 입력 → [그래도 열기].
- Windows: "Windows 의 PC 보호" 창에서 [추가 정보] → [실행]. 백신이 파일을 지웠으면 Windows 보안 → 보호 기록에서 [디바이스에서 허용] 후 다시 받습니다.

## 채점기 시험해 보기

학생 서버 대신 가짜 서버를 띄워 채점기가 제대로 잡아내는지 볼 수 있습니다. 정답 버전과 일부러 틀린 버전이 한꺼번에 포트별로 뜹니다.

```sh
go run ./testdata/fakeserver              # :8080 부터 버전마다 포트 하나씩
go run ./testdata/fakeserver -port 9000   # :9000 부터
```

시작할 때 포트별 버전과 실패해야 하는 검사가 출력됩니다. 채점기 입력 창의 포트 칸에 그 포트를 넣으세요. 가짜 서버는 DB 를 쓰지 않으므로 D1~D3 는 실패로 나옵니다.

## 개발 명령

| 명령 | 하는 일 |
|---|---|
| `make test` | vet, 테스트, 안내문 OS별 대응 일치 확인 |
| `make lint` | golangci-lint 정적 분석 (`brew install golangci-lint` 필요, 설정은 `.golangci.yml`) |
| `make fuzz` | 퍼징. 대상마다 `FUZZTIME`(기본 30s) 동안 |

## 참고

- DB 비밀번호는 로그와 결과 어디에도 남지 않습니다.
- 학생은 결과를 열 수 없지만, 실행 파일을 분석하면 위조할 수는 있습니다. 의심되면 직접 다시 채점하세요.
- 정답 데이터는 `internal/dataset/books.json` 에 들어 있습니다. 사이트가 바뀌면 `make snapshot` 후 `make dist`.
