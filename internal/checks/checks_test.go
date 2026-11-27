package checks

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"dbchecker/internal/dataset"
	"dbchecker/internal/fakestudent"
)

func loadData(t testing.TB) *dataset.Dataset {
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
			items = append(items, map[string]any{"name": b.Title, "cost": b.Price})
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

	// 가격: 숫자와 여러 형태의 문자열을 모두 받고, 틀리거나 없으면 실패한다.
	withPrice := func(price any) []any {
		var items []any
		for i, b := range expected {
			p := price
			if i > 0 || price == nil {
				p = b.Price
			}
			if price == "none" && i == 0 {
				items = append(items, map[string]any{"name": b.Title})
				continue
			}
			items = append(items, map[string]any{"name": b.Title, "price": p})
		}
		return items
	}
	first := expected[0]
	priceCases := []struct {
		name  string
		price any
		pass  bool
	}{
		{"숫자", first.Price, true},
		{"문자열 숫자", fmt.Sprintf("%.2f", first.Price), true},
		{"파운드 기호", fmt.Sprintf("£%.2f", first.Price), true},
		{"인코딩 깨진 파운드", fmt.Sprintf("Â£%.2f", first.Price), true},
		{"다른 가격", first.Price + 1, false},
		{"0", 0.0, false},
		{"가격 없음", "none", false},
	}
	for _, c := range priceCases {
		res := judgeList(expected, ds.Books, withPrice(c.price))
		if res.Passed != c.pass {
			t.Errorf("가격 %s: 통과=%v 여야 합니다: %+v", c.name, c.pass, res)
		}
		if !c.pass && !strings.Contains(res.Detail, "가격") {
			t.Errorf("가격 %s: Detail 에 가격 문제를 적어야 합니다: %s", c.name, res.Detail)
		}
	}
}

// truncateWords 는 사이트 목록 페이지가 제목을 자르는 방식(앞 4단어 + " ...")이다.
func truncateWords(title string) string {
	words := strings.Fields(title)
	if len(words) <= 4 {
		return title
	}
	return strings.Join(words[:4], " ") + " ..."
}

// 정답 서버는 응답 순서나 제목 형태와 상관없이 항상 통과해야 한다.
// 제목이 같은 책(Fantasy 의 "The Star-Touched Queen" 두 권)과 잘린 제목이 겹치는 책
// ("Harry Potter and the ...")이 있어도 가격이 엇갈려 짝지어지면 안 된다.
func TestJudgeListPassesCorrectServerInAnyOrder(t *testing.T) {
	ds := loadData(t)
	orders := map[string]func(books []dataset.Book){
		"사이트 순서": func(books []dataset.Book) {},
		"가격 오름차순": func(books []dataset.Book) {
			sort.SliceStable(books, func(i, j int) bool { return books[i].Price < books[j].Price })
		},
		"가격 내림차순": func(books []dataset.Book) {
			sort.SliceStable(books, func(i, j int) bool { return books[i].Price > books[j].Price })
		},
		"제목순": func(books []dataset.Book) {
			sort.SliceStable(books, func(i, j int) bool { return books[i].Title < books[j].Title })
		},
	}
	titles := map[string]func(string) string{
		"전체 제목": func(title string) string { return title },
		"잘린 제목": truncateWords,
	}
	for _, category := range ds.Categories() {
		expected := ds.Query(category, 0, 1000)
		for orderName, reorder := range orders {
			for titleName, titleOf := range titles {
				served := append([]dataset.Book{}, expected...)
				reorder(served)
				var items []any
				for _, b := range served {
					items = append(items, map[string]any{"title": titleOf(b.Title), "price": b.Price, "category": b.Category})
				}
				res := judgeList(expected, ds.Books, items)
				if !res.Passed {
					t.Errorf("%s / %s / %s: 정답 서버가 실패했습니다: %s", category, orderName, titleName, res.Detail)
				}
			}
		}
	}
}

// A2 의 범위는 항상 minPrice < maxPrice 이고, 정답이 2권 이상이어야 한다 (경계값 검사 A3 와 겹치지 않게).
func TestRandomRangeIsAlwaysARealRange(t *testing.T) {
	ds := loadData(t)
	for seed := int64(0); seed < 2000; seed++ {
		env := &Env{Data: ds, Rand: rand.New(rand.NewSource(seed))}
		category, lo, hi := randomRange(env)
		if !(lo < hi) {
			t.Fatalf("시드 %d: %s minPrice=%.2f maxPrice=%.2f, minPrice < maxPrice 여야 합니다", seed, category, lo, hi)
		}
		if n := len(ds.Query(category, lo, hi)); n < 2 {
			t.Fatalf("시드 %d: %s %.2f~%.2f 의 정답이 %d권입니다. 2권 이상이어야 합니다", seed, category, lo, hi, n)
		}
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

// 가짜 서버 변형마다 실패해야 하는 검사만 정확히 실패하는지 본다. 시드를 바꿔 여러 번 돌린다.
func TestVariantsFailExactlyExpectedChecks(t *testing.T) {
	ds := loadData(t)
	for _, v := range fakestudent.Variants {
		t.Run(v.Name, func(t *testing.T) {
			srv := httptest.NewServer(fakestudent.Handler(ds, v.Options))
			defer srv.Close()
			want := map[string]bool{}
			for _, id := range v.ExpectFail {
				want[id] = true
			}
			for seed := int64(1); seed <= 5; seed++ {
				env := newEnv(t, srv.URL)
				env.Seed = seed
				env.Rand = rand.New(rand.NewSource(seed))
				for _, o := range RunAll(context.Background(), env) {
					if o.Check.Group == GroupDB {
						continue
					}
					if o.Passed() == want[o.Check.ID] {
						t.Errorf("시드 %d %s: 통과=%v 여야 하는데 %v (%s)", seed, o.Check.ID, !want[o.Check.ID], o.Passed(), o.Result.Detail)
					}
				}
			}
		})
	}
}

// 무작위 검사는 10건 중 일부만 틀려도 실패이고, 몇 건이 틀렸는지 알려 줘야 한다.
func TestRandomChecksRepeatAndCountFailures(t *testing.T) {
	correct := fakestudent.Handler(loadData(t), fakestudent.Options{})
	requests := 0
	// 짝수 번째 요청에만 빈 배열을 주는 서버 → 10건 중 5건 실패
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests%2 == 0 {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte("[]"))
			return
		}
		correct.ServeHTTP(w, r)
	}))
	defer srv.Close()
	env := newEnv(t, srv.URL)
	for _, check := range []func(context.Context, *Env) Result{checkA2, checkA3} {
		requests = 0
		res := check(context.Background(), env)
		if res.Passed || !strings.Contains(res.Detail, "10건 중 5건 실패") {
			t.Errorf("10건 중 5건 실패로 보고해야 합니다: %s", res.Detail)
		}
		if requests != 10 {
			t.Errorf("요청이 10번이어야 합니다: %d번", requests)
		}
	}
}

// 서버가 응답하지 않으면 10건을 다 기다리지 않고 첫 건에서 멈춘다.
func TestRandomChecksStopWhenServerDoesNotRespond(t *testing.T) {
	env := newEnv(t, "http://127.0.0.1:1") // 아무도 듣지 않는 포트
	res := checkA2(context.Background(), env)
	if res.Passed || !strings.Contains(res.Detail, "1건째에서 중단") {
		t.Errorf("첫 건에서 중단해야 합니다: %s", res.Detail)
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
			if c.name == "한 글자 다름" && (!strings.Contains(res.Detail, "기대") || !strings.Contains(res.Detail, "받음")) {
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
	outcomes := RunAll(context.Background(), newEnv(t, url))
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
	outcomes := RunAll(context.Background(), newEnv(t, srv.URL))
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
