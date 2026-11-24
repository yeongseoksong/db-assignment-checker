// fakeserver 는 명세대로 응답하는 가짜 학생 서버다. 엔드투엔드 검증용.
// 사용: go run ./testdata/fakeserver -addr :8080
package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"

	"dbchecker/internal/dataset"
	"dbchecker/internal/fakestudent"
)

func main() {
	addr := flag.String("addr", ":8080", "수신 주소")
	flag.Parse()
	ds, err := dataset.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("가짜 학생 서버 시작:", *addr)
	if err := http.ListenAndServe(*addr, fakestudent.Handler(ds, fakestudent.Options{})); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
