// Package app 는 학생용 채점 한 번의 전체 흐름(검증, 검사, 로그, CSV, 암호화, 파일 쓰기)이다.
// 웹 입력 창과 명령줄 모드가 같은 함수를 쓴다.
package app

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"dbchecker/internal/checks"
	"dbchecker/internal/dataset"
	"dbchecker/internal/report"
)

// DefaultPort 는 로컬 서버 포트의 기본값이다.
const DefaultPort = 8080

var reStudentID = regexp.MustCompile(`^[A-Za-z0-9]+$`)

// Input 은 학생이 넣는 값이다.
type Input struct {
	StudentID  string
	DBAddress  string // Neon 주소 (jdbc:postgresql://..., postgresql://..., 호스트/DB 이름만 등)
	DBUser     string
	DBPassword string // 기록 금지
	Port       string // 1~65535 정수 (문자열 그대로 받아 Validate 에서 검증)

	// DBURL 은 Validate 가 만든 정규화된 연결 문자열이다 (비밀번호 포함, 기록 금지).
	DBURL string
}

// BaseURL 은 검증된 포트로 만든 로컬 서버 주소다.
func (in Input) BaseURL() string {
	return "http://localhost:" + in.Port
}

// Validate 는 입력을 정리하고 검증한다. 웹 폼과 플래그가 함께 쓰는 유일한 검증 지점이다.
func Validate(in Input) (Input, error) {
	in.StudentID = strings.TrimSpace(in.StudentID)
	in.DBURL = "" // 항상 DBAddress/DBUser/DBPassword 에서 새로 만든다
	in.Port = strings.TrimSpace(in.Port)

	if !reStudentID.MatchString(in.StudentID) {
		return in, errors.New("학번은 영문과 숫자만 입력할 수 있습니다 (예: 20231234)")
	}
	if strings.TrimSpace(in.DBAddress) != "" {
		dbURL, err := NormalizeDBAddress(in.DBAddress, in.DBUser, in.DBPassword)
		if err != nil {
			return in, err
		}
		in.DBURL = dbURL
	}
	if in.Port == "" {
		in.Port = strconv.Itoa(DefaultPort)
	}
	port, err := strconv.Atoi(in.Port)
	if err != nil || port < 1 || port > 65535 {
		return in, fmt.Errorf("포트 %q 가 올바르지 않습니다. 1~65535 사이의 숫자로 입력하세요 (예: 8080)", in.Port)
	}
	in.Port = strconv.Itoa(port)
	return in, nil
}

// NormalizeDBAddress 는 Neon 주소를 pgx 가 읽는 postgresql:// 연결 문자열로 바꾼다 (순수 함수).
// 받는 형태는 jdbc:postgresql://..., postgres(ql)://..., 그리고 스킴 없는 호스트/DB 이름이다.
// 자격증명은 입력칸 값이 우선이고, 비어 있을 때만 주소 안의 값(userinfo, 쿼리의 user/password)을 쓴다.
// sslmode 가 없으면 Neon 이 SSL 을 요구하므로 require 를 붙인다.
func NormalizeDBAddress(address, user, password string) (string, error) {
	addr := strings.TrimSpace(address)
	addr = strings.TrimPrefix(addr, "jdbc:")
	switch {
	case strings.HasPrefix(addr, "postgresql://"), strings.HasPrefix(addr, "postgres://"):
	case strings.Contains(addr, "://"):
		return "", errors.New("Neon 주소는 jdbc:postgresql:// 또는 postgresql:// 로 시작해야 합니다 (application.properties 의 spring.datasource.url 값을 붙여넣으세요)")
	default:
		addr = "postgresql://" + addr
	}
	u, err := url.Parse(addr)
	if err != nil || u.Host == "" {
		return "", errors.New("Neon 주소 형식이 올바르지 않습니다. 예: jdbc:postgresql://ep-xxx.neon.tech/neondb?sslmode=require")
	}
	query := u.Query()
	if user == "" && u.User != nil {
		user = u.User.Username()
	}
	if password == "" && u.User != nil {
		password, _ = u.User.Password()
	}
	if user == "" {
		user = query.Get("user")
	}
	if password == "" {
		password = query.Get("password")
	}
	query.Del("user")
	query.Del("password")
	if user == "" || password == "" {
		return "", errors.New("DB 사용자명/비밀번호를 입력하세요 (application.properties 의 spring.datasource.username / password 값)")
	}
	u.User = url.UserPassword(user, password)
	if !query.Has("sslmode") {
		query.Set("sslmode", "require")
	}
	u.RawQuery = query.Encode()
	return u.String(), nil
}

// Config 는 빌드 때 주입된 값과 출력 위치다.
type Config struct {
	PublicKey  *ecdh.PublicKey
	HMACSecret []byte
	Version    string
	OutDir     string
}

// Result 는 채점 결과와 만든 파일 경로다.
type Result struct {
	Outcomes []checks.Outcome
	EncPath  string
	LogPath  string
	Passed   int
	Total    int
}

// Run 은 검사를 실행하고 결과(.enc)와 암호화한 로그(.log.enc)를 쓴다. onDone 은 검사 하나가 끝날 때마다 불린다(nil 가능).
// in 은 Validate 를 거친 값이어야 한다.
func Run(ctx context.Context, cfg Config, in Input, onDone func(checks.Outcome)) (*Result, error) {
	if err := os.MkdirAll(cfg.OutDir, 0o755); err != nil {
		return nil, fmt.Errorf("출력 폴더 %q 를 만들 수 없습니다. -out 으로 쓸 수 있는 폴더를 지정하세요: %w", cfg.OutDir, err)
	}
	now := time.Now()
	stamp := now.Format("20060102-150405")
	logPath := filepath.Join(cfg.OutDir, fmt.Sprintf("checker_%s_%s.log.enc", in.StudentID, stamp))
	encPath := filepath.Join(cfg.OutDir, fmt.Sprintf("result_%s_%s.enc", in.StudentID, stamp))
	// 로그는 메모리에 모았다가 마지막에 교수자 공개키로 암호화해 쓴다 (학생은 읽을 수 없음).
	var logBuf bytes.Buffer
	log := newLogger(&logBuf)

	seed := now.UnixNano()
	// 연결 문자열은 반드시 가려서 기록한다.
	log.Info("채점 시작", "version", cfg.Version, "os", runtime.GOOS+"/"+runtime.GOARCH,
		"student_id", in.StudentID, "url", in.BaseURL(),
		"db", checks.RedactURL(in.DBURL), "seed", seed)

	data, err := dataset.Load()
	if err != nil {
		return nil, err
	}
	env := &checks.Env{
		BaseURL: in.BaseURL(), Data: data, Log: log, Seed: seed, DBURL: in.DBURL,
		HTTP: checks.NewHTTPClient(log), Rand: rand.New(rand.NewSource(seed)),
	}

	outcomes := checks.RunAll(ctx, env, onDone)
	passed := 0
	for _, o := range outcomes {
		if o.Passed() {
			passed++
		}
	}
	res := &Result{Outcomes: outcomes, EncPath: encPath, LogPath: logPath, Passed: passed, Total: len(outcomes)}
	log.Info("채점 종료", "passed", passed, "total", len(outcomes))

	csvBytes, err := report.BuildCSV(buildRows(cfg, in, now, res), cfg.HMACSecret)
	if err != nil {
		return nil, err
	}
	sealed, err := report.Seal(cfg.PublicKey, csvBytes)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(encPath, sealed, 0o644); err != nil {
		return nil, fmt.Errorf("결과 파일을 쓸 수 없습니다: %w", err)
	}
	sealedLog, err := report.Seal(cfg.PublicKey, logBuf.Bytes())
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(logPath, sealedLog, 0o644); err != nil {
		return nil, fmt.Errorf("로그 파일을 쓸 수 없습니다: %w", err)
	}
	return res, nil
}

func buildRows(cfg Config, in Input, now time.Time, res *Result) []report.Row {
	host, _ := os.Hostname()
	at := now.Format(time.RFC3339)
	var rows []report.Row
	for _, o := range res.Outcomes {
		rows = append(rows, report.Row{
			StudentID: in.StudentID, CheckID: o.Check.ID, Group: o.Check.Group, Title: o.Check.Title,
			Passed: o.Passed(), Detail: o.Result.Detail, CheckedAt: at, Version: cfg.Version, Host: host,
		})
	}
	rows = append(rows, report.Row{
		StudentID: in.StudentID, CheckID: "TOTAL", Group: "합계", Title: "합계",
		Passed: res.Passed == res.Total, Detail: fmt.Sprintf("통과 %d / 전체 %d", res.Passed, res.Total),
		CheckedAt: at, Version: cfg.Version, Host: host,
	})
	return rows
}

func newLogger(w io.Writer) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug}))
}
