package report

import (
	"bytes"
	"crypto/rsa"
	"strings"
	"testing"
)

func newKeys(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	priv, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	return priv
}

func TestSealOpenRoundTrip(t *testing.T) {
	priv := newKeys(t)
	plain := []byte("student_id,check_id\n20231234,S1\n한글 데이터")
	sealed, err := Seal(&priv.PublicKey, plain)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("20231234")) {
		t.Fatal("암호문에 평문이 그대로 들어 있습니다")
	}
	got, err := Open(priv, sealed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("왕복 결과가 다릅니다: %q", got)
	}
}

func TestOpenFailsOnTamperingAnywhere(t *testing.T) {
	priv := newKeys(t)
	sealed, _ := Seal(&priv.PublicKey, []byte("결과"))
	for i := range sealed {
		tampered := append([]byte{}, sealed...)
		tampered[i] ^= 0x01
		if _, err := Open(priv, tampered); err == nil {
			t.Fatalf("%d번째 바이트 변조가 탐지되지 않았습니다", i)
		}
	}
}

func TestOpenFailsWithWrongKeyOrTruncation(t *testing.T) {
	priv := newKeys(t)
	sealed, _ := Seal(&priv.PublicKey, []byte("결과"))
	if _, err := Open(newKeys(t), sealed); err == nil {
		t.Fatal("다른 개인키로 열렸습니다")
	}
	if _, err := Open(priv, sealed[:10]); err == nil {
		t.Fatal("잘린 파일이 열렸습니다")
	}
}

func TestCSVHMAC(t *testing.T) {
	secret := []byte("secret")
	rows := []Row{
		{StudentID: "1", CheckID: "S1", Group: "구조", Title: "t", Passed: true, Detail: "쉼표, 따옴표\" 줄\n바꿈"},
		{StudentID: "1", CheckID: "TOTAL", Group: "합계", Passed: true},
	}
	data, err := BuildCSV(rows, secret)
	if err != nil {
		t.Fatal(err)
	}
	records, err := ParseCSV(data, secret)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 3 || records[1][5] != rows[0].Detail {
		t.Fatalf("레코드가 다릅니다: %v", records)
	}
	tampered := strings.Replace(string(data), "true", "fals", 1)
	records, err = ParseCSV([]byte(tampered), secret)
	if err == nil || records == nil {
		t.Fatal("결과 변조는 레코드와 함께 HMAC 오류를 돌려줘야 합니다")
	}
	if _, err := ParseCSV(data, []byte("other")); err == nil {
		t.Fatal("다른 비밀키로 검증이 통과했습니다")
	}
}
