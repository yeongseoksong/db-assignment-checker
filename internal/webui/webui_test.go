package webui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"dbchecker/internal/app"
	"dbchecker/internal/checks"
)

func fakeRun(in app.Input) (*app.Result, error) {
	o := checks.Outcome{Check: checks.Check{ID: "S1", Group: "구조", Title: "t"}, Result: checks.Result{Passed: true, Detail: "ok " + in.StudentID}}
	return &app.Result{Outcomes: []checks.Outcome{o}, Passed: 1, Total: 1, EncPath: "a.enc", LogPath: "a.log"}, nil
}

func post(t *testing.T, ts *httptest.Server, path string, form url.Values) *http.Response {
	t.Helper()
	resp, err := http.PostForm(ts.URL+path, form)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestIndexContainsTokenAndForm(t *testing.T) {
	s := New(fakeRun)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	// httptest 의 Host 는 127.0.0.1:port 라 허용된다.
	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	buf := new(strings.Builder)
	b := make([]byte, 1<<16)
	n, _ := resp.Body.Read(b)
	buf.Write(b[:n])
	page := buf.String()
	if !strings.Contains(page, s.token) || !strings.Contains(page, `type="password"`) || strings.Contains(page, "http://cdn") || strings.Contains(page, "https://") {
		t.Error("페이지에 토큰·password 입력이 있어야 하고 외부 리소스가 없어야 합니다")
	}
}

func TestRunRequiresTokenAndValidates(t *testing.T) {
	s := New(fakeRun)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	if r := post(t, ts, "/run", url.Values{"id": {"1"}}); r.StatusCode != http.StatusForbidden {
		t.Errorf("토큰이 없으면 403 이어야 합니다: %d", r.StatusCode)
	}
	if r := post(t, ts, "/run", url.Values{"token": {s.token}, "id": {"홍길동"}}); r.StatusCode != http.StatusBadRequest {
		t.Errorf("잘못된 학번은 400 이어야 합니다: %d", r.StatusCode)
	}
	r := post(t, ts, "/run", url.Values{"token": {s.token}, "id": {"2023"}})
	if r.StatusCode != http.StatusOK {
		t.Errorf("정상 입력은 200 이어야 합니다: %d", r.StatusCode)
	}
}

func TestConcurrentRunRejected(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	s := New(func(in app.Input) (*app.Result, error) {
		close(started)
		<-release
		return fakeRun(in)
	})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		post(t, ts, "/run", url.Values{"token": {s.token}, "id": {"1"}})
	}()
	<-started
	if r := post(t, ts, "/run", url.Values{"token": {s.token}, "id": {"1"}}); r.StatusCode != http.StatusConflict {
		t.Errorf("실행 중 두 번째 요청은 409 이어야 합니다: %d", r.StatusCode)
	}
	close(release)
	wg.Wait()
}

func TestQuitClosesDone(t *testing.T) {
	s := New(fakeRun)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	post(t, ts, "/quit", url.Values{"token": {s.token}})
	select {
	case <-s.Done:
	case <-time.After(2 * time.Second):
		t.Fatal("종료 요청 후 Done 이 닫혀야 합니다")
	}
}

func TestClosingCanceledByNewRequestAndIdleTimeout(t *testing.T) {
	s := New(fakeRun)
	s.grace = 100 * time.Millisecond
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	post(t, ts, "/closing", url.Values{"token": {s.token}})
	http.Get(ts.URL + "/") // 새로고침처럼 곧바로 요청이 오면 종료가 취소된다
	select {
	case <-s.Done:
		t.Fatal("새 요청이 왔으므로 종료되면 안 됩니다")
	case <-time.After(300 * time.Millisecond):
	}
	post(t, ts, "/closing", url.Values{"token": {s.token}})
	select {
	case <-s.Done:
	case <-time.After(2 * time.Second):
		t.Fatal("창이 닫히면 종료되어야 합니다")
	}

	idle := New(fakeRun)
	idle.idle = 50 * time.Millisecond
	idle.touch()
	select {
	case <-idle.Done:
	case <-time.After(2 * time.Second):
		t.Fatal("유휴 시간이 지나면 종료되어야 합니다")
	}
}

func TestRejectsForeignHost(t *testing.T) {
	s := New(fakeRun)
	req := httptest.NewRequest("GET", "http://evil.example/", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("외부 Host 는 403 이어야 합니다: %d", rec.Code)
	}
}
