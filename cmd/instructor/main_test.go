package main

import (
	"archive/zip"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"dbchecker/internal/report"
)

func recordWith(studentID, macs string) []string {
	rec := make([]string, len(report.Header))
	rec[0] = studentID
	rec[slices.Index(report.Header, "mac_addresses")] = macs
	return rec
}

func TestSharedDevices(t *testing.T) {
	sameMachine := "36:0c:4f:98:6b:40;52:ee:bb:79:88:cd;0a:00:27:00:00:00"
	records := [][]string{
		recordWith("A", sameMachine),
		recordWith("A", sameMachine), // 같은 학번의 여러 행은 한 번으로 센다
		recordWith("B", sameMachine),
		recordWith("C", "0a:00:27:00:00:00;7e:17:9d:fe:56:28"),                   // VirtualBox 고정 주소만 겹침 → 무시
		recordWith("D", "7e:17:9d:fe:56:28;ae:9d:2c:c6:55:fb;52:ee:bb:79:88:cd"), // A·B 와 1개, C 와 1개 겹침
	}
	got := sharedDevices(records)
	want := []devicePair{
		{A: "A", B: "B", Shared: 2, Fewer: 2},
		{A: "A", B: "D", Shared: 1, Fewer: 2},
		{A: "B", B: "D", Shared: 1, Fewer: 2},
		{A: "C", B: "D", Shared: 1, Fewer: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
}

func TestSharedDevicesNoOverlap(t *testing.T) {
	records := [][]string{
		recordWith("A", "a4:83:e7:00:00:01"),
		recordWith("B", "a4:83:e7:00:00:02"),
		recordWith("C", ""),
	}
	if got := sharedDevices(records); len(got) != 0 {
		t.Errorf("겹침이 없어야 합니다: %+v", got)
	}
}

// writeZip 은 이름→내용으로 제출 zip 을 만든다.
func writeZip(t *testing.T, entries map[string][]byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "submit_20231234_x.zip")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	zw := zip.NewWriter(file)
	for name, data := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadSubmission(t *testing.T) {
	priv, err := report.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	secret := []byte("test-secret")
	csvBytes, err := report.BuildCSV([]report.Row{{StudentID: "20231234", CheckID: "TOTAL"}}, secret)
	if err != nil {
		t.Fatal(err)
	}
	sealedResult, _ := report.Seal(&priv.PublicKey, csvBytes)
	sealedLog, _ := report.Seal(&priv.PublicKey, []byte("로그 내용"))

	t.Run("정상 제출", func(t *testing.T) {
		path := writeZip(t, map[string][]byte{"result_20231234_x.enc": sealedResult, "checker_20231234_x.log.enc": sealedLog})
		sub := readSubmission(path, priv, secret)
		if sub.Integrity != "OK" || len(sub.Records) != 1 || string(sub.Log) != "로그 내용" || sub.LogName != "checker_20231234_x.log" {
			t.Errorf("정상 제출을 읽지 못했습니다: %+v", sub)
		}
	})

	// 압축하면 작지만 풀면 큰 항목(압축 폭탄)은 통째로 읽지 않고 거부해야 한다.
	t.Run("너무 큰 항목 거부", func(t *testing.T) {
		huge := make([]byte, maxEntryBytes+1)
		path := writeZip(t, map[string][]byte{"result_20231234_x.enc": huge, "checker_20231234_x.log.enc": sealedLog})
		sub := readSubmission(path, priv, secret)
		if !strings.Contains(sub.Integrity, "너무 큽니다") {
			t.Errorf("큰 항목은 크기 초과로 거부해야 합니다: %s", sub.Integrity)
		}
	})

	t.Run(".enc 가 아닌 항목은 읽지 않음", func(t *testing.T) {
		huge := make([]byte, maxEntryBytes+1)
		path := writeZip(t, map[string][]byte{"result_20231234_x.enc": sealedResult, "checker_20231234_x.log.enc": sealedLog, "README.txt": huge})
		sub := readSubmission(path, priv, secret)
		if sub.Integrity != "OK" {
			t.Errorf("관계없는 항목은 무시해야 합니다: %s", sub.Integrity)
		}
	})
}
