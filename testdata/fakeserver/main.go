// fakeserver 는 채점기를 시험하는 가짜 학생 서버다. -bug 로 일부러 틀린 버전을 고를 수 있고,
// 쉼표로 여러 버전을 주면 -port 부터 포트를 하나씩 올려 가며 버전마다 서버를 따로 띄운다.
//
//	go run ./testdata/fakeserver                              정답 서버 (:8080)
//	go run ./testdata/fakeserver -bug correct,boundary        :8080 정답, :8081 경계값 누락
//	go run ./testdata/fakeserver -bug all                     모든 버전을 :8080 부터
//	go run ./testdata/fakeserver -list                        고를 수 있는 버전 목록
package main

import (
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"

	"dbchecker/internal/dataset"
	"dbchecker/internal/fakestudent"
)

func main() {
	port := flag.Int("port", 8080, "첫 서버의 포트. 버전이 여러 개면 하나씩 올라간다")
	bug := flag.String("bug", "correct", "서버 버전. 쉼표로 여러 개, all 이면 전부 (-list 로 목록 확인)")
	list := flag.Bool("list", false, "고를 수 있는 서버 버전 목록")
	flag.Parse()
	if *list {
		printVariants()
		return
	}
	variants, err := parseVariants(*bug)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n\n", err)
		printVariants()
		os.Exit(2)
	}
	ds, err := dataset.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// 포트를 먼저 모두 연다. 하나라도 이미 쓰이고 있으면 아무것도 띄우지 않고 끝낸다.
	var listeners []net.Listener
	for i := range variants {
		addr := ":" + strconv.Itoa(*port+i)
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			for _, opened := range listeners {
				opened.Close()
			}
			fmt.Fprintf(os.Stderr, "포트 %s 를 열 수 없습니다 (다른 프로그램이 쓰는 중인지 확인하세요): %v\n", addr, err)
			os.Exit(1)
		}
		listeners = append(listeners, ln)
	}

	errs := make(chan error, len(variants))
	for i, v := range variants {
		fmt.Printf("  :%d  %-20s %s\n", *port+i, v.Name, v.Description)
		// 버전마다 자기 ServeMux(Handler)를 가진 서버를 따로 띄운다 (≈ 포트별 Spring 앱 하나씩)
		go func(ln net.Listener, handler http.Handler) {
			errs <- http.Serve(ln, handler)
		}(listeners[i], fakestudent.Handler(ds, v.Options))
	}
	fmt.Println("Ctrl+C 로 종료합니다.")
	fmt.Fprintln(os.Stderr, <-errs) // 서버 하나라도 멈추면 전체 종료
	os.Exit(1)
}

// parseVariants 는 "correct,boundary" 나 "all" 을 버전 목록으로 바꾼다.
func parseVariants(spec string) ([]fakestudent.Variant, error) {
	if strings.TrimSpace(spec) == "all" {
		return fakestudent.Variants, nil
	}
	var out []fakestudent.Variant
	for _, name := range strings.Split(spec, ",") {
		name = strings.TrimSpace(name)
		v, ok := fakestudent.FindVariant(name)
		if !ok {
			return nil, fmt.Errorf("알 수 없는 버전 %q 입니다.", name)
		}
		out = append(out, v)
	}
	if len(out) == 0 {
		return nil, errors.New("버전을 하나 이상 지정하세요")
	}
	return out, nil
}

func printVariants() {
	for _, v := range fakestudent.Variants {
		expect := "모두 통과"
		if len(v.ExpectFail) > 0 {
			expect = strings.Join(v.ExpectFail, ", ") + " 실패"
		}
		fmt.Printf("  %-20s %s  →  %s\n", v.Name, v.Description, expect)
	}
}
