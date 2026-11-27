package app

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"dbchecker/internal/dataset"
	"dbchecker/internal/fakestudent"
	"dbchecker/internal/report"
)

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		in      Input
		wantErr string
	}{
		{"정상", Input{StudentID: "20231234", DBAddress: "jdbc:postgresql://ep-x.neon.tech/db", DBUser: "u", DBPassword: "p"}, ""},
		{"DB 비움 허용", Input{StudentID: "a1"}, ""},
		{"학번 한글", Input{StudentID: "홍길동"}, "학번"},
		{"학번 특수문자", Input{StudentID: "2023-12"}, "학번"},
		{"학번 비어 있음", Input{}, "학번"},
		{"DB 스킴 오류", Input{StudentID: "1", DBAddress: "mysql://h/db", DBUser: "u", DBPassword: "p"}, "postgresql://"},
		{"DB 자격증명 없음", Input{StudentID: "1", DBAddress: "ep-x.neon.tech/db"}, "사용자명/비밀번호"},
		{"DB 사용자만", Input{StudentID: "1", DBAddress: "ep-x.neon.tech/db", DBUser: "u"}, "사용자명/비밀번호"},
		{"포트 문자", Input{StudentID: "1", Port: "http"}, "포트"},
		{"포트 0", Input{StudentID: "1", Port: "0"}, "포트"},
		{"포트 범위 초과", Input{StudentID: "1", Port: "65536"}, "포트"},
		{"포트 경계", Input{StudentID: "1", Port: "65535"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Validate(c.in)
			if c.wantErr == "" && err != nil {
				t.Fatalf("에러가 없어야 합니다: %v", err)
			}
			if c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)) {
				t.Fatalf("에러에 %q 가 있어야 합니다: %v", c.wantErr, err)
			}
		})
	}
	got, _ := Validate(Input{StudentID: " 1 ", Port: " 9090 "})
	if got.BaseURL() != "http://localhost:9090" || got.StudentID != "1" {
		t.Errorf("기본값과 정리가 틀렸습니다: %+v", got)
	}
}

func TestRunWritesEncryptedResultWithoutLeakingPassword(t *testing.T) {
	const password = "Sup3rSecretPw"
	priv, _ := report.GenerateKey()
	secret := []byte("hmac-secret")
	cfg := Config{PublicKey: &priv.PublicKey, HMACSecret: secret, Version: "test", OutDir: t.TempDir()}
	ds, _ := dataset.Load()
	srv := httptest.NewServer(fakestudent.Handler(ds, fakestudent.Options{}))
	defer srv.Close()
	srvPort := srv.URL[strings.LastIndex(srv.URL, ":")+1:]

	in, err := Validate(Input{
		StudentID: "20231234", Port: srvPort,
		DBAddress: "jdbc:postgresql://127.0.0.1:1/db?sslmode=disable", DBUser: "neonuser", DBPassword: password,
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := Run(context.Background(), cfg, in)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.OpenReader(res.SubmitPath)
	if err != nil {
		t.Fatalf("제출 zip 을 열 수 없습니다: %v", err)
	}
	defer zr.Close()
	entries := map[string][]byte{} // 확장자 -> 내용
	for _, f := range zr.File {
		rc, _ := f.Open()
		data, _ := io.ReadAll(rc)
		rc.Close()
		if strings.HasSuffix(f.Name, ".log.enc") {
			entries[".log.enc"] = data
		} else if strings.HasSuffix(f.Name, ".enc") {
			entries[".enc"] = data
		}
	}
	if len(zr.File) != 2 || entries[".enc"] == nil || entries[".log.enc"] == nil {
		t.Fatalf("zip 에는 결과(.enc)와 로그(.log.enc) 두 파일이 있어야 합니다: %d개", len(zr.File))
	}
	sealedLog := entries[".log.enc"]
	if bytes.Contains(sealedLog, []byte("채점 시작")) {
		t.Fatal("로그 파일이 평문으로 저장되었습니다")
	}
	logData, err := report.Open(priv, sealedLog)
	if err != nil {
		t.Fatalf("로그를 개인키로 복호화할 수 없습니다: %v", err)
	}
	sealed := entries[".enc"]
	plain, err := report.Open(priv, sealed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(logData, []byte("neonuser")) {
		t.Error("사용자명은 로그에 남아도 됩니다(확인용)")
	}
	if bytes.Contains(logData, []byte(password)) || bytes.Contains(plain, []byte(password)) || bytes.Contains(sealed, []byte(password)) {
		t.Fatal("비밀번호가 로그 또는 결과 파일에 남았습니다")
	}
	records, err := report.ParseCSV(plain, secret)
	if err != nil {
		t.Fatal(err)
	}
	last := records[len(records)-1]
	if last[1] != "TOTAL" || last[5] != "통과 5 / 전체 8" {
		t.Errorf("TOTAL 행이 이상합니다: %v", last)
	}
	if len(records[0]) != 10 || records[0][4] != "passed" || records[0][9] != "mac_addresses" {
		t.Errorf("헤더가 이상합니다: %v", records[0])
	}
	if !strings.Contains(string(logData), "seed=") || !strings.Contains(string(logData), "HTTP 응답") {
		t.Error("로그에 시드와 HTTP 응답이 있어야 합니다")
	}
}

func TestResolveDBSchema(t *testing.T) {
	cases := []struct{ name, field, address, want string }{
		{"입력칸 우선", " bookstore ", "jdbc:postgresql://h/db?currentSchema=other", "bookstore"},
		{"주소의 currentSchema", "", "jdbc:postgresql://h/db?currentSchema=bookstore&sslmode=require", "bookstore"},
		{"currentSchema 목록이면 첫 번째", "", "jdbc:postgresql://h/db?currentSchema=bookstore,public", "bookstore"},
		{"둘 다 없으면 public", "", "jdbc:postgresql://h/db?sslmode=require", "public"},
		{"쿼리 없음", "", "h/db", "public"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ResolveDBSchema(c.field, c.address)
			if got != c.want {
				t.Errorf("got %q want %q", got, c.want)
			}
		})
	}
}

func TestNormalizeDBAddress(t *testing.T) {
	cases := []struct {
		name, address, user, password string
		want                          string // 비어 있으면 에러 기대
	}{
		{"jdbc 접두사 제거", "jdbc:postgresql://ep-x.neon.tech/neondb?sslmode=require", "u", "p", "postgresql://u:p@ep-x.neon.tech/neondb?sslmode=require"},
		{"postgresql 스킴", "postgresql://ep-x.neon.tech/neondb?sslmode=disable", "u", "p", "postgresql://u:p@ep-x.neon.tech/neondb?sslmode=disable"},
		{"postgres 스킴", "postgres://ep-x.neon.tech/neondb?sslmode=require", "u", "p", "postgres://u:p@ep-x.neon.tech/neondb?sslmode=require"},
		{"스킴 없음", "ep-x.neon.tech/neondb", "u", "p", "postgresql://u:p@ep-x.neon.tech/neondb?sslmode=require"},
		{"sslmode 자동 추가", "jdbc:postgresql://h/db", "u", "p", "postgresql://u:p@h/db?sslmode=require"},
		{"입력칸이 주소 안 자격증명보다 우선", "postgresql://old:oldpw@h/db?sslmode=require", "new", "newpw", "postgresql://new:newpw@h/db?sslmode=require"},
		{"입력칸이 비면 주소 안 값 사용", "postgresql://old:oldpw@h/db?sslmode=require", "", "", "postgresql://old:oldpw@h/db?sslmode=require"},
		{"일부만 입력칸 우선", "postgresql://old:oldpw@h/db?sslmode=require", "new", "", "postgresql://new:oldpw@h/db?sslmode=require"},
		{"쿼리의 user/password 사용", "jdbc:postgresql://h/db?user=qu&password=qp&sslmode=require", "", "", "postgresql://qu:qp@h/db?sslmode=require"},
		{"비밀번호 특수문자 인코딩", "h/db", "u", "p@ss:w/rd#1", "postgresql://u:p%40ss%3Aw%2Frd%231@h/db?sslmode=require"},
		{"Neon JDBC channelBinding 변환", "jdbc:postgresql://ep-x.neon.tech/neondb?sslmode=require&channelBinding=require", "u", "p", "postgresql://u:p@ep-x.neon.tech/neondb?channel_binding=require&sslmode=require"},
		{"currentSchema 등 JDBC 전용 파라미터 제거", "jdbc:postgresql://h/db?currentSchema=bookstore&loggerLevel=OFF&sslmode=require", "u", "p", "postgresql://u:p@h/db?sslmode=require"},
		{"자격증명 없음", "h/db", "", "", ""},
		{"다른 스킴", "jdbc:mysql://h/db", "u", "p", ""},
		{"호스트 없음", "postgresql:///db", "u", "p", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := NormalizeDBAddress(c.address, c.user, c.password)
			if c.want == "" {
				if err == nil {
					t.Fatalf("에러여야 합니다: %s", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Errorf("got %s\nwant %s", got, c.want)
			}
		})
	}
}
