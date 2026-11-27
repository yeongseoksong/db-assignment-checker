package main

import (
	"archive/zip"
	"bytes"
	"fmt"
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
func writeZip(t testing.TB, entries map[string][]byte) string {
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

// v1.0.0 결과(rounds 열 없음)도 새 열 개수에 맞춰 빈 칸으로 채운다.
func TestPadRecordsForOldResults(t *testing.T) {
	old := make([]string, 10)
	old[0] = "20231234"
	got := padRecords([][]string{old})
	if len(got[0]) != len(report.Header) || got[0][0] != "20231234" || got[0][len(report.Header)-1] != "" {
		t.Fatalf("옛 결과를 새 열에 맞추지 못했습니다: %q", got[0])
	}
}

func TestFillRoundsFromLog(t *testing.T) {
	row := func(checkID, passed string) []string {
		rec := make([]string, len(report.Header))
		rec[slices.Index(report.Header, "check_id")] = checkID
		rec[slices.Index(report.Header, "passed")] = passed
		return rec
	}
	logLines := func(checkID string, passedRounds int) string {
		var b strings.Builder
		for i := 1; i <= 10; i++ {
			fmt.Fprintf(&b, "time=x level=INFO msg=\"무작위 검사\" id=%s round=%d passed=%v detail=\"...\"\n", checkID, i, i <= passedRounds)
		}
		return b.String()
	}
	col := slices.Index(report.Header, "rounds_passed")

	records := [][]string{row("A1", "true"), row("A2", "false"), row("A3", "true")}
	warnings := fillRoundsFromLog(records, []byte(logLines("A2", 7)+logLines("A3", 10)))
	if len(warnings) != 0 || records[1][col] != "7" || records[1][col+1] != "10" || records[2][col] != "10" || records[0][col] != "" {
		t.Fatalf("로그에서 건수를 채우지 못했습니다: %q / 경고 %v", records, warnings)
	}

	// 결과는 실패인데 로그는 10/10 → 믿지 않고 비워 둔다
	tampered := [][]string{row("A2", "false")}
	warnings = fillRoundsFromLog(tampered, []byte(logLines("A2", 10)))
	if len(warnings) != 1 || tampered[0][col] != "" {
		t.Fatalf("결과와 맞지 않는 로그는 무시하고 경고해야 합니다: %q / 경고 %v", tampered, warnings)
	}

	// 이미 건수가 있는 새 결과는 로그로 덮어쓰지 않는다
	fresh := [][]string{row("A2", "false")}
	fresh[0][col], fresh[0][col+1] = "3", "10"
	fillRoundsFromLog(fresh, []byte(logLines("A2", 9)))
	if fresh[0][col] != "3" {
		t.Fatalf("새 결과의 건수를 덮어썼습니다: %q", fresh[0])
	}
}

func TestScoreRecords(t *testing.T) {
	row := func(checkID, passed, roundsPassed, roundsTotal string) []string {
		rec := make([]string, len(report.Header))
		rec[slices.Index(report.Header, "check_id")] = checkID
		rec[slices.Index(report.Header, "passed")] = passed
		rec[slices.Index(report.Header, "rounds_passed")] = roundsPassed
		rec[slices.Index(report.Header, "rounds_total")] = roundsTotal
		return rec
	}
	records := [][]string{
		row("D1", "false", "", ""),
		row("A1", "true", "", ""),
		row("A2", "false", "7", "10"), // 부분 점수
		row("A3", "true", "10", "10"),
		row("E1", "false", "", ""),
		row("S9", "true", "", ""), // 검사 표에 없는 ID
		row("TOTAL", "false", "", ""),
	}
	got := scoreRecords(records)
	want := [][2]string{{"0", "12.5"}, {"12.5", "12.5"}, {"8.75", "12.5"}, {"12.5", "12.5"}, {"0", "12.5"}, {"", ""}, {"33.75", "100"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("점수가 다릅니다\n받음 %v\n기대 %v", got, want)
	}
}

func TestKeepLatestPerStudent(t *testing.T) {
	sub := func(base, studentID, checkedAt string) entry {
		rec := make([]string, len(report.Header))
		rec[slices.Index(report.Header, "student_id")] = studentID
		rec[slices.Index(report.Header, "checked_at")] = checkedAt
		return entry{base: base, records: [][]string{rec}}
	}
	all := []entry{
		sub("a_new.zip", "20231234", "2026-12-04T23:00:00+09:00"),
		sub("a_old.zip", "20231234", "2026-12-01T10:00:00+09:00"),
		sub("b.zip", "20235678", "2026-12-02T10:00:00+09:00"),
		sub("submit_20231234_20261204-235000.zip", "20231234", ""), // 읽지 못한 파일: 파일 이름 시각이 가장 최근
		sub("unknown.zip", "", ""),                                 // 학번을 모르는 파일은 남긴다
	}
	kept, dropped := keepLatestPerStudent(all)
	var keptNames []string
	for _, e := range kept {
		keptNames = append(keptNames, e.base)
	}
	want := []string{"b.zip", "submit_20231234_20261204-235000.zip", "unknown.zip"}
	if !reflect.DeepEqual(keptNames, want) || len(dropped) != 2 {
		t.Fatalf("남은 제출 %v (기대 %v), 제외 %+v", keptNames, want, dropped)
	}
	if dropped[0].keptBy != "submit_20231234_20261204-235000.zip" {
		t.Errorf("무엇이 덮어썼는지 알려 줘야 합니다: %+v", dropped[0])
	}
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

	t.Run("결과 파일이 두 개면 거부", func(t *testing.T) {
		path := writeZip(t, map[string][]byte{"result_a.enc": sealedResult, "result_b.enc": sealedResult, "checker_20231234_x.log.enc": sealedLog})
		sub := readSubmission(path, priv, secret)
		if !strings.Contains(sub.Integrity, "두 개 이상") {
			t.Errorf("같은 종류 항목이 여러 개면 거부해야 합니다: %s", sub.Integrity)
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

// FuzzReadSubmission 은 학생이 낸 아무 파일을 제출 zip 으로 읽을 때를 흉내 낸다.
// 패닉이 나면 안 되고, 정상 제출을 한 바이트라도 바꾼 것은 OK 로 판정되면 안 된다.
func FuzzReadSubmission(f *testing.F) {
	priv, err := report.GenerateKey()
	if err != nil {
		f.Fatal(err)
	}
	secret := []byte("test-secret")
	csvBytes, _ := report.BuildCSV([]report.Row{{StudentID: "20231234", CheckID: "TOTAL"}}, secret)
	sealedResult, _ := report.Seal(&priv.PublicKey, csvBytes)
	sealedLog, _ := report.Seal(&priv.PublicKey, []byte("로그"))
	seedPath := writeZip(f, map[string][]byte{"result_20231234_x.enc": sealedResult, "checker_20231234_x.log.enc": sealedLog})
	original, err := os.ReadFile(seedPath)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(original)
	f.Add([]byte("PK\x03\x04"))
	f.Add([]byte{})
	dir := f.TempDir()
	f.Fuzz(func(t *testing.T, data []byte) {
		path := filepath.Join(dir, "submit_fuzz.zip")
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		sub := readSubmission(path, priv, secret)
		if bytes.Equal(data, original) {
			if sub.Integrity != "OK" {
				t.Fatalf("정상 제출이 OK 가 아닙니다: %s", sub.Integrity)
			}
			return
		}
		if sub.Integrity == "OK" && !sameContent(t, data, original) {
			t.Fatal("내용이 바뀐 제출이 OK 로 판정됐습니다")
		}
	})
}

// sameContent 는 두 zip 의 항목 이름과 내용이 같은지 본다.
// zip 은 주석·타임스탬프 같은 겉모양만 달라도 바이트가 달라지므로, 그런 변화는 OK 여도 된다.
func sameContent(t *testing.T, a, b []byte) bool {
	t.Helper()
	read := func(data []byte) map[string]string {
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil
		}
		out := map[string]string{}
		for _, f := range zr.File {
			if !strings.HasSuffix(f.Name, ".enc") {
				continue
			}
			content, err := readZipEntry(f)
			if err != nil {
				return nil
			}
			out[f.Name] = string(content)
		}
		return out
	}
	ca, cb := read(a), read(b)
	return ca != nil && reflect.DeepEqual(ca, cb)
}
