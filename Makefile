# 교수자용 작업 모음. 사용법은 README.md 를 보세요.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: keys snapshot test dist clean

keys:
	go run ./cmd/instructor keygen -dir keys

snapshot:
	go run ./tools/snapshot

test:
	@# ./... 는 testdata/ 폴더를 건너뛰므로 가짜 서버는 따로 지정한다
	go vet ./... ./testdata/fakeserver
	go test ./...

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

clean:
	rm -rf build dist
