package checks

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"dbchecker/internal/dataset"
)

const (
	dbConnectTimeout = 15 * time.Second
	dbMaxRows        = 5000
	bookMatchRatio   = 0.95
)

// dbColumn 은 검사 대상 스키마의 텍스트 컬럼 하나와 읽어 온 값(중복 제거, 최대 5000개)이다.
type dbColumn struct {
	Schema, Table, Column string
	Values                []string
}

// name 은 결과에 보여 줄 컬럼 이름이다. public 스키마면 스키마를 생략한다.
func (c dbColumn) name() string {
	if c.Schema == "public" {
		return c.Table + "." + c.Column
	}
	return c.Schema + "." + c.Table + "." + c.Column
}

// dbSnapshot 은 D1 이 읽어 온 DB 내용이다. D2/D3 의 판정은 이것만 본다.
type dbSnapshot struct {
	Columns []dbColumn
}

// RedactURL 은 연결 문자열의 비밀번호를 가린 문자열을 돌려준다. 로그와 화면에는 이것만 쓴다.
func RedactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "(해석할 수 없는 연결 문자열)"
	}
	return u.Redacted()
}

// scrubPassword 는 텍스트에 섞인 비밀번호(원문과 URL 인코딩 형태)를 *** 로 바꾼다.
func scrubPassword(text, rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.User == nil {
		return text
	}
	pw, ok := u.User.Password()
	if !ok || pw == "" {
		return text
	}
	userinfo := (&url.URL{User: url.UserPassword("", pw)}).User.String() // ":" + 인코딩된 비밀번호
	for _, form := range []string{pw, url.QueryEscape(pw), url.PathEscape(pw), strings.TrimPrefix(userinfo, ":")} {
		text = strings.ReplaceAll(text, form, "***")
	}
	return text
}

// describeDBError 는 접속 실패 원인을 학생이 고칠 수 있는 말로 바꾼다. 비밀번호는 가린다.
func describeDBError(err error, rawURL string) string {
	var pgErr *pgconn.PgError
	var dnsErr *net.DNSError
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "연결 시간(15초)이 초과되었습니다. 인터넷 연결과 Neon 프로젝트가 켜져 있는지(일시 정지 상태가 아닌지) 확인하세요"
	case errors.As(err, &pgErr) && (pgErr.Code == "28P01" || pgErr.Code == "28000"):
		return "인증에 실패했습니다. DB 사용자명과 비밀번호를 Neon 대시보드에서 다시 복사하세요"
	case errors.As(err, &pgErr) && pgErr.Code == "3D000":
		return "데이터베이스 이름이 없습니다. Neon 주소 끝의 /<db이름> 을 확인하세요"
	case errors.As(err, &dnsErr):
		return "호스트를 찾을 수 없습니다. Neon 주소의 호스트(ep-xxx.neon.tech) 오타를 확인하세요"
	}
	msg := scrubPassword(err.Error(), rawURL)
	if strings.Contains(strings.ToLower(msg), "ssl") || strings.Contains(strings.ToLower(msg), "tls") {
		return "SSL 연결에 문제가 있습니다. Neon 주소 끝에 ?sslmode=require 가 있는지 확인하세요: " + msg
	}
	return "연결에 실패했습니다: " + msg
}

// loadSnapshot 은 읽기 전용 트랜잭션으로 연결해 schema 의 텍스트 컬럼 값을 읽는다.
func loadSnapshot(ctx context.Context, connString, schema string) (*dbSnapshot, error) {
	conn, err := pgx.Connect(ctx, connString)
	if err != nil {
		return nil, err
	}
	defer conn.Close(context.Background())
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.Background())

	const listColumns = `
		SELECT c.table_schema, c.table_name, c.column_name
		FROM information_schema.columns c
		JOIN information_schema.tables t ON t.table_schema = c.table_schema AND t.table_name = c.table_name
		WHERE c.table_schema = $1
		  AND t.table_type = 'BASE TABLE'
		  AND c.data_type IN ('text', 'character varying', 'character')
		ORDER BY c.table_schema, c.table_name, c.ordinal_position`
	rows, err := tx.Query(ctx, listColumns, schema)
	if err != nil {
		return nil, err
	}
	var snap dbSnapshot
	for rows.Next() {
		var col dbColumn
		if err := rows.Scan(&col.Schema, &col.Table, &col.Column); err != nil {
			rows.Close()
			return nil, err
		}
		snap.Columns = append(snap.Columns, col)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range snap.Columns {
		col := &snap.Columns[i]
		// 식별자는 파라미터로 넘길 수 없으므로 Sanitize 로 인용한다.
		table := pgx.Identifier{col.Schema, col.Table}.Sanitize()
		column := pgx.Identifier{col.Column}.Sanitize()
		query := fmt.Sprintf("SELECT DISTINCT %s FROM %s LIMIT %d", column, table, dbMaxRows)
		values, err := readStrings(ctx, tx, query)
		if err != nil {
			return nil, err
		}
		col.Values = values
	}
	return &snap, nil
}

func readStrings(ctx context.Context, tx pgx.Tx, query string) ([]string, error) {
	rows, err := tx.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v *string // NULL 허용
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		if v != nil {
			out = append(out, *v)
		}
	}
	return out, rows.Err()
}

// ---- 순수 판정 함수 (DB 없이 테스트한다) ----

// countTitleMatches 는 값 목록과 제목 매칭 규칙(정규화, 10자 이상 접두사)으로 일치하는 책 수를 센다.
func countTitleMatches(values []string, books []dataset.Book) int {
	exact := map[string]bool{}
	var prefixes []string
	for _, v := range values {
		n := normalizeTitle(v)
		exact[n] = true
		if len(n) >= minPrefixLen {
			prefixes = append(prefixes, n)
		}
	}
	matched := 0
	for _, b := range books {
		want := normalizeTitle(b.Title)
		if exact[want] {
			matched++
			continue
		}
		for _, p := range prefixes {
			if matchNormalized(p, want) {
				matched++
				break
			}
		}
	}
	return matched
}

// judgeBooksInDB 는 데이터셋 책의 95% 이상과 일치하는 텍스트 컬럼이 있으면 통과시킨다.
func judgeBooksInDB(columns []dbColumn, books []dataset.Book) Result {
	if len(columns) == 0 {
		return Result{Detail: "지정한 스키마에 text/varchar 컬럼이 있는 테이블이 없습니다. 스키마 이름과, 크롤링한 책을 테이블에 저장했는지 확인하세요"}
	}
	best, bestCount := "", -1
	for _, col := range columns {
		n := countTitleMatches(col.Values, books)
		if n > bestCount {
			best, bestCount = col.name(), n
		}
	}
	total := len(books)
	detail := fmt.Sprintf("%s 컬럼이 책 %d/%d권과 일치", best, bestCount, total)
	if float64(bestCount) >= bookMatchRatio*float64(total) {
		return Result{Passed: true, Detail: detail}
	}
	return Result{Detail: detail + " (95% 이상 필요). 전체 카테고리를 빠짐없이 크롤링해 저장했는지 확인하세요"}
}

func normalizeCategory(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// judgeCategoriesInDB 는 어떤 텍스트 컬럼의 값이 카테고리명을 모두 포함하면 통과시킨다.
func judgeCategoriesInDB(columns []dbColumn, categories []string) Result {
	best, bestMissing := "", categories
	for _, col := range columns {
		have := map[string]bool{}
		for _, v := range col.Values {
			have[normalizeCategory(v)] = true
		}
		var missing []string
		for _, c := range categories {
			if !have[normalizeCategory(c)] {
				missing = append(missing, c)
			}
		}
		if best == "" || len(missing) < len(bestMissing) {
			best, bestMissing = col.name(), missing
		}
	}
	if best == "" {
		return Result{Detail: "지정한 스키마에 text/varchar 컬럼이 있는 테이블이 없습니다"}
	}
	found := len(categories) - len(bestMissing)
	detail := fmt.Sprintf("%s 컬럼에 카테고리 %d/%d개", best, found, len(categories))
	if len(bestMissing) == 0 {
		return Result{Passed: true, Detail: detail}
	}
	return Result{Detail: fmt.Sprintf("%s. 없는 카테고리: %s", detail, strings.Join(limit(bestMissing), ", "))}
}

// ---- 검사 ----

func checkD1(ctx context.Context, env *Env) Result {
	ctx, cancel := context.WithTimeout(ctx, dbConnectTimeout)
	defer cancel()
	start := time.Now()
	snap, err := loadSnapshot(ctx, env.DBURL, env.DBSchema)
	if err != nil {
		friendly := describeDBError(err, env.DBURL)
		env.Log.Debug("DB 연결 실패", "db", RedactURL(env.DBURL), "err", scrubPassword(err.Error(), env.DBURL))
		return Result{Detail: friendly}
	}
	env.db = snap
	env.Log.Debug("DB 읽기 완료", "db", RedactURL(env.DBURL), "schema", env.DBSchema, "columns", len(snap.Columns), "elapsed", time.Since(start))
	if len(snap.Columns) == 0 {
		return Result{Passed: true, Detail: fmt.Sprintf("연결 성공. 그런데 스키마 %q 에 테이블이 없습니다. 입력 창의 스키마 칸을 확인하세요", env.DBSchema)}
	}
	return Result{Passed: true, Detail: fmt.Sprintf("연결 성공 (스키마 %s, 텍스트 컬럼 %d개 확인)", env.DBSchema, len(snap.Columns))}
}

func checkD2(_ context.Context, env *Env) Result {
	if env.db == nil {
		return Result{Detail: "D1(연결)이 실패해 확인하지 못했습니다"}
	}
	return judgeBooksInDB(env.db.Columns, env.Data.Books)
}

func checkD3(_ context.Context, env *Env) Result {
	if env.db == nil {
		return Result{Detail: "D1(연결)이 실패해 확인하지 못했습니다"}
	}
	return judgeCategoriesInDB(env.db.Columns, env.Data.Categories())
}
