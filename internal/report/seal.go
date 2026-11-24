// Package report 는 채점 결과의 CSV 직렬화와 암호화(봉인)를 담당한다.
package report

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
)

// 파일 형식: magic(5) | 임시 공개키(32) | nonce(12) | ciphertext
const (
	magic       = "DBCK1"
	pubKeyLen   = 32
	nonceLen    = 12
	headerLen   = len(magic) + pubKeyLen + nonceLen
	hkdfContext = "dbchecker result v1"
)

// ErrNotSealed 는 이 프로그램이 만든 봉인 파일이 아닐 때 돌려준다.
var ErrNotSealed = errors.New("DBCK1 형식의 결과 파일이 아닙니다 (파일이 손상되었거나 다른 파일입니다)")

func deriveKey(shared []byte) ([]byte, error) {
	return hkdf.Key(sha256.New, shared, nil, hkdfContext, 32)
}

// Seal 은 교수자 공개키로 plaintext 를 암호화한다 (X25519 ECDH + HKDF-SHA256 + AES-256-GCM).
// 임시 키쌍을 매번 새로 만들므로 공개키만 가진 쪽은 결과를 복호화할 수 없다.
func Seal(pub *ecdh.PublicKey, plaintext []byte) ([]byte, error) {
	eph, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("임시 키 생성 실패: %w", err)
	}
	shared, err := eph.ECDH(pub)
	if err != nil {
		return nil, fmt.Errorf("키 교환 실패: %w", err)
	}
	key, err := deriveKey(shared)
	if err != nil {
		return nil, err
	}
	aead, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, headerLen+len(plaintext)+aead.Overhead())
	out = append(out, magic...)
	out = append(out, eph.PublicKey().Bytes()...)
	nonce := make([]byte, nonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("nonce 생성 실패: %w", err)
	}
	out = append(out, nonce...)
	// 매직+임시 공개키를 AAD 로 묶어 헤더 변조도 탐지한다.
	aad := out[:len(magic)+pubKeyLen]
	return aead.Seal(out, nonce, plaintext, aad), nil
}

// Open 은 Seal 로 만든 데이터를 교수자 개인키로 복호화한다. 변조되면 에러를 돌려준다.
func Open(priv *ecdh.PrivateKey, sealed []byte) ([]byte, error) {
	if len(sealed) < headerLen+16 || string(sealed[:len(magic)]) != magic {
		return nil, ErrNotSealed
	}
	ephBytes := sealed[len(magic) : len(magic)+pubKeyLen]
	nonce := sealed[len(magic)+pubKeyLen : headerLen]
	eph, err := ecdh.X25519().NewPublicKey(ephBytes)
	if err != nil {
		return nil, ErrNotSealed
	}
	shared, err := priv.ECDH(eph)
	if err != nil {
		return nil, fmt.Errorf("키 교환 실패: %w", err)
	}
	key, err := deriveKey(shared)
	if err != nil {
		return nil, err
	}
	aead, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	plain, err := aead.Open(nil, nonce, sealed[headerLen:], sealed[:len(magic)+pubKeyLen])
	if err != nil {
		return nil, errors.New("복호화 실패: 파일이 변조되었거나 개인키가 맞지 않습니다")
	}
	return plain, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
