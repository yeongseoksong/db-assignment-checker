// fakeserver 는 채점기를 시험하는 가짜 학생 서버다. 정답 버전과 일부러 틀린 버전을 모두
// -port 부터 포트를 하나씩 올려 가며 띄우고, 어느 포트가 어떤 버전인지 시작할 때 한 번 출력한다.
//
//	go run ./testdata/fakeserver              :8080 부터
//	go run ./testdata/fakeserver -port 9000   :9000 부터
package main

import (
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
	port := flag.Int("port", 8080, "첫 버전의 포트. 버전마다 하나씩 올라간다")
	flag.Parse()
	ds, err := dataset.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// 포트를 먼저 모두 연다. 하나라도 이미 쓰이고 있으면 아무것도 띄우지 않고 끝낸다.
	var listeners []net.Listener
	for i := range fakestudent.Variants {
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

	errs := make(chan error, len(fakestudent.Variants))
	fmt.Println("포트    버전                 실패 검사          설명") // 한글은 화면 폭이 두 배라 값 열은 ASCII 로만 채운다
	for i, v := range fakestudent.Variants {
		expect := "-"
		if len(v.ExpectFail) > 0 {
			expect = strings.Join(v.ExpectFail, ", ")
		}
		fmt.Printf(":%-6d %-20s %-18s %s\n", *port+i, v.Name, expect, v.Description)
		// 버전마다 자기 ServeMux(Handler)를 가진 서버를 따로 띄운다 (≈ 포트별 Spring 앱 하나씩)
		go func(ln net.Listener, handler http.Handler) {
			errs <- http.Serve(ln, handler)
		}(listeners[i], fakestudent.Handler(ds, v.Options))
	}
	fmt.Println("Ctrl+C 로 종료합니다.")
	fmt.Fprintln(os.Stderr, <-errs) // 서버 하나라도 멈추면 전체 종료
	os.Exit(1)
}
