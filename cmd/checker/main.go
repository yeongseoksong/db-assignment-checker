// checker 는 학생이 자기 컴퓨터에서 실행하는 채점 프로그램이다.
// 실행하면 브라우저에 입력 창이 열리고, 거기서 값을 넣어 검사한다.
package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"dbchecker/internal/app"
	"dbchecker/internal/report"
	"dbchecker/internal/webui"
)

// 아래 세 값은 빌드할 때 -ldflags "-X main.publicKeyB64=... -X main.hmacSecretB64=..." 로 주입한다.
var (
	version       = "dev"
	publicKeyB64  = ""
	hmacSecretB64 = ""
)

func main() {
	code := run()
	os.Exit(code)
}

// runningFromTempDir 는 Windows 에서 zip 을 풀지 않고 안의 .exe 를 바로 실행했는지 본다.
// 그러면 탐색기가 임시 폴더에 풀어 실행하므로 실행 파일 옆 results 폴더가 임시 폴더에 생겨 사라진다.
func runningFromTempDir() bool {
	if runtime.GOOS != "windows" {
		return false
	}
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	return insideDir(exe, os.TempDir())
}

// insideDir 는 path 가 dir 안에 있으면 true 다. Windows 경로는 대소문자를 구분하지 않으므로 소문자로 비교한다.
func insideDir(path, dir string) bool {
	if dir == "" {
		return false
	}
	rel, err := filepath.Rel(strings.ToLower(dir), strings.ToLower(path))
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func run() int {
	if runningFromTempDir() {
		fmt.Println("zip 파일 안에서 바로 실행했습니다. 이대로는 제출 파일이 임시 폴더에 생겨 사라집니다.")
		fmt.Println("zip 파일을 오른쪽 클릭 → [압축 풀기] 한 뒤, 풀린 폴더의 checker-windows-amd64.exe 를 실행하세요.")
		fmt.Print("엔터를 누르면 종료합니다...")
		bufio.NewReader(os.Stdin).ReadString('\n')
		return 1
	}
	var outDir string
	var noBrowser bool
	flag.StringVar(&outDir, "out", "", "결과 파일을 저장할 폴더 (기본: 실행 파일 옆 results 폴더)")
	flag.BoolVar(&noBrowser, "no-browser", false, "브라우저를 자동으로 열지 않음 (테스트용)")
	flag.Parse()

	cfg, err := loadConfig(outDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "오류:", err)
		return 1
	}
	if err := runWeb(cfg, noBrowser); err != nil {
		fmt.Fprintln(os.Stderr, "오류:", err)
		return 1
	}
	return 0
}

func loadConfig(outDir string) (app.Config, error) {
	cfg := app.Config{Version: version}
	resolved, err := resolveOutDir(outDir)
	if err != nil {
		return cfg, err
	}
	cfg.OutDir = resolved
	if publicKeyB64 == "" || hmacSecretB64 == "" {
		return cfg, errors.New("교수자용으로 빌드되지 않은 바이너리입니다. 교수자가 배포한 checker 파일을 사용하세요")
	}
	pubBytes, err := base64.StdEncoding.DecodeString(publicKeyB64)
	if err != nil {
		return cfg, fmt.Errorf("내장 공개키가 손상되었습니다. 교수자에게 문의하세요: %w", err)
	}
	cfg.PublicKey, err = report.ParsePublicKey(pubBytes)
	if err != nil {
		return cfg, fmt.Errorf("내장 공개키가 올바르지 않습니다. 교수자에게 문의하세요: %w", err)
	}
	cfg.HMACSecret, err = base64.StdEncoding.DecodeString(hmacSecretB64)
	if err != nil {
		return cfg, fmt.Errorf("내장 HMAC 키가 손상되었습니다. 교수자에게 문의하세요: %w", err)
	}
	return cfg, nil
}

// resolveOutDir 는 결과 폴더를 절대 경로로 정한다.
// 기본값은 실행 파일 옆 results 폴더다. macOS 에서 더블클릭하면 작업 폴더가 홈 폴더가 되므로
// 작업 폴더 기준으로 두면 학생이 파일을 찾지 못한다.
func resolveOutDir(outDir string) (string, error) {
	if outDir == "" {
		exe, err := os.Executable()
		if err != nil {
			return "", fmt.Errorf("실행 파일 위치를 알 수 없습니다. -out 으로 결과 폴더를 지정하세요: %w", err)
		}
		outDir = filepath.Join(filepath.Dir(exe), "results")
	}
	abs, err := filepath.Abs(outDir)
	if err != nil {
		return "", fmt.Errorf("결과 폴더 경로 %q 가 올바르지 않습니다: %w", outDir, err)
	}
	return abs, nil
}

func runWeb(cfg app.Config, noBrowser bool) error {
	srv := webui.New(func(in app.Input) (*app.Result, error) {
		return app.Run(context.Background(), cfg, in)
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("입력 창을 띄울 수 없습니다. 다른 프로그램이 포트를 막고 있는지 확인하세요: %w", err)
	}
	addr := "http://" + listener.Addr().String() + "/"
	httpServer := &http.Server{Handler: srv.Handler()}
	go httpServer.Serve(listener)

	fmt.Println("입력 창 주소:", addr)
	if noBrowser {
		fmt.Println("(-no-browser: 브라우저를 자동으로 열지 않았습니다)")
	} else if err := openBrowser(addr); err != nil {
		fmt.Println("브라우저를 자동으로 열 수 없었습니다. 위 주소를 브라우저에 직접 붙여넣으세요.")
	}
	<-srv.Done
	httpServer.Shutdown(context.Background())
	fmt.Println("종료합니다.")
	return nil
}

func openBrowser(target string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", target)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	default:
		cmd = exec.Command("xdg-open", target)
	}
	return cmd.Start()
}
