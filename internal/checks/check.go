// Package checks 는 채점 항목(DB·API·오류처리)과 그 레지스트리를 정의한다.
package checks

import (
	"context"
	"log/slog"
	"math/rand"
	"net/http"

	"dbchecker/internal/dataset"
)

// 검사 그룹 이름 (CSV 와 콘솔에 그대로 나온다).
const (
	GroupAPI    = "API"
	GroupErrors = "오류처리"
	GroupDB     = "DB"
)

// Env 는 검사가 실행되는 환경이다.
type Env struct {
	BaseURL string
	Data    *dataset.Dataset
	HTTP    *http.Client
	Log     *slog.Logger
	Rand    *rand.Rand
	Seed    int64 // Rand 의 시드. A2/A3 detail 에 남겨 재현할 수 있게 한다.
	// DBURL 은 Neon 연결 문자열이다. 비밀번호가 들어 있으므로 로그·CSV·화면에 그대로 쓰지 않는다.
	DBURL string
	// DBSchema 는 D 검사가 테이블을 찾을 스키마다.
	DBSchema string

	db *dbSnapshot // D1 이 채우고 D2/D3 가 쓴다.
}

// Result 는 검사 결과다.
type Result struct {
	Passed bool
	Detail string
	Rounds *Rounds // 무작위 반복 검사(A2, A3)만 채운다. 나머지 검사는 nil
}

// Rounds 는 무작위 반복 검사에서 맞은 건수다. 교수자가 부분 점수를 줄 때 쓴다.
type Rounds struct {
	Passed, Total int
}

// Check 는 채점 항목 하나다.
type Check struct {
	ID, Group, Title string
	Points           float64 // 배점. 무작위 반복 검사(A2, A3)는 맞은 건수 비율만큼 받는다
	Run              func(ctx context.Context, env *Env) Result
}

// All 은 채점 항목 표다. 검사를 추가하려면 한 줄을 더한다.
// 배점(Points)은 교수자 도구(decrypt)가 점수를 계산할 때만 쓴다. 학생 화면에는 나오지 않는다.
// 배점을 바꾸면 decrypt 를 다시 돌리기만 하면 된다 (채점기를 다시 배포할 필요 없음).
var All = []Check{
	{"D1", GroupDB, "Neon DB 연결", 12.5, checkD1},
	{"D2", GroupDB, "DB 에 책 데이터 존재", 12.5, checkD2},
	{"D3", GroupDB, "DB 에 카테고리 50개 존재", 12.5, checkD3},
	{"A1", GroupAPI, "고정 케이스 Travel 10~30", 12.5, checkA1},
	{"A2", GroupAPI, "무작위 카테고리와 가격 범위 (10건)", 12.5, checkA2},
	{"A3", GroupAPI, "가격 경계값 포함, 이상/이하 (10건)", 12.5, checkA3},
	{"E1", GroupErrors, "존재하지 않는 카테고리 메시지", 12.5, checkE1},
	{"E2", GroupErrors, "minPrice > maxPrice 메시지", 12.5, checkE2},
}
