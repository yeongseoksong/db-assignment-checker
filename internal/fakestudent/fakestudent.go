// Package fakestudent 는 테스트용 가짜 학생 서버다. 명세대로 응답하는 정답 서버와,
// 일부러 틀리게 응답하는 변형을 Options 로 만든다.
package fakestudent

import (
	"encoding/json"
	"net/http"
	"strconv"

	"dbchecker/internal/dataset"
)

// Options 는 서버가 일부러 틀리는 방식이다. 모두 false 면 정답 서버다.
type Options struct {
	ExcludeBoundary bool   // 가격 경계값(이상/이하)을 포함하지 않는다
	WrongMessage    bool   // 오류 메시지를 다르게 쓴다
	WrapInObject    bool   // {"books":[...]} 로 감싼다
	TruncateTitles  bool   // 제목을 25자로 자르고 ... 를 붙인다
	TitleField      string // 제목 필드명 (기본 "title")
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
			writeMessage(w, http.StatusNotFound, pick(opt.WrongMessage, "존재하지 않는 카테고리입니다"))
			return
		}
		if err1 != nil || err2 != nil || lo > hi {
			writeMessage(w, http.StatusBadRequest, pick(opt.WrongMessage, "minPrice가 maxPrice보다 높아서는 안됩니다"))
			return
		}
		var out []bookJSON
		for _, b := range ds.Query(category, lo, hi) {
			if opt.ExcludeBoundary && (b.Price <= lo+1e-9 || b.Price >= hi-1e-9) {
				continue
			}
			title := b.Title
			if opt.TruncateTitles && len([]rune(title)) > 25 {
				title = string([]rune(title)[:25]) + "..."
			}
			out = append(out, bookJSON{field: title, "price": b.Price, "category": b.Category})
		}
		if out == nil {
			out = []bookJSON{}
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if opt.WrapInObject {
			json.NewEncoder(w).Encode(map[string]any{"books": out})
			return
		}
		json.NewEncoder(w).Encode(out)
	})
	return mux
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
	json.NewEncoder(w).Encode(map[string]string{"message": msg})
}
