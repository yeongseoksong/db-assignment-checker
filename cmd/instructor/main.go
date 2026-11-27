// instructor 는 교수자 전용 도구다 (학생에게 배포하지 않는다).
//
//	instructor keygen  -dir keys
//	instructor decrypt -key keys/private.key -hmac keys/hmac.secret -out merged.csv results/*.enc
package main

import (
	"archive/zip"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"dbchecker/internal/checks"
	"dbchecker/internal/report"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "keygen":
		err = keygen(os.Args[2:])
	case "decrypt":
		err = decrypt(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "오류:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `사용법:
  instructor keygen  -dir keys
  instructor decrypt -key keys/private.key -hmac keys/hmac.secret -out merged.csv submissions/*.zip
    (로그는 merged.csv 옆 logs/ 폴더에 평문 .log 로 풀립니다)`)
}

func keygen(args []string) error {
	fs := flag.NewFlagSet("keygen", flag.ExitOnError)
	dir := fs.String("dir", "keys", "키를 저장할 폴더")
	fs.Parse(args)

	files := map[string]string{} // 파일 경로 -> 내용
	priv, err := report.GenerateKey()
	if err != nil {
		return err
	}
	privDER, err := report.MarshalPrivateKey(priv)
	if err != nil {
		return err
	}
	pubDER, err := report.MarshalPublicKey(&priv.PublicKey)
	if err != nil {
		return err
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return err
	}
	enc := base64.StdEncoding.EncodeToString
	files[filepath.Join(*dir, "private.key")] = enc(privDER) + "\n" // RSA 2048 개인키, PKCS#8 DER 을 base64 한 줄로
	files[filepath.Join(*dir, "public.key")] = enc(pubDER) + "\n"   // RSA 공개키, PKIX DER 을 base64 한 줄로
	files[filepath.Join(*dir, "hmac.secret")] = enc(secret) + "\n"

	// 하나라도 이미 있으면 아무것도 쓰지 않는다 (기존 키를 덮어쓰면 이미 받은 .enc 를 못 연다).
	for path := range files {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("%s 가 이미 있습니다. 덮어쓰지 않습니다. 새 키가 필요하면 keys 폴더를 직접 옮기거나 지운 뒤 다시 실행하세요", path)
		}
	}
	if err := os.MkdirAll(*dir, 0o700); err != nil {
		return err
	}
	for path, content := range files {
		mode := os.FileMode(0o644)
		if strings.HasSuffix(path, "private.key") || strings.HasSuffix(path, "hmac.secret") {
			mode = 0o600
		}
		if err := os.WriteFile(path, []byte(content), mode); err != nil {
			return err
		}
	}
	fmt.Printf("키를 만들었습니다: %s/private.key, public.key, hmac.secret\n", *dir)
	fmt.Println("private.key 와 hmac.secret 은 절대 학생에게 공유하지 마세요. 분실하면 결과를 복호화할 수 없습니다.")
	return nil
}

func readKeyFile(path string) ([]byte, error) {
	text, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("키 파일 %s 를 읽을 수 없습니다. make keys 로 먼저 만드세요: %w", path, err)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(text)))
	if err != nil {
		return nil, fmt.Errorf("키 파일 %s 가 base64 형식이 아닙니다: %w", path, err)
	}
	return raw, nil
}

var reFileID = regexp.MustCompile(`^submit_([A-Za-z0-9]+)_`)

func decrypt(args []string) error {
	fs := flag.NewFlagSet("decrypt", flag.ExitOnError)
	keyPath := fs.String("key", "keys/private.key", "개인키 파일")
	hmacPath := fs.String("hmac", "keys/hmac.secret", "HMAC 비밀키 파일")
	outPath := fs.String("out", "merged.csv", "병합 결과 CSV")
	fs.Parse(args)
	inputs := fs.Args()
	if len(inputs) == 0 {
		return errors.New("제출 zip 파일을 하나 이상 지정하세요 (예: submissions/*.zip)")
	}
	privBytes, err := readKeyFile(*keyPath)
	if err != nil {
		return err
	}
	priv, err := report.ParsePrivateKey(privBytes)
	if err != nil {
		return fmt.Errorf("개인키가 올바른 RSA 개인키가 아닙니다 (이전 형식의 키라면 make keys 로 새로 만드세요): %w", err)
	}
	secret, err := readKeyFile(*hmacPath)
	if err != nil {
		return err
	}

	out, err := os.Create(*outPath)
	if err != nil {
		return err
	}
	defer out.Close()                                          // 아래에서 정상 경로의 Close 오류를 따로 확인한다
	if _, err := out.WriteString("\xef\xbb\xbf"); err != nil { // 엑셀에서 한글이 깨지지 않도록 BOM
		return fmt.Errorf("%s 에 쓸 수 없습니다: %w", *outPath, err)
	}
	w := csv.NewWriter(out)
	_ = w.Write(append(append([]string{}, report.Header...), "score", "max_score", "integrity", "source_file")) // 쓰기 오류는 Flush 뒤 w.Error() 로 한꺼번에 확인한다

	logDir := filepath.Join(filepath.Dir(*outPath), "logs")
	invalid, logs := 0, 0
	var merged [][]string // 정상적으로 읽은 레코드만 (MAC 중복 검사용)
	for _, path := range inputs {
		base := filepath.Base(path)
		sub := readSubmission(path, priv, secret)
		if sub.Integrity != "OK" {
			invalid++
			fmt.Fprintf(os.Stderr, "경고: %s: %s\n", base, sub.Integrity)
		}
		records := padRecords(sub.Records)
		for _, warning := range fillRoundsFromLog(records, sub.Log) {
			fmt.Fprintf(os.Stderr, "경고: %s: %s\n", base, warning)
		}
		merged = append(merged, records...)
		if records == nil {
			// 읽을 수 없는 파일도 흔적을 남긴다. 학번은 파일 이름에서 추정한다.
			row := make([]string, len(report.Header))
			m := reFileID.FindStringSubmatch(base)
			if m != nil {
				row[0] = m[1]
			}
			records = [][]string{row}
		}
		scores := make([][2]string, len(records)) // 서명이 맞지 않는 제출은 점수를 비워 둔다
		if sub.Integrity == "OK" {
			scores = scoreRecords(records)
		}
		for i, rec := range records {
			_ = w.Write(append(append([]string{}, rec...), scores[i][0], scores[i][1], sub.Integrity, base)) // 위와 같음
		}
		if sub.Log != nil {
			if err := writeLog(logDir, sub.LogName, sub.Log); err != nil {
				fmt.Fprintf(os.Stderr, "경고: %s: 로그를 쓸 수 없음: %v\n", base, err)
			} else {
				logs++
			}
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return err
	}
	if err := out.Close(); err != nil { // 디스크가 꽉 찼으면 여기서 드러난다
		return fmt.Errorf("%s 를 저장하지 못했습니다: %w", *outPath, err)
	}
	fmt.Printf("제출 %d개 병합 (정상 %d, 의심 %d) -> %s\n", len(inputs), len(inputs)-invalid, invalid, *outPath)
	fmt.Printf("로그 %d개 복호화 -> %s\n", logs, logDir)
	for _, p := range sharedDevices(merged) {
		fmt.Fprintf(os.Stderr, "경고: %s 와 %s 의 MAC 주소가 겹침 (%d/%d개, 다 겹치면 같은 기기일 가능성이 큼)\n", p.A, p.B, p.Shared, p.Fewer)
	}
	return nil
}

// devicePair 는 MAC 주소가 겹치는 학번 두 개다.
type devicePair struct {
	A, B   string // A < B
	Shared int    // 겹친 MAC 개수
	Fewer  int    // 두 학번 중 MAC 이 적은 쪽의 개수 (같은 기기면 Shared 와 거의 같다)
}

// sharedDevices 는 MAC 주소가 하나라도 겹치는 학번 쌍을 겹친 개수가 많은 순으로 돌려준다.
// 가상 인터페이스 주소는 기기끼리 우연히 한두 개 겹칠 수 있으므로 판단은 Shared/Fewer 비율로 한다.
func sharedDevices(records [][]string) []devicePair {
	macCol := slices.Index(report.Header, "mac_addresses")
	macsByID := map[string]map[string]bool{}
	for _, rec := range records {
		if len(rec) <= macCol || rec[0] == "" {
			continue
		}
		if macsByID[rec[0]] == nil {
			macsByID[rec[0]] = map[string]bool{}
		}
		for _, mac := range strings.Split(rec[macCol], ";") {
			if isDistinctiveMAC(mac) {
				macsByID[rec[0]][mac] = true
			}
		}
	}
	var ids []string
	for id := range macsByID {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var pairs []devicePair
	for i := range ids {
		for j := i + 1; j < len(ids); j++ {
			a, b := macsByID[ids[i]], macsByID[ids[j]]
			shared := 0
			for mac := range a {
				if b[mac] {
					shared++
				}
			}
			if shared == 0 {
				continue
			}
			pairs = append(pairs, devicePair{A: ids[i], B: ids[j], Shared: shared, Fewer: min(len(a), len(b))})
		}
	}
	sort.SliceStable(pairs, func(i, j int) bool { return pairs[i].Shared > pairs[j].Shared })
	return pairs
}

// isDistinctiveMAC 은 기기를 구별하는 데 쓸 만한 MAC 인지 본다.
// 뒤 3바이트가 모두 0 인 주소(예: VirtualBox host-only 0a:00:27:00:00:00)는 여러 기기에서 똑같이 나와서 뺀다.
func isDistinctiveMAC(s string) bool {
	hw, err := net.ParseMAC(s)
	if err != nil || len(hw) < 6 {
		return false
	}
	return hw[3]|hw[4]|hw[5] != 0
}

// padRecords 는 열이 적은 레코드의 뒤를 빈 칸으로 채운다.
// v1.0.0 채점기는 rounds_passed, rounds_total 열이 없었다. 새 열은 항상 맨 끝에 붙이므로 앞 열은 그대로 맞는다.
func padRecords(records [][]string) [][]string {
	for i, rec := range records {
		for len(rec) < len(report.Header) {
			rec = append(rec, "")
		}
		records[i] = rec
	}
	return records
}

// scoreRecords 는 한 제출의 레코드마다 [점수, 배점] 을 계산한다. 배점은 internal/checks 의 검사 표가 기준이다.
// 맞은 건수(rounds)가 있으면 배점 × 맞은 건수 / 전체 건수, 없으면 통과 시 배점, 실패 시 0 이다.
// TOTAL 행은 다른 행 점수의 합과 전체 배점이다. 검사 표에 없는 ID 는 빈 칸이다.
func scoreRecords(records [][]string) [][2]string {
	points := map[string]float64{}
	maxTotal := 0.0
	for _, c := range checks.All {
		points[c.ID] = c.Points
		maxTotal += c.Points
	}
	idCol := slices.Index(report.Header, "check_id")
	passedCol := slices.Index(report.Header, "passed")
	roundsPassedCol := slices.Index(report.Header, "rounds_passed")
	roundsTotalCol := slices.Index(report.Header, "rounds_total")

	out := make([][2]string, len(records))
	total := 0.0
	totalRow := -1
	for i, rec := range records {
		checkID := rec[idCol]
		if checkID == "TOTAL" {
			totalRow = i
			continue
		}
		checkMax, known := points[checkID]
		if !known {
			continue
		}
		score := 0.0
		roundsPassed, err1 := strconv.Atoi(rec[roundsPassedCol])
		roundsTotal, err2 := strconv.Atoi(rec[roundsTotalCol])
		if err1 == nil && err2 == nil && roundsTotal > 0 {
			score = checkMax * float64(roundsPassed) / float64(roundsTotal)
		} else if rec[passedCol] == "true" {
			score = checkMax
		}
		total += score
		out[i] = [2]string{formatScore(score), formatScore(checkMax)}
	}
	if totalRow >= 0 {
		out[totalRow] = [2]string{formatScore(total), formatScore(maxTotal)}
	}
	return out
}

// formatScore 는 점수를 소수 둘째 자리까지 반올림하고 뒤의 0 은 뺀다 (8.75, 12.5, 100).
func formatScore(v float64) string {
	return strconv.FormatFloat(math.Round(v*100)/100, 'f', -1, 64)
}

// oldRandomRounds 는 rounds 열이 없던 v1.0.0 채점기의 무작위 반복 횟수다.
const oldRandomRounds = 10

// reRoundLog 는 채점기 로그의 무작위 검사 한 건 기록이다.
// 예: msg="무작위 검사" id=A2 round=3 passed=true detail=...
var reRoundLog = regexp.MustCompile(`msg="무작위 검사" id=(A2|A3) round=\d+ passed=(true|false)`)

// fillRoundsFromLog 는 rounds 열이 빈 A2·A3 행(v1.0.0 결과)을 로그에서 센 맞은 건수로 채운다.
// 로그는 암호화만 되고 서명은 없으므로, 결과 행의 통과 여부와 맞지 않으면 채우지 않고 경고를 돌려준다.
// (통과했다면 10/10 이어야 하고, 실패했다면 10/10 이면 안 된다)
func fillRoundsFromLog(records [][]string, log []byte) []string {
	if log == nil {
		return nil
	}
	passedByCheck := map[string]int{}
	for _, m := range reRoundLog.FindAllStringSubmatch(string(log), -1) {
		if m[2] == "true" {
			passedByCheck[m[1]]++
		}
	}
	idCol := slices.Index(report.Header, "check_id")
	passedCol := slices.Index(report.Header, "passed")
	roundsPassedCol := slices.Index(report.Header, "rounds_passed")
	roundsTotalCol := slices.Index(report.Header, "rounds_total")
	var warnings []string
	for _, rec := range records {
		checkID := rec[idCol]
		if (checkID != "A2" && checkID != "A3") || rec[roundsPassedCol] != "" {
			continue
		}
		count := passedByCheck[checkID]
		resultPassed := rec[passedCol] == "true"
		if resultPassed != (count == oldRandomRounds) {
			warnings = append(warnings, fmt.Sprintf("%s 의 로그 건수(%d/%d)가 결과(passed=%v)와 맞지 않아 건수를 비워 둡니다", checkID, count, oldRandomRounds, resultPassed))
			continue
		}
		rec[roundsPassedCol] = strconv.Itoa(count)
		rec[roundsTotalCol] = strconv.Itoa(oldRandomRounds)
	}
	return warnings
}

// submission 은 제출 zip 하나를 풀어 읽은 결과다.
type submission struct {
	Integrity string     // "OK" 또는 "INVALID: 사유"
	Records   [][]string // 헤더를 뺀 CSV 레코드 (읽지 못했으면 nil)
	LogName   string     // 평문 로그 파일 이름 (checker_<학번>_<시각>.log)
	Log       []byte     // 평문 로그 (없거나 못 풀었으면 nil)
}

// readSubmission 은 제출 zip 에서 결과(.enc)와 로그(.log.enc)를 꺼내 복호화하고 결과의 HMAC 을 검증한다.
func readSubmission(path string, priv *rsa.PrivateKey, secret []byte) submission {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return submission{Integrity: "INVALID: zip 을 열 수 없음 (" + err.Error() + ")"}
	}
	defer zr.Close()

	var sealedResult, sealedLog []byte
	var logName string
	for _, f := range zr.File {
		if !strings.HasSuffix(f.Name, ".enc") {
			continue // 결과·로그가 아닌 항목은 읽지 않는다
		}
		// 정상 제출에는 결과와 로그가 하나씩뿐이다. 같은 종류가 또 나오면 더 읽지 않는다
		// (항목마다 16MB 제한이 있어도 개수 제한이 없으면 작은 zip 으로 수 GB 를 읽게 할 수 있다).
		isLog := strings.HasSuffix(f.Name, ".log.enc")
		if (isLog && sealedLog != nil) || (!isLog && sealedResult != nil) {
			return submission{Integrity: "INVALID: zip 에 결과 또는 로그 파일이 두 개 이상 있음"}
		}
		data, err := readZipEntry(f)
		if err != nil {
			return submission{Integrity: "INVALID: zip 항목 " + f.Name + " 을 읽을 수 없음 (" + err.Error() + ")"}
		}
		if isLog {
			sealedLog = data
			logName = strings.TrimSuffix(filepath.Base(f.Name), ".enc")
		} else {
			sealedResult = data
		}
	}

	var sub submission
	if sealedLog != nil {
		plainLog, err := report.Open(priv, sealedLog)
		if err == nil {
			sub.Log = plainLog
			sub.LogName = logName
		}
	}
	if sealedResult == nil {
		sub.Integrity = "INVALID: zip 에 결과 파일(.enc)이 없음"
		return sub
	}
	plain, err := report.Open(priv, sealedResult)
	if err != nil {
		sub.Integrity = "INVALID: " + err.Error()
		return sub
	}
	records, integrityErr := report.ParseCSV(plain, secret)
	if len(records) > 1 {
		sub.Records = records[1:]
	}
	if integrityErr != nil {
		sub.Integrity = "INVALID: " + integrityErr.Error()
		return sub
	}
	if sealedLog == nil {
		sub.Integrity = "INVALID: zip 에 로그 파일(.log.enc)이 없음"
		return sub
	}
	if sub.Log == nil {
		sub.Integrity = "INVALID: 로그 복호화 실패"
		return sub
	}
	sub.Integrity = "OK"
	return sub
}

// maxEntryBytes 는 제출 zip 항목 하나를 풀었을 때 허용하는 최대 크기다.
// 정상 결과·로그는 수십 KB 다. 압축 폭탄(작은 zip 이 수 GB 로 풀림)으로 병합이 죽지 않게 막는다.
const maxEntryBytes = 16 << 20

func readZipEntry(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	// zip 헤더의 크기는 속일 수 있으므로 실제로 읽는 양을 제한한다.
	data, err := io.ReadAll(io.LimitReader(rc, maxEntryBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxEntryBytes {
		return nil, fmt.Errorf("항목이 너무 큽니다 (%dMB 초과)", maxEntryBytes>>20)
	}
	return data, nil
}

// writeLog 는 복호화한 로그를 logDir 에 쓴다.
func writeLog(logDir, name string, data []byte) error {
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(logDir, name), data, 0o644)
}
