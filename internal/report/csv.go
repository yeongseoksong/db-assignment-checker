package report

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Header 는 결과 CSV 의 열 이름이다.
var Header = []string{
	"student_id", "check_id", "group", "title", "passed",
	"detail", "checked_at", "checker_version", "hostname", "mac_addresses",
}

const hmacPrefix = "#hmac,"

// Row 는 CSV 한 줄(검사 하나 또는 TOTAL)이다.
type Row struct {
	StudentID, CheckID, Group, Title string
	Passed                           bool
	Detail, CheckedAt, Version, Host string
	MACs                             string // 실행한 기기의 MAC 주소들 (";" 구분, 정렬됨)
}

func (r Row) record() []string {
	return []string{
		r.StudentID, r.CheckID, r.Group, r.Title,
		strconv.FormatBool(r.Passed),
		r.Detail, r.CheckedAt, r.Version, r.Host, r.MACs,
	}
}

func computeMAC(body, secret []byte) string {
	m := hmac.New(sha256.New, secret)
	m.Write(body)
	return hex.EncodeToString(m.Sum(nil))
}

// BuildCSV 는 헤더와 행을 CSV 로 쓰고 마지막에 `#hmac,<hex>` 줄을 붙인다.
func BuildCSV(rows []Row, secret []byte) ([]byte, error) {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	if err := w.Write(Header); err != nil {
		return nil, err
	}
	for _, r := range rows {
		if err := w.Write(r.record()); err != nil {
			return nil, err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, err
	}
	body := buf.Bytes()
	out := append([]byte{}, body...)
	out = append(out, []byte(hmacPrefix+computeMAC(body, secret)+"\n")...)
	return out, nil
}

// ParseCSV 는 BuildCSV 결과를 읽어 (헤더 포함) 레코드를 돌려준다.
// HMAC 이 맞지 않으면 레코드와 함께 integrityErr 를 돌려준다.
// 레코드를 읽을 수 없으면 records 는 nil 이고 integrityErr 에 사유가 담긴다.
func ParseCSV(data, secret []byte) (records [][]string, integrityErr error) {
	trimmed := bytes.TrimRight(data, "\r\n")
	idx := bytes.LastIndex(trimmed, []byte("\n"+hmacPrefix))
	if idx < 0 {
		return nil, errors.New("#hmac 줄이 없습니다")
	}
	body := data[:idx+1]
	macLine := strings.TrimSpace(string(trimmed[idx+1+len(hmacPrefix):]))
	records, err := csv.NewReader(bytes.NewReader(body)).ReadAll()
	if err != nil {
		return nil, fmt.Errorf("CSV 해석 실패: %w", err)
	}
	if !hmac.Equal([]byte(macLine), []byte(computeMAC(body, secret))) {
		return records, errors.New("HMAC 불일치")
	}
	return records, nil
}
