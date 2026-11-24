package checks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"dbchecker/internal/dataset"
)

const (
	msgNoCategory  = "존재하지 않는 카테고리입니다"
	msgPriceRange  = "minPrice가 maxPrice보다 높아서는 안됩니다"
	minPrefixLen   = 10 // 잘린 제목을 허용할 때 필요한 최소 글자 수
	maxTitlesShown = 5
	priceTolerance = 0.005 // 소수점 둘째 자리까지 같으면 같은 가격
)

// response 는 서버 응답의 상태코드와 본문이다.
type response struct {
	Status int
	Body   []byte
}

// fetch 는 BaseURL+path 로 GET 요청을 보낸다. 쿼리는 url.Values 로 인코딩한다.
func fetch(ctx context.Context, env *Env, path string, query url.Values) (response, error) {
	target := strings.TrimRight(env.BaseURL, "/") + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return response{}, fmt.Errorf("URL %q 이 올바르지 않습니다. -url 값을 확인하세요: %w", env.BaseURL, err)
	}
	resp, err := env.HTTP.Do(req)
	if err != nil {
		return response{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return response{}, err
	}
	return response{Status: resp.StatusCode, Body: body}, nil
}

func booksQuery(category string, minPrice, maxPrice float64) url.Values {
	q := url.Values{}
	q.Set("category", category)
	q.Set("minPrice", formatPrice(minPrice))
	q.Set("maxPrice", formatPrice(maxPrice))
	return q
}

func formatPrice(p float64) string {
	return fmt.Sprintf("%.2f", p)
}

// normalizeTitle 은 비교용으로 제목을 소문자화하고 엔티티·공백·끝의 "..." 를 정리한다.
func normalizeTitle(s string) string {
	s = html.UnescapeString(s)
	s = strings.ToLower(strings.Join(strings.Fields(s), " "))
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(s, "..."), "…"))
	return s
}

// titleMatches 는 학생 값이 기대 제목과 같거나, 기대 제목의 (10자 이상) 접두사이면 true 다.
func titleMatches(studentValue, expectedTitle string) bool {
	return matchNormalized(normalizeTitle(studentValue), normalizeTitle(expectedTitle))
}

// matchNormalized 는 이미 정규화된 두 제목을 titleMatches 규칙으로 비교한다.
func matchNormalized(got, want string) bool {
	if got == want {
		return true
	}
	return len(got) >= minPrefixLen && strings.HasPrefix(want, got)
}

// extractItems 는 JSON 응답에서 원소 배열을 꺼낸다.
// 배열이거나, 배열 값이 정확히 하나인 객체({"books":[...]})를 허용한다.
func extractItems(body []byte) ([]any, error) {
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return nil, fmt.Errorf("응답이 JSON 이 아닙니다 (%s)", shorten(string(body), 80))
	}
	switch t := v.(type) {
	case []any:
		return t, nil
	case map[string]any:
		var arrays [][]any
		for _, field := range t {
			if arr, ok := field.([]any); ok {
				arrays = append(arrays, arr)
			}
		}
		if len(arrays) == 1 {
			return arrays[0], nil
		}
		return nil, fmt.Errorf("응답이 JSON 배열이어야 합니다 (객체이고 배열 필드가 %d개: %s)", len(arrays), shorten(string(body), 80))
	}
	return nil, fmt.Errorf("응답이 JSON 배열이어야 합니다 (%s)", shorten(string(body), 80))
}

// stringValues 는 값 안의 모든 문자열을 모은다. 필드명은 학생마다 달라서 보지 않는다.
func stringValues(v any, out *[]string) {
	switch t := v.(type) {
	case string:
		*out = append(*out, t)
	case map[string]any:
		for _, field := range t {
			stringValues(field, out)
		}
	case []any:
		for _, field := range t {
			stringValues(field, out)
		}
	}
}

// priceValues 는 값 안에서 가격으로 읽을 수 있는 것을 모두 모은다. 필드명은 보지 않는다.
// 숫자(36.94)와 문자열("36.94", "£36.94", "Â£36.94") 둘 다 받는다.
func priceValues(v any, out *[]float64) {
	switch t := v.(type) {
	case float64: // JSON 숫자는 Go 에서 항상 float64 로 읽힌다
		*out = append(*out, t)
	case string:
		price, ok := parsePrice(t)
		if ok {
			*out = append(*out, price)
		}
	case map[string]any:
		for _, field := range t {
			priceValues(field, out)
		}
	case []any:
		for _, field := range t {
			priceValues(field, out)
		}
	}
}

// parsePrice 는 "£36.94" 같은 문자열에서 통화 기호와 공백을 떼고 숫자로 읽는다.
// jsoup 으로 읽은 "£" 가 인코딩 문제로 "Â£" 가 되는 경우도 받는다.
func parsePrice(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	s = strings.TrimLeft(s, "£Â$€₩ ")
	price, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return price, true
}

// hasPrice 는 원소 안에 want 와 같은 가격 값이 있으면 true 다.
func hasPrice(item any, want float64) bool {
	var prices []float64
	priceValues(item, &prices)
	for _, p := range prices {
		if math.Abs(p-want) < priceTolerance {
			return true
		}
	}
	return false
}

// listVerdict 는 응답 목록을 기대 목록과 비교한 결과다.
type listVerdict struct {
	Expected   int
	Missing    []string
	Extra      []string // 중복·범위 밖·알 수 없는 원소 (설명 문자열)
	WrongPrice []string // 제목은 맞지만 가격 값이 없거나 다른 책 (설명 문자열)
}

func (v listVerdict) exact() bool {
	return len(v.Missing) == 0 && len(v.Extra) == 0 && len(v.WrongPrice) == 0
}

// judgeList 는 응답 원소가 기대 책 목록과 정확히 같은 집합일 때만 통과시킨다.
func judgeList(expected []dataset.Book, all []dataset.Book, items []any) Result {
	v := listVerdict{Expected: len(expected)}
	allCandidates := make([][]string, len(items))
	for i, item := range items {
		stringValues(item, &allCandidates[i])
	}
	assigned, used := assignBooks(expected, items, allCandidates)
	for i, item := range items {
		candidates := allCandidates[i]
		idx := assigned[i]
		switch {
		case idx >= 0:
			book := expected[idx]
			if !hasPrice(item, book.Price) {
				v.WrongPrice = append(v.WrongPrice, fmt.Sprintf("%q(기대 %.2f)", shorten(book.Title, 40), book.Price))
			}
		case matchIndex(candidates, expected, nil) >= 0:
			v.Extra = append(v.Extra, "중복: "+firstValue(candidates))
		case matchesAny(candidates, all):
			v.Extra = append(v.Extra, "범위 밖: "+firstValue(candidates))
		default:
			v.Extra = append(v.Extra, "알 수 없음: "+firstValue(candidates))
		}
	}
	for i, b := range expected {
		if !used[i] {
			v.Missing = append(v.Missing, b.Title)
		}
	}
	if v.exact() {
		return Result{Passed: true, Detail: fmt.Sprintf("기대한 %d권과 정확히 일치", v.Expected)}
	}
	return Result{Detail: v.describe()}
}

func firstValue(candidates []string) string {
	if len(candidates) == 0 {
		return "(문자열 값 없음)"
	}
	return fmt.Sprintf("%q", shorten(candidates[0], 40))
}

// assignBooks 는 응답 원소마다 기대 책을 하나씩 배정한다.
// assigned[i] 는 items[i] 에 배정된 expected 의 위치(없으면 -1)이고, used[j] 는 expected[j] 가 배정되었는지다.
// 한 원소의 제목이 여러 책과 맞을 수 있다(제목이 같은 책 두 권, "Harry Potter and the ..." 같은 잘린 제목).
// 제목만 보고 앞에서부터 배정하면 가격이 엇갈리므로, 제목과 가격이 모두 맞는 책을 먼저 배정하고
// 남은 원소만 제목으로 배정한다.
func assignBooks(expected []dataset.Book, items []any, allCandidates [][]string) (assigned []int, used []bool) {
	assigned = make([]int, len(items))
	used = make([]bool, len(expected))
	for i := range assigned {
		assigned[i] = -1
	}
	for i, item := range items {
		for j, book := range expected {
			if !used[j] && anyTitleMatches(allCandidates[i], book.Title) && hasPrice(item, book.Price) {
				assigned[i] = j
				used[j] = true
				break
			}
		}
	}
	for i := range items {
		if assigned[i] >= 0 {
			continue
		}
		j := matchIndex(allCandidates[i], expected, used)
		if j >= 0 {
			assigned[i] = j
			used[j] = true
		}
	}
	return assigned, used
}

// anyTitleMatches 는 후보 문자열 중 하나라도 제목과 맞으면 true 다.
func anyTitleMatches(candidates []string, title string) bool {
	for _, c := range candidates {
		if titleMatches(c, title) {
			return true
		}
	}
	return false
}

// matchIndex 는 후보 문자열 중 하나라도 맞는 첫 기대 책의 위치를 돌려준다. used 가 true 인 책은 건너뛴다.
func matchIndex(candidates []string, expected []dataset.Book, used []bool) int {
	for i, b := range expected {
		if used != nil && used[i] {
			continue
		}
		if anyTitleMatches(candidates, b.Title) {
			return i
		}
	}
	return -1
}

func matchesAny(candidates []string, books []dataset.Book) bool {
	return matchIndex(candidates, books, nil) >= 0
}

func (v listVerdict) describe() string {
	parts := []string{fmt.Sprintf("기대 %d권과 다릅니다", v.Expected)}
	if len(v.Missing) > 0 {
		parts = append(parts, fmt.Sprintf("누락 %d권: %s", len(v.Missing), strings.Join(quoteAll(limit(v.Missing)), ", ")))
	}
	if len(v.Extra) > 0 {
		parts = append(parts, fmt.Sprintf("불필요 %d개: %s", len(v.Extra), strings.Join(limit(v.Extra), ", ")))
	}
	if len(v.WrongPrice) > 0 {
		parts = append(parts, fmt.Sprintf("가격이 없거나 다름 %d권: %s", len(v.WrongPrice), strings.Join(limit(v.WrongPrice), ", ")))
	}
	return strings.Join(parts, "; ")
}

func limit(ss []string) []string {
	if len(ss) > maxTitlesShown {
		return ss[:maxTitlesShown]
	}
	return ss
}

func quoteAll(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = fmt.Sprintf("%q", s)
	}
	return out
}

func shorten(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}

// queryBooks 는 /books 를 호출해 원소 배열을 돌려준다. 200 이 아니거나 배열이 아니면 사유를 Result 로 돌려준다.
func queryBooks(ctx context.Context, env *Env, category string, minPrice, maxPrice float64) ([]any, *Result) {
	resp, err := fetch(ctx, env, "/books", booksQuery(category, minPrice, maxPrice))
	if err != nil {
		return nil, &Result{Detail: "요청 실패: " + err.Error()}
	}
	if resp.Status != http.StatusOK {
		return nil, &Result{Detail: fmt.Sprintf("상태코드 %d (200 이어야 합니다): %s", resp.Status, shorten(string(resp.Body), 80))}
	}
	items, err := extractItems(resp.Body)
	if err != nil {
		return nil, &Result{Detail: err.Error()}
	}
	return items, nil
}

// runListCase 는 A 검사 공통 흐름이다. 범위의 기대 목록과 정확히 같은지 본다.
func runListCase(ctx context.Context, env *Env, category string, minPrice, maxPrice float64, prefix string) Result {
	expected := env.Data.Query(category, minPrice, maxPrice)
	items, failure := queryBooks(ctx, env, category, minPrice, maxPrice)
	if failure != nil {
		failure.Detail = prefix + failure.Detail
		return *failure
	}
	res := judgeList(expected, env.Data.Books, items)
	res.Detail = prefix + res.Detail
	return res
}

func checkA1(ctx context.Context, env *Env) Result {
	return runListCase(ctx, env, "Travel", 10, 30, "category=Travel minPrice=10 maxPrice=30: ")
}

// randomRange 는 임의 카테고리에서 서로 다른 가격 두 개를 골라 lo < hi 인 범위를 만든다.
// 같은 가격을 두 번 고르면 minPrice=maxPrice 가 되어 A3(경계값)와 같은 검사가 되므로,
// 가격이 두 종류 이상인 카테고리에서만 고른다.
func randomRange(env *Env) (category string, lo, hi float64) {
	pricesByCategory := map[string][]float64{}
	for _, b := range env.Data.Books {
		prices := pricesByCategory[b.Category]
		if !slices.Contains(prices, b.Price) {
			pricesByCategory[b.Category] = append(prices, b.Price)
		}
	}
	var eligible []string
	for _, c := range env.Data.Categories() { // 정렬된 순서라 시드가 같으면 결과도 같다
		if len(pricesByCategory[c]) >= 2 {
			eligible = append(eligible, c)
		}
	}
	category = eligible[env.Rand.Intn(len(eligible))]
	prices := pricesByCategory[category]
	i := env.Rand.Intn(len(prices))
	j := env.Rand.Intn(len(prices) - 1)
	if j >= i {
		j++ // i 를 건너뛰어 서로 다른 위치를 고른다
	}
	lo, hi = prices[i], prices[j]
	if lo > hi {
		lo, hi = hi, lo
	}
	return category, lo, hi
}

func checkA2(ctx context.Context, env *Env) Result {
	category, lo, hi := randomRange(env)
	prefix := fmt.Sprintf("[시드 %d] category=%s minPrice=%s maxPrice=%s: ", env.Seed, category, formatPrice(lo), formatPrice(hi))
	return runListCase(ctx, env, category, lo, hi, prefix)
}

func checkA3(ctx context.Context, env *Env) Result {
	book := env.Data.Books[env.Rand.Intn(len(env.Data.Books))]
	prefix := fmt.Sprintf("[시드 %d] category=%s 가격=%s 책=%q: ", env.Seed, book.Category, formatPrice(book.Price), book.Title)
	items, failure := queryBooks(ctx, env, book.Category, book.Price, book.Price)
	if failure != nil {
		failure.Detail = prefix + failure.Detail
		return *failure
	}
	for _, item := range items {
		var candidates []string
		stringValues(item, &candidates)
		if matchIndex(candidates, []dataset.Book{book}, nil) >= 0 {
			return Result{Passed: true, Detail: prefix + "경계 가격의 책이 포함됨"}
		}
	}
	return Result{Detail: prefix + "minPrice=maxPrice 일 때 그 가격의 책이 빠졌습니다. 가격 비교를 >= 와 <= 로 하세요"}
}

// checkMessage 는 오류 응답 본문이 JSON 객체이고 message 가 문구와 정확히 같은지 본다.
// 앞뒤 공백만 걷고 바이트 그대로 비교한다. 상태코드는 로그에만 남긴다.
func checkMessage(ctx context.Context, env *Env, category string, minPrice, maxPrice float64, want string) Result {
	resp, err := fetch(ctx, env, "/books", booksQuery(category, minPrice, maxPrice))
	if err != nil {
		return Result{Detail: "요청 실패: " + err.Error()}
	}
	env.Log.Info("오류 응답 상태코드(통과 여부와 무관)", "status", resp.Status)
	var obj map[string]any
	if err := json.Unmarshal(resp.Body, &obj); err != nil {
		return Result{Detail: fmt.Sprintf("상태코드 %d, 본문이 JSON 객체가 아닙니다: %s", resp.Status, shorten(string(resp.Body), 80))}
	}
	got, ok := obj["message"].(string)
	if !ok {
		return Result{Detail: fmt.Sprintf("상태코드 %d, JSON 에 문자열 message 필드가 없습니다: %s", resp.Status, shorten(string(resp.Body), 80))}
	}
	if !bytes.Equal([]byte(strings.TrimSpace(got)), []byte(want)) {
		return Result{Detail: fmt.Sprintf("기대 %q, 받음 %q (상태코드 %d)", want, got, resp.Status)}
	}
	return Result{Passed: true, Detail: fmt.Sprintf("message 일치 (상태코드 %d)", resp.Status)}
}

func checkE1(ctx context.Context, env *Env) Result {
	category := fmt.Sprintf("NoSuch-%08x", env.Rand.Uint32())
	return checkMessage(ctx, env, category, 10, 30, msgNoCategory)
}

func checkE2(ctx context.Context, env *Env) Result {
	cats := env.Data.Categories()
	category := cats[env.Rand.Intn(len(cats))]
	return checkMessage(ctx, env, category, 30, 10, msgPriceRange)
}
