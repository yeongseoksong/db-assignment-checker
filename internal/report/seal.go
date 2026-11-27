// Package report 는 채점 결과의 CSV 직렬화와 암호화(봉인)를 담당한다.
package report

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"errors"
	"fmt"
)

// 파일 형식: magic(5) | RSA 공개키로 암호화한 AES 키(키 길이, 2048비트면 256) | nonce(12) | ciphertext
const (
	magic     = "DBCK2"
	nonceLen  = 12
	aesKeyLen = 32 // AES-256
	rsaBits   = 2048
)

// ErrNotSealed 는 이 프로그램이 만든 봉인 파일이 아닐 때 돌려준다.
var ErrNotSealed = errors.New("DBCK2 형식의 결과 파일이 아닙니다 (파일이 손상되었거나 다른 파일입니다)")

var errOpen = errors.New("복호화 실패: 파일이 변조되었거나 개인키가 맞지 않습니다")

// Seal 은 교수자 공개키로 plaintext 를 암호화한다.
// RSA 는 키 길이보다 긴 데이터를 직접 암호화하지 못하므로, 무작위 AES 키로 본문을 암호화(AES-256-GCM)하고
// 그 AES 키만 RSA 공개키로 암호화(RSA-OAEP, SHA-256)해 함께 저장한다.
// (≈ Java 의 Cipher("RSA/ECB/OAEPWithSHA-256AndMGF1Padding") 로 AES 키를 감싸는 방식)
func Seal(pub *rsa.PublicKey, plaintext []byte) ([]byte, error) {
	key := make([]byte, aesKeyLen)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("AES 키 생성 실패: %w", err)
	}
	wrappedKey, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, pub, key, nil)
	if err != nil {
		return nil, fmt.Errorf("AES 키 암호화 실패: %w", err)
	}
	aead, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, nonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("nonce 생성 실패: %w", err)
	}
	out := make([]byte, 0, len(magic)+len(wrappedKey)+nonceLen+len(plaintext)+aead.Overhead())
	out = append(out, magic...)
	out = append(out, wrappedKey...)
	// 매직+암호화된 AES 키를 AAD 로 묶어 헤더 변조도 탐지한다.
	aad := out
	out = append(out, nonce...)
	return aead.Seal(out, nonce, plaintext, aad), nil
}

// Open 은 Seal 로 만든 데이터를 교수자 개인키로 복호화한다. 변조되면 에러를 돌려준다.
func Open(priv *rsa.PrivateKey, sealed []byte) ([]byte, error) {
	wrappedLen := priv.Size() // RSA 로 암호화한 결과는 항상 키 길이와 같다
	headerLen := len(magic) + wrappedLen + nonceLen
	if len(sealed) < headerLen+16 || string(sealed[:len(magic)]) != magic {
		return nil, ErrNotSealed
	}
	wrappedKey := sealed[len(magic) : len(magic)+wrappedLen]
	nonce := sealed[len(magic)+wrappedLen : headerLen]
	key, err := rsa.DecryptOAEP(sha256.New(), nil, priv, wrappedKey, nil)
	if err != nil {
		return nil, errOpen
	}
	aead, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	plain, err := aead.Open(nil, nonce, sealed[headerLen:], sealed[:len(magic)+wrappedLen])
	if err != nil {
		return nil, errOpen
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

// ---- 키 만들기·저장 형식 (키 파일은 DER 을 base64 로 한 줄에 쓴다) ----

// GenerateKey 는 교수자용 RSA 키쌍을 만든다.
func GenerateKey() (*rsa.PrivateKey, error) {
	return rsa.GenerateKey(rand.Reader, rsaBits)
}

// MarshalPublicKey 는 공개키를 PKIX DER 로 바꾼다 (Java 의 PublicKey.getEncoded() 와 같은 형식).
func MarshalPublicKey(pub *rsa.PublicKey) ([]byte, error) {
	return x509.MarshalPKIXPublicKey(pub)
}

// MarshalPrivateKey 는 개인키를 PKCS#8 DER 로 바꾼다 (Java 의 PrivateKey.getEncoded() 와 같은 형식).
func MarshalPrivateKey(priv *rsa.PrivateKey) ([]byte, error) {
	return x509.MarshalPKCS8PrivateKey(priv)
}

// ParsePublicKey 는 PKIX DER 에서 RSA 공개키를 읽는다.
func ParsePublicKey(der []byte) (*rsa.PublicKey, error) {
	key, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, err
	}
	pub, ok := key.(*rsa.PublicKey)
	if !ok {
		return nil, errors.New("RSA 공개키가 아닙니다")
	}
	return pub, nil
}

// ParsePrivateKey 는 PKCS#8 DER 에서 RSA 개인키를 읽는다.
func ParsePrivateKey(der []byte) (*rsa.PrivateKey, error) {
	key, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, err
	}
	priv, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("RSA 개인키가 아닙니다")
	}
	return priv, nil
}
