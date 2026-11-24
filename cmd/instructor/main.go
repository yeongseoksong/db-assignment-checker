// instructor 는 교수자 전용 도구다 (학생에게 배포하지 않는다).
//
//	instructor keygen  -dir keys
//	instructor decrypt -key keys/private.key -hmac keys/hmac.secret -out merged.csv results/*.enc
package main

import (
	"archive/zip"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

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
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return err
	}
	enc := base64.StdEncoding.EncodeToString
	files[filepath.Join(*dir, "private.key")] = enc(priv.Bytes()) + "\n"
	files[filepath.Join(*dir, "public.key")] = enc(priv.PublicKey().Bytes()) + "\n"
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
	priv, err := ecdh.X25519().NewPrivateKey(privBytes)
	if err != nil {
		return fmt.Errorf("개인키가 올바르지 않습니다: %w", err)
	}
	secret, err := readKeyFile(*hmacPath)
	if err != nil {
		return err
	}

	out, err := os.Create(*outPath)
	if err != nil {
		return err
	}
	defer out.Close()
	out.WriteString("\xef\xbb\xbf") // 엑셀에서 한글이 깨지지 않도록 BOM
	w := csv.NewWriter(out)
	w.Write(append(append([]string{}, report.Header...), "integrity", "source_file"))

	logDir := filepath.Join(filepath.Dir(*outPath), "logs")
	invalid, logs := 0, 0
	for _, path := range inputs {
		base := filepath.Base(path)
		sub := readSubmission(path, priv, secret)
		if sub.Integrity != "OK" {
			invalid++
			fmt.Fprintf(os.Stderr, "경고: %s: %s\n", base, sub.Integrity)
		}
		records := sub.Records
		if records == nil {
			// 읽을 수 없는 파일도 흔적을 남긴다. 학번은 파일 이름에서 추정한다.
			row := make([]string, len(report.Header))
			m := reFileID.FindStringSubmatch(base)
			if m != nil {
				row[0] = m[1]
			}
			records = [][]string{row}
		}
		for _, rec := range records {
			w.Write(append(append([]string{}, rec...), sub.Integrity, base))
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
	fmt.Printf("제출 %d개 병합 (정상 %d, 의심 %d) -> %s\n", len(inputs), len(inputs)-invalid, invalid, *outPath)
	fmt.Printf("로그 %d개 복호화 -> %s\n", logs, logDir)
	return nil
}

// submission 은 제출 zip 하나를 풀어 읽은 결과다.
type submission struct {
	Integrity string     // "OK" 또는 "INVALID: 사유"
	Records   [][]string // 헤더를 뺀 CSV 레코드 (읽지 못했으면 nil)
	LogName   string     // 평문 로그 파일 이름 (checker_<학번>_<시각>.log)
	Log       []byte     // 평문 로그 (없거나 못 풀었으면 nil)
}

// readSubmission 은 제출 zip 에서 결과(.enc)와 로그(.log.enc)를 꺼내 복호화하고 결과의 HMAC 을 검증한다.
func readSubmission(path string, priv *ecdh.PrivateKey, secret []byte) submission {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return submission{Integrity: "INVALID: zip 을 열 수 없음 (" + err.Error() + ")"}
	}
	defer zr.Close()

	var sealedResult, sealedLog []byte
	var logName string
	for _, f := range zr.File {
		data, err := readZipEntry(f)
		if err != nil {
			return submission{Integrity: "INVALID: zip 항목 " + f.Name + " 을 읽을 수 없음 (" + err.Error() + ")"}
		}
		if strings.HasSuffix(f.Name, ".log.enc") {
			sealedLog = data
			logName = strings.TrimSuffix(filepath.Base(f.Name), ".enc")
		} else if strings.HasSuffix(f.Name, ".enc") {
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

func readZipEntry(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// writeLog 는 복호화한 로그를 logDir 에 쓴다.
func writeLog(logDir, name string, data []byte) error {
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(logDir, name), data, 0o644)
}
