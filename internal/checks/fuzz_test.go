package checks

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"dbchecker/internal/dataset"
)

// FuzzJudgeAnyResponse 는 학생 서버가 보낼 수 있는 아무 바이트를 판정 코드에 넣는다.
// 어떤 응답이 와도 패닉이 나면 안 된다 (패닉이 나면 채점기 전체가 죽고 제출 파일도 생기지 않는다).
func FuzzJudgeAnyResponse(f *testing.F) {
	ds := loadData(f)
	expected := ds.Query("Travel", 10, 30)
	seeds := []string{
		`[]`, `{}`, `null`, `"x"`, `123`, `[1,2,3]`, `[null]`, `[[[]]]`,
		`{"books":[{"title":"It's Only the Himalayas","price":45.17}]}`,
		`{"a":[],"b":[]}`,
		`[{"title":"1,000 Places to See Before You Die","price":"£26.08"}]`,
		`[{"title":"The Road to Little Dribbling: Adventures of an American in Britain (Notes From a Small Island #2)","price":23.21}]`,
		`[{"title":"...","price":"Â£"}]`, `[{"t":{"u":["x",{"v":1e308}]}}]`,
		`[{"title":"` + "\u0000�\U0010FFFF" + `","price":-0}]`,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		items, err := extractItems(body)
		if err != nil {
			return // JSON 이 아니면 친절한 오류로 끝나면 된다
		}
		judgeList(expected, ds.Books, items)
		for _, item := range items {
			var candidates []string
			stringValues(item, &candidates)
			matchIndex(candidates, expected, nil)
			hasPrice(item, 23.21)
		}
	})
}

// FuzzCorrectResponseAlwaysPasses 는 정답 응답을 무작위로 모양만 바꿔도 항상 통과하는지 본다.
// 퍼저가 고르는 것: 카테고리·가격 범위, 응답 순서, 제목 형태, 가격 형태, 필드 이름, 감싸기 여부.
func FuzzCorrectResponseAlwaysPasses(f *testing.F) {
	ds := loadData(f)
	f.Add(uint16(0), uint16(0), uint16(5), int64(1), uint8(0), uint8(0), false)
	f.Add(uint16(17), uint16(3), uint16(40), int64(42), uint8(1), uint8(1), true)
	f.Add(uint16(30), uint16(9), uint16(9), int64(7), uint8(2), uint8(2), false)
	f.Fuzz(func(t *testing.T, catIdx, loIdx, hiIdx uint16, order int64, titleMode, priceMode uint8, wrap bool) {
		cats := ds.Categories()
		category := cats[int(catIdx)%len(cats)]
		var prices []float64
		for _, b := range ds.Books {
			if b.Category == category {
				prices = append(prices, b.Price)
			}
		}
		lo, hi := prices[int(loIdx)%len(prices)], prices[int(hiIdx)%len(prices)]
		if lo > hi {
			lo, hi = hi, lo
		}
		expected := ds.Query(category, lo, hi)

		served := append([]dataset.Book{}, expected...)
		rand.New(rand.NewSource(order)).Shuffle(len(served), func(i, j int) { served[i], served[j] = served[j], served[i] })
		var books []any
		for i, b := range served {
			title := b.Title
			switch titleMode % 3 {
			case 1:
				title = truncateWords(b.Title)
			case 2:
				title = "  " + b.Title + "  " // 앞뒤 공백
			}
			var price any = b.Price
			switch priceMode % 3 {
			case 1:
				price = fmt.Sprintf("%.2f", b.Price)
			case 2:
				price = fmt.Sprintf("£%.2f", b.Price)
			}
			field := []string{"title", "name", "bookTitle"}[i%3]
			books = append(books, map[string]any{field: title, "price": price, "category": b.Category, "id": i})
		}
		var payload any = books
		if wrap {
			payload = map[string]any{"books": books}
		}
		if books == nil && !wrap {
			payload = []any{}
		}
		body, _ := json.Marshal(payload)
		items, err := extractItems(body)
		if err != nil {
			t.Fatalf("정답 응답을 읽지 못했습니다: %v", err)
		}
		res := judgeList(expected, ds.Books, items)
		if !res.Passed {
			t.Fatalf("%s %.2f~%.2f 정답 응답이 실패했습니다: %s", category, lo, hi, res.Detail)
		}
	})
}

// 판정 중 패닉이 나도 채점기는 죽지 않고 그 검사만 실패로 기록해야 한다.
func TestPanickingCheckIsRecordedNotFatal(t *testing.T) {
	saved := All
	defer func() { All = saved }()
	All = []Check{
		{"X1", GroupAPI, "패닉 나는 검사", func(ctx context.Context, env *Env) Result { panic("판정 버그") }},
		{"X2", GroupAPI, "다음 검사", func(ctx context.Context, env *Env) Result { return Result{Passed: true} }},
	}
	env := newEnv(t, "http://127.0.0.1:1")
	env.BaseURL = ""
	outcomes := runChecks(context.Background(), env, nil, nil)
	if len(outcomes) != 2 || outcomes[0].Passed() || !strings.Contains(outcomes[0].Result.Detail, "채점기 내부 오류") || !outcomes[1].Passed() {
		t.Fatalf("패닉은 해당 검사 실패로 기록하고 다음 검사로 넘어가야 합니다: %+v", outcomes)
	}
}
