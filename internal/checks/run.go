package checks

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
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

// RunAll 은 표의 검사를 순서대로 실행한다. 각 검사가 끝날 때마다 onDone 을 부른다(nil 가능).
// 연결 문자열이 없으면 DB 검사, 서버에 연결할 수 없으면 동적 검사는 전부 실패다.
func RunAll(ctx context.Context, env *Env, onDone func(Outcome)) []Outcome {
	serverErr := probeServer(ctx, env)
	if serverErr != nil {
		env.Log.Warn("서버 연결 실패", "url", env.BaseURL, "err", serverErr)
	}
	var outcomes []Outcome
	for _, c := range All {
		var res Result
		switch {
		case c.Group == GroupDB && env.DBURL == "":
			res = Result{Detail: "Neon 연결 문자열을 지정하세요 (-db 또는 입력 창의 연결 문자열 칸)"}
		case (c.Group == GroupAPI || c.Group == GroupErrors) && serverErr != nil:
			res = Result{Detail: fmt.Sprintf("서버(%s)에 연결할 수 없습니다. Spring Boot 앱을 먼저 실행하세요", env.BaseURL)}
		default:
			res = c.Run(ctx, env)
		}
		env.Log.Info("검사 결과", "id", c.ID, "passed", res.Passed, "detail", res.Detail)
		o := Outcome{Check: c, Result: res}
		outcomes = append(outcomes, o)
		if onDone != nil {
			onDone(o)
		}
	}
	return outcomes
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
