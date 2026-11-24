// fakeserver 는 채점기를 시험하는 가짜 학생 서버다. -bug 로 일부러 틀린 버전을 고를 수 있다.
//
//	go run ./testdata/fakeserver                       정답 서버 (:8080)
//	go run ./testdata/fakeserver -bug boundary         경계값을 빠뜨리는 서버
//	go run ./testdata/fakeserver -list                 고를 수 있는 버전 목록
package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"

	"dbchecker/internal/dataset"
	"dbchecker/internal/fakestudent"
)

func main() {
	addr := flag.String("addr", ":8080", "수신 주소")
	bug := flag.String("bug", "correct", "서버 버전 (-list 로 목록 확인)")
	list := flag.Bool("list", false, "고를 수 있는 서버 버전 목록")
	flag.Parse()
	if *list {
		printVariants()
		return
	}
	variant, ok := fakestudent.FindVariant(*bug)
	if !ok {
		fmt.Fprintf(os.Stderr, "알 수 없는 버전 %q 입니다.\n\n", *bug)
		printVariants()
		os.Exit(2)
	}
	ds, err := dataset.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("가짜 학생 서버 시작: %s (버전 %s: %s)\n", *addr, variant.Name, variant.Description)
	if err := http.ListenAndServe(*addr, fakestudent.Handler(ds, variant.Options)); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
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
