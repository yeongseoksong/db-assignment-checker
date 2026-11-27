package checks

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"runtime/debug"
)

// Outcome 은 검사와 그 결과의 쌍이다.
type Outcome struct {
	Check  Check
	Result Result
}

// Passed 는 검사를 통과했을 때 true 다.
func (o Outcome) Passed() bool {
	return o.Result.Passed
}

// RunAll 은 표의 검사를 순서대로 실행한다.
// 연결 문자열이 없으면 DB 검사, 서버에 연결할 수 없으면 동적 검사는 전부 실패다.
func RunAll(ctx context.Context, env *Env) []Outcome {
	serverErr := probeServer(ctx, env)
	if serverErr != nil {
		env.Log.Warn("서버 연결 실패", "url", env.BaseURL, "err", serverErr)
	}
	return runChecks(ctx, env, serverErr)
}

func runChecks(ctx context.Context, env *Env, serverErr error) []Outcome {
	var outcomes []Outcome
	for _, c := range All {
		var res Result
		switch {
		case c.Group == GroupDB && env.DBURL == "":
			res = Result{Detail: "Neon 주소를 입력하세요 (입력 창의 Neon 주소 칸 또는 -db)"}
		case (c.Group == GroupAPI || c.Group == GroupErrors) && serverErr != nil:
			res = Result{Detail: fmt.Sprintf("서버(%s)에 연결할 수 없습니다. Spring Boot 앱을 먼저 실행하세요", env.BaseURL)}
		default:
			res = runOne(ctx, env, c)
		}
		env.Log.Info("검사 결과", "id", c.ID, "passed", res.Passed, "detail", res.Detail)
		outcomes = append(outcomes, Outcome{Check: c, Result: res})
	}
	return outcomes
}

// runOne 은 검사 하나를 실행한다. 학생 서버의 예상 못 한 응답으로 판정 코드가 패닉 나도
// 채점기 전체가 죽지 않고(그러면 제출 파일도 안 생긴다) 그 검사만 실패로 기록한다.
// (≈ Java 에서 검사마다 try { ... } catch (RuntimeException e) 로 감싸는 것)
func runOne(ctx context.Context, env *Env, c Check) (res Result) {
	defer func() {
		r := recover()
		if r != nil {
			env.Log.Error("검사 중 패닉", "id", c.ID, "panic", r, "stack", string(debug.Stack()))
			res = Result{Detail: fmt.Sprintf("채점기 내부 오류로 이 검사를 끝내지 못했습니다 (%v). 결과 zip 을 그대로 제출하고 교수님께 알려 주세요", r)}
		}
	}()
	return c.Run(ctx, env)
}

// probeServer 는 BaseURL 로 한 번 요청해 연결 가능 여부만 본다. 어떤 HTTP 응답이든 연결된 것으로 본다.
func probeServer(ctx context.Context, env *Env) error {
	if _, err := fetch(ctx, env, "/", nil); err != nil && isConnectError(err) {
		return err
	}
	return nil
}

func isConnectError(err error) bool {
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}
	var urlErr *url.Error
	return errors.As(err, &urlErr) && !urlErr.Timeout()
}
