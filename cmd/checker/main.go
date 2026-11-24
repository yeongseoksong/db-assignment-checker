// checker 는 학생이 자기 컴퓨터에서 실행하는 채점 프로그램이다.
//
//	창 모드:    checker                      (브라우저에 입력 창이 열린다)
//	명령줄 모드: checker -id 20231234 -db postgresql://... -port 8080
package main

import (
	"bufio"
	"context"
	"crypto/ecdh"
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
	"strconv"
	"strings"

	"dbchecker/internal/app"
	"dbchecker/internal/checks"
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

func run() int {
	var in app.Input
	var outDir string
	var noPause, noBrowser, cliMode bool
	flag.StringVar(&in.StudentID, "id", "", "학번 (영문/숫자)")
	flag.StringVar(&in.DBAddress, "db", "", "Neon 주소 (jdbc:postgresql://ep-xxx.neon.tech/neondb?sslmode=require)")
	flag.StringVar(&in.DBUser, "db-user", "", "DB 사용자명")
	flag.StringVar(&in.DBPassword, "db-password", "", "DB 비밀번호 (비우면 PGPASSWORD 환경변수, 그것도 없으면 물어봄)")
	flag.StringVar(&in.Port, "port", strconv.Itoa(app.DefaultPort), "스프링 서버가 떠 있는 로컬 포트")
	flag.StringVar(&outDir, "out", "", "결과 파일을 저장할 폴더 (기본: 실행 파일 옆 results 폴더)")
	flag.BoolVar(&cliMode, "cli", false, "브라우저 없이 콘솔에서 실행")
	flag.BoolVar(&noBrowser, "no-browser", false, "창 모드에서 브라우저를 자동으로 열지 않음")
	flag.BoolVar(&noPause, "no-pause", false, "종료 전에 엔터를 기다리지 않음 (명령줄 모드)")
	flag.Parse()

	cfg, err := loadConfig(outDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "오류:", err)
		return 1
	}
	given := map[string]bool{}
	flag.Visit(func(f *flag.Flag) { given[f.Name] = true })
	allGiven := given["id"] && given["db"] && given["db-user"] && given["port"]

	if cliMode || allGiven {
		code := runCLI(cfg, in)
		if !noPause && isTerminal(os.Stdin) {
			fmt.Print("엔터를 누르면 종료합니다...")
			bufio.NewReader(os.Stdin).ReadString('\n')
		}
		return code
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
	cfg.PublicKey, err = ecdh.X25519().NewPublicKey(pubBytes)
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

// ---- 명령줄 모드 ----

func runCLI(cfg app.Config, in app.Input) int {
	stdin := bufio.NewReader(os.Stdin)
	if in.StudentID == "" {
		in.StudentID = prompt(stdin, "학번을 입력하세요: ")
	}
	if in.DBAddress == "" {
		in.DBAddress = prompt(stdin, "Neon 주소를 입력하세요 (비우면 DB 검사는 실패): ")
	}
	if in.DBAddress != "" && in.DBUser == "" {
		in.DBUser = prompt(stdin, "DB 사용자명을 입력하세요: ")
	}
	if in.DBAddress != "" && in.DBPassword == "" {
		in.DBPassword = os.Getenv("PGPASSWORD")
	}
	if in.DBAddress != "" && in.DBPassword == "" {
		// 외부 패키지 없이는 입력을 가리기 어려워 화면에 보이게 둔다.
		in.DBPassword = prompt(stdin, "DB 비밀번호를 입력하세요 (화면에 보입니다): ")
	}
	in, err := app.Validate(in)
	if err != nil {
		fmt.Fprintln(os.Stderr, "오류:", err)
		return 1
	}
	res, err := app.Run(context.Background(), cfg, in, printOutcome)
	if err != nil {
		fmt.Fprintln(os.Stderr, "오류:", err)
		return 1
	}
	fmt.Printf("\n통과 %d / 전체 %d\n", res.Passed, res.Total)
	fmt.Println("제출 파일(.enc):", res.EncPath)
	fmt.Println("로그 파일      :", res.LogPath)
	return 0
}

func prompt(r *bufio.Reader, msg string) string {
	fmt.Print(msg)
	line, _ := r.ReadString('\n')
	return strings.TrimSpace(line)
}

func printOutcome(o checks.Outcome) {
	mark := "✔"
	if !o.Passed() {
		mark = "✘"
	}
	fmt.Printf("%s %-3s [%s] %s\n", mark, o.Check.ID, o.Check.Group, o.Check.Title)
	if o.Result.Detail != "" {
		fmt.Printf("    %s\n", o.Result.Detail)
	}
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// ---- 창 모드 ----

func runWeb(cfg app.Config, noBrowser bool) error {
	srv := webui.New(func(in app.Input) (*app.Result, error) {
		return app.Run(context.Background(), cfg, in, nil)
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
