// Package fakestudent 는 테스트용 가짜 학생 서버다. 명세대로 응답하는 정답 서버와,
// 일부러 틀리게 응답하는 변형을 Options 로 만든다. 자주 쓰는 조합은 Variants 표에 이름을 붙여 둔다.
package fakestudent

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"

	"dbchecker/internal/dataset"
)

// Options 는 서버가 응답하는 방식이다. 모두 기본값이면 정답 서버다.
type Options struct {
	// 틀린 동작
	ExcludeBoundary bool // 가격 비교를 > 와 < 로 해서 경계값을 빠뜨린다
	WrongMessage    bool // 오류 메시지 끝에 "!" 를 붙인다
	NoErrorHandling bool // 없는 카테고리, minPrice > maxPrice 에 오류 대신 200 과 빈 배열을 준다
	IgnoreCategory  bool // 카테고리로 거르지 않고 가격 범위만 본다
	OmitPrice       bool // 응답에 가격을 넣지 않는다
	FloorPrice      bool // 가격을 정수로 잘라서 준다 (45.17 → 45)

	// 명세 안에서 형태만 다른 동작 (채점기가 받아 줘야 한다)
	WrapInObject   bool   // {"books":[...]} 로 감싼다
	TruncateTitles bool   // 제목을 25자로 자르고 ... 를 붙인다
	TitleField     string // 제목 필드명 (기본 "title")
	PriceAsString  bool   // 가격을 "£36.94" 같은 문자열로 준다
}

// Variant 는 이름 붙인 가짜 서버 설정과, 그 서버에서 실패해야 하는 검사 ID 다.
type Variant struct {
	Name        string
	Description string
	Options     Options
	ExpectFail  []string // API·오류처리 검사 중 실패해야 하는 것 (DB 검사는 가짜 서버와 무관)
}

// Variants 는 가짜 서버 변형 표다. testdata/fakeserver 의 -bug 플래그와 테스트가 함께 쓴다.
var Variants = []Variant{
	{"correct", "명세대로 응답하는 정답 서버", Options{}, nil},
	{"wrapped", "정답. 응답을 {\"books\":[...]} 로 감싸고 제목 필드명이 bookTitle", Options{WrapInObject: true, TitleField: "bookTitle"}, nil},
	{"truncated", "정답. 목록 페이지처럼 잘린 제목(...)을 저장", Options{TruncateTitles: true, TitleField: "name"}, nil},
	{"price-string", "정답. 가격을 \"£36.94\" 문자열로 줌", Options{PriceAsString: true}, nil},
	{"boundary", "가격 비교를 >, < 로 해서 경계값 책이 빠짐", Options{ExcludeBoundary: true}, []string{"A2", "A3"}},
	{"no-category-filter", "카테고리로 거르지 않고 가격 범위만 봄", Options{IgnoreCategory: true}, []string{"A1", "A2"}},
	{"no-price", "응답에 가격이 없음", Options{OmitPrice: true}, []string{"A1", "A2"}},
	{"floor-price", "가격을 정수로 잘라서 줌 (45.17 → 45)", Options{FloorPrice: true}, []string{"A1", "A2"}},
	{"wrong-message", "오류 메시지 문구가 명세와 다름", Options{WrongMessage: true}, []string{"E1", "E2"}},
	{"no-error", "오류 상황에서도 200 과 빈 배열을 줌", Options{NoErrorHandling: true}, []string{"E1", "E2"}},
}

// FindVariant 는 이름으로 변형을 찾는다.
func FindVariant(name string) (Variant, bool) {
	for _, v := range Variants {
		if v.Name == name {
			return v, true
		}
	}
	return Variant{}, false
}

type bookJSON map[string]any

// Handler 는 /books 를 처리하는 http.Handler 다.
func Handler(ds *dataset.Dataset, opt Options) http.Handler {
	field := opt.TitleField
	if field == "" {
		field = "title"
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/books", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		category := q.Get("category")
		lo, err1 := strconv.ParseFloat(q.Get("minPrice"), 64)
		hi, err2 := strconv.ParseFloat(q.Get("maxPrice"), 64)
		if !ds.HasCategory(category) {
			if opt.NoErrorHandling {
				writeBooks(w, opt, nil)
				return
			}
			writeMessage(w, http.StatusNotFound, pick(opt.WrongMessage, "존재하지 않는 카테고리입니다"))
			return
		}
		if err1 != nil || err2 != nil || lo > hi {
			if opt.NoErrorHandling {
				writeBooks(w, opt, nil)
				return
			}
			writeMessage(w, http.StatusBadRequest, pick(opt.WrongMessage, "minPrice가 maxPrice보다 높아서는 안됩니다"))
			return
		}
		var out []bookJSON
		for _, b := range ds.Books {
			if !opt.IgnoreCategory && b.Category != category {
				continue
			}
			if opt.ExcludeBoundary {
				if b.Price <= lo+1e-9 || b.Price >= hi-1e-9 {
					continue
				}
			} else if b.Price < lo-1e-9 || b.Price > hi+1e-9 {
				continue
			}
			out = append(out, toJSON(b, opt, field))
		}
		writeBooks(w, opt, out)
	})
	return mux
}

func toJSON(b dataset.Book, opt Options, field string) bookJSON {
	title := b.Title
	if opt.TruncateTitles && len([]rune(title)) > 25 {
		title = string([]rune(title)[:25]) + "..."
	}
	book := bookJSON{field: title, "category": b.Category}
	if opt.OmitPrice {
		return book
	}
	var price any = b.Price
	if opt.FloorPrice {
		price = math.Floor(b.Price)
	}
	if opt.PriceAsString {
		price = fmt.Sprintf("£%.2f", b.Price)
	}
	book["price"] = price
	return book
}

func writeBooks(w http.ResponseWriter, opt Options, books []bookJSON) {
	if books == nil {
		books = []bookJSON{}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if opt.WrapInObject {
		_ = json.NewEncoder(w).Encode(map[string]any{"books": books})
		return
	}
	_ = json.NewEncoder(w).Encode(books)
}

func pick(wrong bool, right string) string {
	if wrong {
		return right + "!"
	}
	return right
}

func writeMessage(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"message": msg})
}
