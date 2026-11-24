// instructor 는 교수자 전용 도구다 (학생에게 배포하지 않는다).
//
//	instructor keygen  -dir keys
//	instructor decrypt -key keys/private.key -hmac keys/hmac.secret -out merged.csv results/*.enc
package main

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
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
  instructor decrypt -key keys/private.key -hmac keys/hmac.secret -out merged.csv results/*.enc
    (*.log.enc 는 merged.csv 옆 logs/ 폴더에 평문 .log 로 풀립니다)`)
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

var reFileID = regexp.MustCompile(`^result_([A-Za-z0-9]+)_`)

func decrypt(args []string) error {
	fs := flag.NewFlagSet("decrypt", flag.ExitOnError)
	keyPath := fs.String("key", "keys/private.key", "개인키 파일")
	hmacPath := fs.String("hmac", "keys/hmac.secret", "HMAC 비밀키 파일")
	outPath := fs.String("out", "merged.csv", "병합 결과 CSV")
	fs.Parse(args)
	inputs := fs.Args()
	if len(inputs) == 0 {
		return errors.New(".enc 파일을 하나 이상 지정하세요 (예: results/*.enc)")
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
	invalid, results, logs := 0, 0, 0
	for _, path := range inputs {
		base := filepath.Base(path)
		if strings.HasSuffix(base, ".log.enc") {
			if err := decryptLog(path, priv, logDir); err != nil {
				fmt.Fprintf(os.Stderr, "경고: %s: %v\n", base, err)
			} else {
				logs++
			}
			continue
		}
		results++
		integrity, records := readOne(path, priv, secret)
		if integrity != "OK" {
			invalid++
			fmt.Fprintf(os.Stderr, "경고: %s: %s\n", base, integrity)
		}
		if records == nil {
			// 읽을 수 없는 파일도 흔적을 남긴다. 학번은 파일 이름에서 추정한다.
			row := make([]string, len(report.Header))
			if m := reFileID.FindStringSubmatch(base); m != nil {
				row[0] = m[1]
			}
			records = [][]string{row}
		}
		for _, rec := range records {
			w.Write(append(append([]string{}, rec...), integrity, base))
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return err
	}
	fmt.Printf("결과 %d개 병합 (정상 %d, 의심 %d) -> %s\n", results, results-invalid, invalid, *outPath)
	if logs > 0 {
		fmt.Printf("로그 %d개 복호화 -> %s\n", logs, logDir)
	}
	return nil
}

// decryptLog 는 암호화된 로그 하나를 logDir 에 평문 .log 로 풀어 쓴다.
func decryptLog(path string, priv *ecdh.PrivateKey, logDir string) error {
	sealed, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	plain, err := report.Open(priv, sealed)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return err
	}
	name := strings.TrimSuffix(filepath.Base(path), ".enc") // checker_<학번>_<시각>.log
	return os.WriteFile(filepath.Join(logDir, name), plain, 0o644)
}

// readOne 은 파일 하나를 복호화하고 HMAC 을 검증한다. 헤더를 뺀 레코드와 integrity 문자열을 돌려준다.
func readOne(path string, priv *ecdh.PrivateKey, secret []byte) (string, [][]string) {
	sealed, err := os.ReadFile(path)
	if err != nil {
		return "INVALID: 파일을 읽을 수 없음 (" + err.Error() + ")", nil
	}
	plain, err := report.Open(priv, sealed)
	if err != nil {
		return "INVALID: " + err.Error(), nil
	}
	records, integrityErr := report.ParseCSV(plain, secret)
	var body [][]string
	if len(records) > 1 {
		body = records[1:]
	}
	if integrityErr != nil {
		return "INVALID: " + integrityErr.Error(), body
	}
	return "OK", body
}
