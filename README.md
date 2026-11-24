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
| A2 | 무작위 카테고리·가격 범위 조회 결과가 정답과 같음 |
| A3 | `minPrice = maxPrice` 일 때 그 가격의 책이 포함됨 |
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
| macOS | `checker-macos` |
| Windows | `checker-windows-amd64.exe` |
| Linux | `checker-linux-amd64` |

제출받은 zip 을 한 폴더에 모아 복호화합니다.

```sh
go run ./cmd/instructor decrypt -out merged.csv submissions/*.zip
```

- `merged.csv`: 학생별·검사별 결과. `integrity` 가 `INVALID` 면 변조되었거나 파일이 빠진 제출입니다.
- `logs/`: 학생별 로그.
- 서로 다른 학번의 MAC 주소가 겹치면 경고가 나옵니다 (같은 기기 의심).

## 학생

1. 스프링 서버를 먼저 실행합니다.
2. 채점기를 실행하면 브라우저에 입력 창이 열립니다.
3. 학번, Neon 주소, DB 사용자명·비밀번호, 스키마(선택), 포트를 넣고 [검사 시작]을 누릅니다.
4. `results/submit_<학번>_<시각>.zip` 을 제출합니다.

macOS 에서 실행이 막히면:

```sh
xattr -d com.apple.quarantine ./checker-macos
chmod +x ./checker-macos
./checker-macos
```

Windows 에서 "PC 보호" 창이 뜨면 [추가 정보] → [실행]을 누릅니다.

브라우저 없이 실행하려면 플래그로 값을 줍니다. 빠진 값은 물어봅니다.

```sh
./checker-macos -id 20231234 -db "jdbc:postgresql://ep-xxx.neon.tech/neondb" -db-user neondb_owner -port 8080
```

## 참고

- DB 비밀번호는 로그와 결과 어디에도 남지 않습니다.
- 학생은 결과를 열 수 없지만, 실행 파일을 분석하면 위조할 수는 있습니다. 의심되면 직접 다시 채점하세요.
- 정답 데이터는 `internal/dataset/books.json` 에 들어 있습니다. 사이트가 바뀌면 `make snapshot` 후 `make dist`.
