# 교수자용 작업 모음. 사용법은 docs/instructor.md 를 보세요.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

# test 의 <( ) 비교에 bash 가 필요하다
SHELL := /bin/bash

.PHONY: keys snapshot test readme lint fuzz dist clean

keys:
	go run ./cmd/instructor keygen -dir keys

snapshot:
	go run ./tools/snapshot

test:
	@# ./... 는 testdata/ 폴더를 건너뛰므로 가짜 서버는 따로 지정한다
	go vet ./... ./testdata/fakeserver
	go test ./...
	@# OS별 대응은 사용법(guide.md)과 공지(notice.md)에 똑같이 쓴다. 한쪽만 고치면 여기서 실패한다.
	@diff -B <(sed -n '/^## OS별 대응/,/^## [^O]/p' docs/guide.md | sed '/^## [^O]/d') \
	      <(sed -n '/^## OS별 대응/,/^## [^O]/p' docs/notice.md | sed '/^## [^O]/d') \
	  || (echo "docs/guide.md 와 docs/notice.md 의 'OS별 대응' 부분이 다릅니다. 둘을 똑같이 맞추세요" && exit 1)
	@# README.md 는 docs/guide.md 와 같아야 한다 (이미지 경로만 docs/images/).
	@sed 's#](images/#](docs/images/#g' docs/guide.md | diff - README.md \
	  || (echo "README.md 가 docs/guide.md 와 다릅니다. make readme 로 다시 만드세요" && exit 1)

# README.md 를 docs/guide.md 로부터 다시 만든다 (저장소 첫 화면 기준으로 이미지 경로만 바꿈)
readme:
	sed 's#](images/#](docs/images/#g' docs/guide.md > README.md

# keys/ 의 공개키와 HMAC 비밀키를 바이너리에 주입해 플랫폼별로 크로스컴파일한다.
# 중간 파일은 build/ 에서 만들고 dist/ 에는 완성된 zip 만 쓴다.
# (dist/ 안에서 zip 을 풀어 테스트해도 다음 빌드와 이름이 겹치지 않는다.)
dist:
	@test -f keys/public.key || (echo "keys/public.key 가 없습니다. 먼저 make keys 를 실행하세요" && exit 1)
	@test -f keys/hmac.secret || (echo "keys/hmac.secret 이 없습니다. 먼저 make keys 를 실행하세요" && exit 1)
	@rm -rf build && mkdir -p build dist
	@PUB=$$(tr -d '\n' < keys/public.key); SEC=$$(tr -d '\n' < keys/hmac.secret); \
	LDFLAGS="-s -w -X main.version=$(VERSION) -X main.publicKeyB64=$$PUB -X main.hmacSecretB64=$$SEC"; \
	for target in darwin/arm64 darwin/amd64 windows/amd64 linux/amd64; do \
		os=$${target%/*}; arch=$${target#*/}; \
		ext=""; if [ "$$os" = "windows" ]; then ext=".exe"; fi; \
		out="build/checker-$$os-$$arch$$ext"; \
		echo "빌드: $$out"; \
		GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 go build -trimpath -ldflags "$$LDFLAGS" -o "$$out" ./cmd/checker || exit 1; \
	done
	@# Apple Silicon 과 Intel 용을 유니버설 바이너리 하나로 합친다 (학생이 CPU 를 고를 필요 없음).
	lipo -create -output build/checker-macos build/checker-darwin-arm64 build/checker-darwin-amd64
	@# 모두 zip 으로 묶는다. macOS·Linux 는 실행 권한을 보존하려고, Windows 는 LMS·메일이 .exe 를 막는 경우가 있어서다.
	@# 학생 안내(docs/guide.md)와 그 캡처(docs/images)도 함께 넣는다. 한글 파일명은 Windows 에서 깨지므로 GUIDE.md 로 넣는다.
	cp docs/guide.md build/GUIDE.md && cp -R docs/images build/images
	cd build && for f in checker-macos checker-linux-amd64 checker-windows-amd64.exe; do \
		zip -q -r "../dist/$${f%.exe}.zip.tmp" "$$f" GUIDE.md images && mv "../dist/$${f%.exe}.zip.tmp" "../dist/$${f%.exe}.zip" || exit 1; \
	done
	rm -rf build
	@ls -l dist/*.zip

# 정적 분석. golangci-lint 가 필요하다 (brew install golangci-lint). 설정은 .golangci.yml
lint:
	golangci-lint run ./... ./testdata/fakeserver

# 퍼징: 신뢰할 수 없는 입력(학생 서버 응답, 제출 파일)을 다루는 코드에 무작위 입력을 넣어 본다.
# 대상마다 FUZZTIME 동안 돈다. 실패 입력은 해당 패키지의 testdata/fuzz/ 에 저장되어 이후 go test 에서 계속 검사된다.
# 최소화(-fuzzminimizetime)는 끈다. 켜 두면 새 입력을 찾을 때마다 수십 초씩 실행이 멈춘 것처럼 보인다.
FUZZTIME ?= 30s
FUZZ_TARGETS = ./internal/checks:FuzzJudgeAnyResponse ./internal/checks:FuzzCorrectResponseAlwaysPasses \
	./internal/report:FuzzOpenRejectsAnythingElse ./internal/report:FuzzCSVRoundTrip ./internal/report:FuzzParseCSVAnyInput \
	./cmd/instructor:FuzzReadSubmission
fuzz:
	@for t in $(FUZZ_TARGETS); do \
		pkg=$${t%%:*}; name=$${t##*:}; echo "== $$name ($(FUZZTIME))"; \
		go test $$pkg -run '^$$' -fuzz "^$$name\$$" -fuzztime $(FUZZTIME) -fuzzminimizetime 0 2>&1 | grep -vE '^fuzz: elapsed' || exit 1; \
	done

clean:
	rm -rf build dist


mock:
	go run ./testdata/fakeserver
