package report

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

// FuzzOpenRejectsAnythingElse 는 교수자 도구가 학생이 낸 아무 파일을 열 때를 흉내 낸다.
// 패닉이 나면 안 되고, 정상 봉인 파일을 한 바이트라도 바꾼 것은 절대 열리면 안 된다.
func FuzzOpenRejectsAnythingElse(f *testing.F) {
	priv, err := GenerateKey()
	if err != nil {
		f.Fatal(err)
	}
	original, err := Seal(&priv.PublicKey, []byte("student_id,check_id\n20231234,TOTAL\n"))
	if err != nil {
		f.Fatal(err)
	}
	f.Add(original)
	f.Add([]byte{})
	f.Add([]byte("DBCK2"))
	f.Add([]byte("DBCK1" + string(make([]byte, 64))))
	f.Add(append([]byte("DBCK2"), make([]byte, 300)...))
	f.Fuzz(func(t *testing.T, data []byte) {
		plain, err := Open(priv, data)
		if bytes.Equal(data, original) {
			if err != nil {
				t.Fatalf("정상 파일이 열리지 않았습니다: %v", err)
			}
			return
		}
		if err == nil {
			t.Fatalf("변조된 파일이 열렸습니다 (평문 %q)", plain)
		}
	})
}

// FuzzCSVRoundTrip 은 어떤 문자열(쉼표, 따옴표, 줄바꿈, 가짜 "#hmac," 줄 포함)이 결과에 들어가도
// BuildCSV → ParseCSV 가 같은 값을 돌려주고 서명 검증을 통과하는지 본다.
// detail 에는 학생 서버가 보낸 문자열이 들어갈 수 있다.
func FuzzCSVRoundTrip(f *testing.F) {
	f.Add("20231234", "통과 8 / 전체 8", "host", true)
	f.Add("1", "쉼표, \"따옴표\"\n줄바꿈", "h", false)
	f.Add("1", "a\n#hmac,deadbeef\n", "h\r\n", true)
	f.Add("", "", "", false)
	secret := []byte("fuzz-secret")
	f.Fuzz(func(t *testing.T, id, detail, host string, passed bool) {
		rows := []Row{{StudentID: id, CheckID: "A2", Group: "API", Title: "t", Passed: passed, Detail: detail, Host: host}}
		data, err := BuildCSV(rows, secret)
		if err != nil {
			return // CSV 로 쓸 수 없는 값은 쓰기 단계에서 오류로 끝나면 된다
		}
		records, err := ParseCSV(data, secret)
		if err != nil {
			t.Fatalf("직접 만든 CSV 의 서명 검증이 실패했습니다: %v\n%q", err, data)
		}
		// encoding/csv 는 따옴표 안의 \r\n 을 \n 으로 읽는다 (문서화된 동작). 그 차이만 허용한다.
		want := rows[0].record()
		for i := range want {
			want[i] = strings.ReplaceAll(want[i], "\r\n", "\n")
		}
		if len(records) != 2 || !reflect.DeepEqual(records[1], want) {
			t.Fatalf("되읽은 값이 다릅니다\n쓴 값: %q\n읽은 값: %q", rows[0].record(), records)
		}
	})
}

// FuzzParseCSVAnyInput 은 복호화된 내용이 무엇이든 ParseCSV 가 패닉 없이 끝나는지 본다.
func FuzzParseCSVAnyInput(f *testing.F) {
	f.Add([]byte("a,b\n#hmac,00\n"))
	f.Add([]byte("#hmac,"))
	f.Add([]byte("\n#hmac,\n\n"))
	f.Add([]byte("\"unterminated\n#hmac,00"))
	f.Fuzz(func(t *testing.T, data []byte) {
		ParseCSV(data, []byte("k"))
	})
}
