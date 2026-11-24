// Package webui 는 학생용 입력 창이다. 127.0.0.1 에서만 듣는 작은 웹 서버가
// 폼 하나를 보여 주고 검사를 실행한다. 외부 GUI 라이브러리를 쓰지 않는다.
package webui

import (
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"dbchecker/internal/app"
	"dbchecker/internal/checks"
)

//go:embed index.html
var indexHTML string

const (
	closeGrace  = 3 * time.Second
	idleTimeout = 10 * time.Minute
)

// RunFunc 는 검증된 입력으로 채점을 실행한다.
type RunFunc func(in app.Input) (*app.Result, error)

// Server 는 입력 창 핸들러다. Done 이 닫히면 프로세스를 끝내면 된다.
type Server struct {
	run   RunFunc
	token string

	Done chan struct{}

	mu        sync.Mutex // 중복 실행 방지
	stateMu   sync.Mutex // 타이머 상태 보호
	idleTimer *time.Timer
	closeTmr  *time.Timer
	running   bool
	doneOnce  sync.Once

	idle, grace time.Duration
}

// New 는 서버를 만든다. 10분 동안 아무 요청이 없으면 Done 이 닫힌다.
func New(run RunFunc) *Server {
	buf := make([]byte, 16)
	rand.Read(buf)
	s := &Server{run: run, token: hex.EncodeToString(buf), Done: make(chan struct{}), idle: idleTimeout, grace: closeGrace}
	s.idleTimer = time.AfterFunc(s.idle, s.finish)
	return s
}

func (s *Server) finish() {
	s.doneOnce.Do(func() { close(s.Done) })
}

// touch 는 요청이 올 때마다 유휴 타이머를 되감고 예약된 종료를 취소한다.
func (s *Server) touch() {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.idleTimer.Reset(s.idle)
	if s.closeTmr != nil {
		s.closeTmr.Stop()
		s.closeTmr = nil
	}
}

// Handler 는 라우팅을 돌려준다.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("POST /run", s.handleRun)
	mux.HandleFunc("POST /quit", s.handleQuit)
	mux.HandleFunc("POST /closing", s.handleClosing)
	return localOnly(mux)
}

// localOnly 는 Host 헤더가 루프백 주소가 아닌 요청을 거부한다 (DNS rebinding 방어).
func localOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if !strings.HasPrefix(host, "127.0.0.1:") && !strings.HasPrefix(host, "localhost:") {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	s.touch()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write([]byte(strings.Replace(indexHTML, "__TOKEN__", s.token, 1)))
}

func (s *Server) authorized(r *http.Request) bool {
	return r.PostFormValue("token") == s.token
}

type outcomeJSON struct {
	ID     string `json:"id"`
	Group  string `json:"group"`
	Title  string `json:"title"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail"`
}

type runResponse struct {
	Error      string        `json:"error,omitempty"`
	Outcomes   []outcomeJSON `json:"outcomes,omitempty"`
	Passed     int           `json:"passed"`
	Total      int           `json:"total"`
	SubmitPath string        `json:"submitPath,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func (s *Server) handleRun(w http.ResponseWriter, r *http.Request) {
	s.touch()
	if !s.authorized(r) {
		writeJSON(w, http.StatusForbidden, runResponse{Error: "잘못된 요청입니다. 채점기를 다시 실행하세요"})
		return
	}
	if !s.mu.TryLock() {
		writeJSON(w, http.StatusConflict, runResponse{Error: "이미 검사 중입니다. 끝날 때까지 기다리세요"})
		return
	}
	defer s.mu.Unlock()
	s.setRunning(true)
	defer s.setRunning(false)

	in, err := app.Validate(app.Input{
		StudentID:  r.PostFormValue("id"),
		DBAddress:  r.PostFormValue("db"),
		DBUser:     r.PostFormValue("dbuser"),
		DBPassword: r.PostFormValue("dbpass"),
		DBSchema:   r.PostFormValue("dbschema"),
		Port:       r.PostFormValue("port"),
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, runResponse{Error: err.Error()})
		return
	}
	res, err := s.run(in)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, runResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, toResponse(res))
}

func toResponse(res *app.Result) runResponse {
	resp := runResponse{Passed: res.Passed, Total: res.Total, SubmitPath: res.SubmitPath}
	for _, o := range res.Outcomes {
		resp.Outcomes = append(resp.Outcomes, toOutcome(o))
	}
	return resp
}

func toOutcome(o checks.Outcome) outcomeJSON {
	return outcomeJSON{ID: o.Check.ID, Group: o.Check.Group, Title: o.Check.Title, Passed: o.Passed(), Detail: o.Result.Detail}
}

func (s *Server) setRunning(v bool) {
	s.stateMu.Lock()
	s.running = v
	s.stateMu.Unlock()
}

func (s *Server) handleQuit(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	go func() {
		time.Sleep(200 * time.Millisecond) // 응답이 나간 뒤 종료
		s.finish()
	}()
}

// handleClosing 은 창이 닫힐 때 오는 신호다. 검사 중이 아니면 잠시 뒤 종료를 예약한다.
func (s *Server) handleClosing(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if !s.running && s.closeTmr == nil {
		s.closeTmr = time.AfterFunc(s.grace, s.finish)
	}
	w.WriteHeader(http.StatusNoContent)
}
