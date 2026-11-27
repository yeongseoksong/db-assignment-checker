package checks

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"math/rand"
	"os"
	"strings"
	"testing"

	"dbchecker/internal/dataset"
)

func titles(books []dataset.Book) []string {
	var out []string
	for _, b := range books {
		out = append(out, b.Title)
	}
	return out
}

func TestJudgeBooksInDB(t *testing.T) {
	ds := loadData(t)
	all := titles(ds.Books)

	full := []dbColumn{{Table: "book", Column: "title", Values: all}, {Table: "book", Column: "category", Values: ds.Categories()}}
	if res := judgeBooksInDB(full, ds.Books); !res.Passed || !strings.Contains(res.Detail, "book.title") {
		t.Errorf("전체 제목이 있으면 통과하고 컬럼을 적어야 합니다: %+v", res)
	}

	var truncated []string
	for _, title := range all {
		runes := []rune(title)
		if len(runes) > 25 {
			title = string(runes[:25]) + "..."
		}
		truncated = append(truncated, title)
	}
	if res := judgeBooksInDB([]dbColumn{{Table: "b", Column: "t", Values: truncated}}, ds.Books); !res.Passed {
		t.Errorf("잘린 제목도 통과해야 합니다: %+v", res)
	}

	cut := 940 // 94%
	if res := judgeBooksInDB([]dbColumn{{Table: "b", Column: "t", Values: all[:cut]}}, ds.Books); res.Passed {
		t.Errorf("94%% 는 실패해야 합니다: %+v", res)
	}
	if res := judgeBooksInDB([]dbColumn{{Table: "b", Column: "t", Values: all[:950]}}, ds.Books); !res.Passed {
		t.Errorf("95%% 는 통과해야 합니다: %+v", res)
	}
	if res := judgeBooksInDB(nil, ds.Books); res.Passed {
		t.Error("텍스트 컬럼이 없으면 실패여야 합니다")
	}
}

func TestJudgeCategoriesInDB(t *testing.T) {
	ds := loadData(t)
	cats := ds.Categories()
	if len(cats) != 50 {
		t.Fatalf("카테고리 50개 전제: %d", len(cats))
	}
	var messy []string
	for _, c := range cats {
		messy = append(messy, "  "+strings.ToUpper(c)+" ")
	}
	if res := judgeCategoriesInDB([]dbColumn{{Table: "c", Column: "name", Values: messy}}, cats); !res.Passed {
		t.Errorf("대소문자·공백 차이는 통과해야 합니다: %+v", res)
	}
	res := judgeCategoriesInDB([]dbColumn{{Table: "c", Column: "name", Values: cats[:49]}}, cats)
	if res.Passed || !strings.Contains(res.Detail, cats[49]) {
		t.Errorf("하나라도 없으면 실패하고 이름을 적어야 합니다: %+v", res)
	}
}

const testPassword = "S3cr3t-Pa55w0rd"

func TestPasswordNeverLogged(t *testing.T) {
	// 연결할 수 없는 주소를 쓰되 비밀번호가 들어 있는 연결 문자열로 D1 을 실행한다.
	connString := "postgresql://neonuser:" + testPassword + "@127.0.0.1:1/neondb?sslmode=disable"
	var logBuf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	env := &Env{Data: loadData(t), HTTP: NewHTTPClient(log), Log: log, Rand: rand.New(rand.NewSource(1)), DBURL: connString, BaseURL: "http://127.0.0.1:1"}
	outcomes := RunAll(context.Background(), env)
	var all strings.Builder
	all.WriteString(logBuf.String())
	for _, o := range outcomes {
		all.WriteString(o.Result.Detail)
	}
	if strings.Contains(all.String(), testPassword) {
		t.Fatalf("로그나 Detail 에 비밀번호가 남았습니다:\n%s", all.String())
	}
	if !strings.Contains(RedactURL(connString), "neonuser") || strings.Contains(RedactURL(connString), testPassword) {
		t.Errorf("RedactURL 결과가 이상합니다: %s", RedactURL(connString))
	}
	if d := scrubPassword("failed: "+connString, connString); strings.Contains(d, testPassword) {
		t.Errorf("scrubPassword 가 비밀번호를 남겼습니다: %s", d)
	}
}

// TestDBIntegration 은 TEST_PG_URL 환경변수가 있을 때만 실제 Postgres 로 D1~D3 를 검사한다.
// TestDBIntegration 은 실제 DB 로 D1~D3 를 돌린다. TEST_PG_URL 이 있을 때만 돈다.
// testdata/neon_seed.sql 을 넣은 DB 라면 TEST_PG_SCHEMA=checker_test 로 실행한다.
func TestDBIntegration(t *testing.T) {
	connString := os.Getenv("TEST_PG_URL")
	if connString == "" {
		t.Skip("TEST_PG_URL 이 없어 건너뜁니다")
	}
	schema := os.Getenv("TEST_PG_SCHEMA")
	if schema == "" {
		schema = "public"
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	env := &Env{Data: loadData(t), Log: log, DBURL: connString, DBSchema: schema}
	ctx := context.Background()
	for _, c := range dbChecks() {
		res := c.Run(ctx, env)
		if !res.Passed {
			t.Errorf("%s 실패: %s", c.ID, res.Detail)
		}
		t.Logf("%s: %s", c.ID, res.Detail)
	}
}

func dbChecks() []Check {
	var out []Check
	for _, c := range All {
		if c.Group == GroupDB {
			out = append(out, c)
		}
	}
	return out
}
