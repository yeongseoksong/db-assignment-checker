# 교수자용 작업 모음. 사용법은 README.md 를 보세요.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: keys snapshot test dist clean

keys:
	go run ./cmd/instructor keygen -dir keys

snapshot:
	go run ./tools/snapshot

test:
	go vet ./...
	go test ./...

# keys/ 의 공개키와 HMAC 비밀키를 바이너리에 주입해 플랫폼별로 크로스컴파일한다.
dist:
	@test -f keys/public.key || (echo "keys/public.key 가 없습니다. 먼저 make keys 를 실행하세요" && exit 1)
	@test -f keys/hmac.secret || (echo "keys/hmac.secret 이 없습니다. 먼저 make keys 를 실행하세요" && exit 1)
	@mkdir -p dist
	@PUB=$$(tr -d '\n' < keys/public.key); SEC=$$(tr -d '\n' < keys/hmac.secret); \
	LDFLAGS="-s -w -X main.version=$(VERSION) -X main.publicKeyB64=$$PUB -X main.hmacSecretB64=$$SEC"; \
	for target in darwin/arm64 darwin/amd64 windows/amd64 linux/amd64; do \
		os=$${target%/*}; arch=$${target#*/}; \
		ext=""; if [ "$$os" = "windows" ]; then ext=".exe"; fi; \
		out="dist/checker-$$os-$$arch$$ext"; \
		echo "빌드: $$out"; \
		GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 go build -trimpath -ldflags "$$LDFLAGS" -o "$$out" ./cmd/checker || exit 1; \
	done
	@# Apple Silicon 과 Intel 용을 유니버설 바이너리 하나로 합친다 (학생이 CPU 를 고를 필요 없음).
	lipo -create -output dist/checker-macos dist/checker-darwin-arm64 dist/checker-darwin-amd64
	rm dist/checker-darwin-arm64 dist/checker-darwin-amd64
	@# 모두 zip 으로 묶는다. macOS·Linux 는 실행 권한을 보존하려고, Windows 는 LMS·메일이 .exe 를 막는 경우가 있어서다.
	cd dist && for f in checker-macos checker-linux-amd64 checker-windows-amd64.exe; do \
		rm -f "$${f%.exe}.zip" && zip -q "$${f%.exe}.zip" "$$f" && rm "$$f" || exit 1; \
	done

clean:
	rm -rf dist
