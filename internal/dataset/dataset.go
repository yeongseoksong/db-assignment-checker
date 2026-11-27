// Package dataset 은 books.toscrape.com 스냅샷(books.json)을 내장하고 조회 함수를 제공한다.
package dataset

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

//go:embed books.json
var embedded []byte

// Book 은 사이트의 책 한 권이다.
type Book struct {
	Title    string  `json:"title"`
	Price    float64 `json:"price"`
	Category string  `json:"category"`
}

// Dataset 은 전체 책 목록과 카테고리 색인이다.
type Dataset struct {
	Books      []Book
	categories []string
}

// Load 는 내장된 books.json 을 읽는다.
func Load() (*Dataset, error) {
	return Parse(embedded)
}

// Parse 는 JSON 바이트에서 데이터셋을 만든다.
func Parse(data []byte) (*Dataset, error) {
	var books []Book
	if err := json.Unmarshal(data, &books); err != nil {
		return nil, fmt.Errorf("books.json 을 읽을 수 없습니다: %w", err)
	}
	if len(books) == 0 {
		return nil, errors.New("books.json 이 비어 있습니다. make snapshot 으로 다시 생성하세요")
	}
	seen := map[string]bool{}
	var cats []string
	for _, b := range books {
		if !seen[b.Category] {
			seen[b.Category] = true
			cats = append(cats, b.Category)
		}
	}
	sort.Strings(cats)
	return &Dataset{Books: books, categories: cats}, nil
}

// Categories 는 정렬된 카테고리 이름 목록이다.
func (d *Dataset) Categories() []string {
	return d.categories
}

// HasCategory 는 카테고리 존재 여부를 돌려준다.
func (d *Dataset) HasCategory(name string) bool {
	for _, c := range d.categories {
		if c == name {
			return true
		}
	}
	return false
}

// Query 는 카테고리 안에서 min 이상 max 이하 가격의 책을 돌려준다.
func (d *Dataset) Query(category string, min, max float64) []Book {
	var out []Book
	for _, b := range d.Books {
		if b.Category == category && b.Price >= min-1e-9 && b.Price <= max+1e-9 {
			out = append(out, b)
		}
	}
	return out
}
