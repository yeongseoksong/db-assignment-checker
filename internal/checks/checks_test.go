package checks

import (
	"context"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"dbchecker/internal/dataset"
	"dbchecker/internal/fakestudent"
)

func loadData(t *testing.T) *dataset.Dataset {
	t.Helper()
	ds, err := dataset.Load()
	if err != nil {
		t.Fatal(err)
	}
	return ds
}

func newEnv(t *testing.T, baseURL string) *Env {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	env := &Env{BaseURL: baseURL, Data: loadData(t), HTTP: NewHTTPClient(log), Log: log, Seed: 42, Rand: rand.New(rand.NewSource(42))}
	return env
}

func passedMap(outcomes []Outcome) map[string]bool {
	m := map[string]bool{}
	for _, o := range outcomes {
		m[o.Check.ID] = o.Passed()
	}
	return m
}

func countPassed(outcomes []Outcome) int {
	n := 0
	for _, o := range outcomes {
		if o.Passed() {
			n++
		}
	}
	return n
}

func TestRegistryHasUniqueIDs(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range All {
		if seen[c.ID] {
			t.Fatalf("검사 ID %s 가 중복되었습니다", c.ID)
		}
		seen[c.ID] = true
	}
}

func TestNormalizeAndMatch(t *testing.T) {
	expected := "Full Moon over Noah’s Ark: An Odyssey to Mount Ararat and Beyond"
	cases := []struct {
		got  string
		want bool
	}{
		{expected, true},
		{"  FULL MOON  over Noah’s Ark: An Odyssey to Mount Ararat and Beyond ", true},
		{"Full Moon over Noah’s Ark...", true},
		{"Full Moon over Noah&#8217;s Ark: An Odyssey to Mount Ararat and Beyond", true},
		{"Full Moon", false}, // 10자 미만 접두사
		{"Full Moon over Noah’s Ark: An Odyssey to Mount Ararat and Beyond!", false},
		{"Travel", false},
	}
	for _, c := range cases {
		if got := titleMatches(c.got, expected); got != c.want {
			t.Errorf("titleMatches(%q) = %v, 기대 %v", c.got, got, c.want)
		}
	}
}

func TestJudgeList(t *testing.T) {
	ds := loadData(t)
	expected := ds.Query("Travel", 0, 100)
	if len(expected) < 3 {
		t.Fatal("테스트 전제: Travel 에 책이 3권 이상 있어야 합니다")
	}
	toItems := func(books []dataset.Book) []any {
		var items []any
		for _, b := range books {
			items = append(items, map[string]any{"name": b.Title, "x": 1.0})
		}
		return items
	}
	if res := judgeList(expected, ds.Books, toItems(expected)); !res.Passed {
		t.Errorf("완전 일치는 통과해야 합니다: %+v", res)
	}
	res := judgeList(expected, ds.Books, toItems(expected[:len(expected)-1]))
	if res.Passed || !strings.Contains(res.Detail, expected[len(expected)-1].Title) {
		t.Errorf("누락은 실패하고 누락 제목을 적어야 합니다: %+v", res)
	}
	if res := judgeList(expected, ds.Books, nil); res.Passed {
		t.Errorf("빈 배열은 실패해야 합니다: %+v", res)
	}
	other := ds.Query("Poetry", 0, 100)[:1]
	res = judgeList(expected, ds.Books, toItems(append(append([]dataset.Book{}, expected...), other...)))
	if res.Passed || !strings.Contains(res.Detail, "불필요") || !strings.Contains(res.Detail, "범위 밖") {
		t.Errorf("불필요한 원소는 실패하고 Detail 에 적어야 합니다: %+v", res)
	}
	dup := append(append([]dataset.Book{}, expected...), expected[0])
	if res := judgeList(expected, ds.Books, toItems(dup)); res.Passed {
		t.Errorf("중복 원소는 실패해야 합니다: %+v", res)
	}
}

func TestExtractItems(t *testing.T) {
	if _, err := extractItems([]byte(`{"books":[{"a":"b"}]}`)); err != nil {
		t.Errorf("객체로 감싼 배열은 허용해야 합니다: %v", err)
	}
	if _, err := extractItems([]byte(`{"message":"x"}`)); err == nil {
		t.Error("배열이 없는 객체는 거부해야 합니다")
	}
	if _, err := extractItems([]byte(`<html>`)); err == nil {
		t.Error("JSON 이 아니면 거부해야 합니다")
	}
}

func TestCorrectServerPassesAllDynamicChecks(t *testing.T) {
	variants := map[string]fakestudent.Options{
		"기본":     {},
		"객체로 감쌈": {WrapInObject: true, TitleField: "bookTitle"},
		"제목 잘림":  {TruncateTitles: true, TitleField: "name"},
	}
	for name, opt := range variants {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(fakestudent.Handler(loadData(t), opt))
			defer srv.Close()
			outcomes := RunAll(context.Background(), newEnv(t, srv.URL), nil)
			for _, o := range outcomes {
				if (o.Check.Group == GroupAPI || o.Check.Group == GroupErrors) && !o.Passed() {
					t.Errorf("%s 가 통과하지 못했습니다: %+v", o.Check.ID, o.Result)
				}
			}
		})
	}
}

func TestWrongServerFails(t *testing.T) {
	srv := httptest.NewServer(fakestudent.Handler(loadData(t), fakestudent.Options{ExcludeBoundary: true, WrongMessage: true}))
	defer srv.Close()
	p := passedMap(RunAll(context.Background(), newEnv(t, srv.URL), nil))
	if p["A3"] {
		t.Error("경계 미포함은 A3 실패여야 합니다")
	}
	if p["E1"] || p["E2"] {
		t.Error("메시지가 다르면 E 검사는 실패여야 합니다")
	}
}

func messageServer(body string, status int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
}

func TestMessageChecksExactMatch(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name   string
		body   string
		status int
		want   bool
	}{
		{"정확한 문구", `{"message":"` + msgNoCategory + `"}`, 404, true},
		{"상태코드 무관", `{"message":"` + msgNoCategory + `"}`, 200, true},
		{"앞뒤 공백 허용", `{"message":"  ` + msgNoCategory + ` "}`, 400, true},
		{"한 글자 다름", `{"message":"존재하지 않는 카테고리입니다."}`, 404, false},
		{"message 키 없음", `{"error":"` + msgNoCategory + `"}`, 404, false},
		{"JSON 아님", `존재하지 않는 카테고리입니다`, 404, false},
		{"배열", `["` + msgNoCategory + `"]`, 404, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := messageServer(c.body, c.status)
			defer srv.Close()
			res := checkE1(ctx, newEnv(t, srv.URL))
			if res.Passed != c.want {
				t.Errorf("통과=%v 기대 %v (%s)", res.Passed, c.want, res.Detail)
			}
			if c.name == "한 글자 다름" && !(strings.Contains(res.Detail, "기대") && strings.Contains(res.Detail, "받음")) {
				t.Errorf("실패 Detail 에 기대/받음 문구가 있어야 합니다: %s", res.Detail)
			}
		})
	}
	srv := messageServer(`{"message":"`+msgPriceRange+`"}`, 400)
	defer srv.Close()
	if res := checkE2(ctx, newEnv(t, srv.URL)); !res.Passed {
		t.Errorf("E2 정확한 문구는 통과해야 합니다: %+v", res)
	}
	srv2 := messageServer(`{"message":"`+msgNoCategory+`"}`, 400)
	defer srv2.Close()
	if res := checkE2(ctx, newEnv(t, srv2.URL)); res.Passed {
		t.Error("E2 에 E1 문구는 실패여야 합니다")
	}
}

func TestServerDown(t *testing.T) {
	srv := httptest.NewServer(nil)
	url := srv.URL
	srv.Close()
	outcomes := RunAll(context.Background(), newEnv(t, url), nil)
	p := passedMap(outcomes)
	for _, id := range []string{"A1", "A2", "A3", "E1", "E2"} {
		if p[id] {
			t.Errorf("%s: 서버가 꺼져 있으면 실패여야 합니다", id)
		}
	}
	if d := outcomes[len(outcomes)-1].Result.Detail; !strings.Contains(d, "Spring Boot 앱을 먼저 실행하세요") {
		t.Errorf("친절한 안내가 필요합니다: %s", d)
	}
}

func TestNoDBAndNoServerStillRunEveryCheck(t *testing.T) {
	srv := httptest.NewServer(fakestudent.Handler(loadData(t), fakestudent.Options{}))
	defer srv.Close()
	outcomes := RunAll(context.Background(), newEnv(t, srv.URL), nil)
	if len(outcomes) != len(All) {
		t.Fatalf("모든 검사가 결과를 내야 합니다: %d/%d", len(outcomes), len(All))
	}
	for _, o := range outcomes {
		if o.Check.Group == GroupDB && (o.Passed() || o.Result.Detail == "") {
			t.Errorf("%s: 연결 문자열이 없으면 안내와 함께 실패여야 합니다: %+v", o.Check.ID, o.Result)
		}
	}
	if n := countPassed(outcomes); n != 5 {
		t.Errorf("동적 검사 5개만 통과해야 합니다: %d", n)
	}
}
