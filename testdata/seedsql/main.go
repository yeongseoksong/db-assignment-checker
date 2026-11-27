// seedsql 은 채점기 DB 검사(D1~D3)를 시험할 수집 데이터 SQL 을 만든다.
// 정답 데이터(books.json) 1000권과 카테고리 50개를 checker_test 스키마에 넣는 SQL 을 표준 출력으로 쓴다.
//
//	go run ./testdata/seedsql > testdata/neon_seed.sql   (make seed-sql)
//
// 만든 SQL 은 Neon 콘솔의 SQL Editor 나 psql 에서 실행한다.
package main

import (
	"fmt"
	"os"
	"strings"

	"dbchecker/internal/dataset"
)

const schema = "checker_test"

func main() {
	ds, err := dataset.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "오류:", err)
		os.Exit(1)
	}
	fmt.Print(buildSQL(ds))
}

// quote 는 SQL 문자열 리터럴로 감싼다. 작은따옴표는 두 번 써서 이스케이프한다.
func quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func buildSQL(ds *dataset.Dataset) string {
	var b strings.Builder
	fmt.Fprintf(&b, `-- 채점기 DB 검사 시험용 수집 데이터 (자동 생성: make seed-sql). 손으로 고치지 마세요.
-- books.toscrape.com 의 책 %d권, 카테고리 %d개를 %s 스키마에 넣습니다.
-- %s 스키마만 지우고 다시 만들므로 다른 테이블은 건드리지 않고, 몇 번을 실행해도 결과가 같습니다.
-- 채점기 입력 창의 스키마 칸에 %s 를 넣으세요.

BEGIN;
DROP SCHEMA IF EXISTS %s CASCADE;
CREATE SCHEMA %s;

CREATE TABLE %s.category (
    id   int  PRIMARY KEY,
    name text NOT NULL UNIQUE
);

CREATE TABLE %s.book (
    id          serial        PRIMARY KEY,
    title       text          NOT NULL,
    price       numeric(6, 2) NOT NULL,
    category_id int           NOT NULL REFERENCES %s.category (id)
);

`, len(ds.Books), len(ds.Categories()), schema, schema, schema, schema, schema, schema, schema, schema)

	categoryID := map[string]int{}
	b.WriteString("INSERT INTO " + schema + ".category (id, name) VALUES\n")
	for i, name := range ds.Categories() {
		categoryID[name] = i + 1
		sep := ","
		if i == len(ds.Categories())-1 {
			sep = ";"
		}
		fmt.Fprintf(&b, "    (%d, %s)%s\n", i+1, quote(name), sep)
	}

	b.WriteString("\nINSERT INTO " + schema + ".book (title, price, category_id) VALUES\n")
	for i, book := range ds.Books {
		sep := ","
		if i == len(ds.Books)-1 {
			sep = ";"
		}
		fmt.Fprintf(&b, "    (%s, %.2f, %d)%s\n", quote(book.Title), book.Price, categoryID[book.Category], sep)
	}
	b.WriteString("\nCOMMIT;\n")
	return b.String()
}
