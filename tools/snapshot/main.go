// snapshot 은 books.toscrape.com 전체를 긁어 internal/dataset/books.json 을 만든다.
// 사용: go run ./tools/snapshot
package main

import (
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const baseURL = "https://books.toscrape.com/"

type book struct {
	Title    string  `json:"title"`
	Price    float64 `json:"price"`
	Category string  `json:"category"`
}

var (
	// 사이드바: <a href="catalogue/category/books/travel_2/index.html"> Travel </a>
	reCategory = regexp.MustCompile(`(?s)<a href="(catalogue/category/books/[a-z0-9\-_]+/index\.html)">\s*(.*?)\s*</a>`)
	reTitle    = regexp.MustCompile(`<h3><a [^>]*title="([^"]*)"`)
	rePrice    = regexp.MustCompile(`<p class="price_color">[^0-9]*([0-9]+\.[0-9]+)</p>`)
	reNext     = regexp.MustCompile(`<li class="next">`)
)

var client = &http.Client{Timeout: 30 * time.Second}

func fetch(url string) (string, error) {
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		resp, err := client.Get(url)
		if err == nil {
			body, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()
			if readErr == nil && resp.StatusCode == 200 {
				return string(body), nil
			}
			if readErr != nil {
				err = readErr
			} else {
				err = fmt.Errorf("상태코드 %d", resp.StatusCode)
			}
		}
		lastErr = err
		time.Sleep(time.Duration(attempt) * time.Second)
	}
	return "", fmt.Errorf("%s 요청 실패: %w", url, lastErr)
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "오류:", err)
		os.Exit(1)
	}
}

func run() error {
	home, err := fetch(baseURL + "index.html")
	if err != nil {
		return err
	}
	var books []book
	categoryCount := 0
	for _, m := range reCategory.FindAllStringSubmatch(home, -1) {
		href, name := m[1], strings.TrimSpace(html.UnescapeString(m[2]))
		if name == "Books" { // 사이드바 최상위 항목(전체)은 제외
			continue
		}
		categoryCount++
		pageBase := baseURL + strings.TrimSuffix(href, "index.html")
		pageURL := pageBase + "index.html"
		for page := 2; ; page++ {
			body, err := fetch(pageURL)
			if err != nil {
				return err
			}
			titles := reTitle.FindAllStringSubmatch(body, -1)
			prices := rePrice.FindAllStringSubmatch(body, -1)
			if len(titles) != len(prices) {
				return fmt.Errorf("%s: 제목 %d개, 가격 %d개로 개수가 다릅니다", pageURL, len(titles), len(prices))
			}
			for i := range titles {
				price, _ := strconv.ParseFloat(prices[i][1], 64)
				books = append(books, book{Title: html.UnescapeString(titles[i][1]), Price: price, Category: name})
			}
			if !reNext.MatchString(body) {
				break
			}
			pageURL = fmt.Sprintf("%spage-%d.html", pageBase, page)
		}
		fmt.Printf("%-22s 누적 %d권\n", name, len(books))
	}
	fmt.Printf("카테고리 %d개, 책 %d권\n", categoryCount, len(books))
	if categoryCount != 50 || len(books) != 1000 {
		return fmt.Errorf("카테고리 50개/책 1000권이어야 하는데 %d개/%d권입니다. 사이트 구조가 바뀌었는지 확인하세요", categoryCount, len(books))
	}
	data, err := json.MarshalIndent(books, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile("internal/dataset/books.json", append(data, '\n'), 0o644)
}
