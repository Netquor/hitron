package hitron

// Password of the day (PWoD).
//
// The CODA6X runs htx_pod_main from /usr/sbin, which loads libpodalgorithm and
// rewrites the password of the passwd entry whose GECOS is "Default Admin"
// (mso) shortly after local midnight. The ISP provisions the seed in DOCSIS
// TLV43 sub-option 242, so the value is deterministic only on a unit that has
// received a seed.
//
//   msg = "Pa55w0RolHiTr0N" + YYYY + MM + DD + seed
//   h   = SHA1(msg)[0:16]
//   pw  = 16 characters from h through the firmware's [0-9A-Za-z] map
//
// The stored seed is base64(AES-128-ECB(seed)) under a fixed key, with padding
// added only when the seed is not already a multiple of the AES block size.
// Both the key and the prefix are immediates in libpodalgorithm; they are not
// secrets the device can rotate.
//
// Everything here is local arithmetic. Nothing contacts the device.

import (
	"bytes"
	"crypto/aes"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"strings"
	"time"
)

const pwodPrefix = "Pa55w0RolHiTr0N"

var pwodKey = []byte{
	0xb5, 0xc4, 0xba, 0xa2, 0xc6, 0xdf, 0xcb, 0xea,
	0xb6, 0xf8, 0xb6, 0xfe, 0xb8, 0xe7, 0xb5, 0xc4,
}

// PWODPassword returns the CODA6X password of the day for seed on date. Only
// the date's year, month and day are used, so the caller decides whether that
// is the host's local date or UTC.
func PWODPassword(seed string, date time.Time) (string, error) {
	raw, err := latin1Bytes(seed)
	if err != nil {
		return "", err
	}
	msg := make([]byte, 0, len(pwodPrefix)+8+len(raw))
	msg = append(msg, pwodPrefix...)
	msg = append(msg, fmt.Sprintf("%d%02d%02d", date.Year(), int(date.Month()), date.Day())...)
	msg = append(msg, raw...)
	sum := sha1.Sum(msg)
	return pwodCharset(sum[:16]), nil
}

// pwodCharset maps each of the first 16 hash bytes into [0-9A-Za-z]. The
// branches are the firmware's, not a conventional modulo: 0x00-0x2F become
// digits, 0x3A-0x40 step over the punctuation gap, and both 0x5B-0x60 and
// 0x7B-0xFF fold back into letters.
func pwodCharset(digest []byte) string {
	out := make([]byte, 0, 16)
	for _, b := range digest[:16] {
		if b <= 0x2F {
			b = b%10 + 0x30
		}
		if b > 0x39 && b <= 0x40 {
			b += 7
		}
		if b > 0x5A && b <= 0x60 {
			if b%2 == 0 {
				b = 0x5A - b%0x1A
			} else {
				b = b%0x1A + 0x61
			}
		}
		if b > 0x7A {
			b = b%0x1A + 0x61
		}
		out = append(out, b)
	}
	return string(out)
}

// PWODEncryptSeed encodes a plaintext seed the way general.podCfg.defseed
// stores it: AES-128-ECB under the firmware key, base64, and no padding when
// the seed is already block aligned. An empty seed encodes to an empty string.
func PWODEncryptSeed(seed string) (string, error) {
	raw, err := latin1Bytes(seed)
	if err != nil {
		return "", err
	}
	if len(raw)%aes.BlockSize != 0 {
		n := aes.BlockSize - len(raw)%aes.BlockSize
		raw = append(raw, bytes.Repeat([]byte{byte(n)}, n)...)
	}
	if len(raw) == 0 {
		return "", nil
	}
	encrypted, err := ecbCrypt(pwodKey, raw, true)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(encrypted), nil
}

// PWODDecryptSeed reverses PWODEncryptSeed. It keeps two firmware quirks: the
// ciphertext and the plaintext are each cut at the first NUL, and the trailing
// byte is treated as a pad length even when no padding was added. A seed that
// is already block aligned and ends in a nonzero byte therefore decodes short;
// when the plaintext seed is available, use it instead.
func PWODDecryptSeed(encoded string) (string, error) {
	encoded = strings.TrimSpace(encoded)
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		raw, err = base64.RawStdEncoding.DecodeString(encoded)
		if err != nil {
			return "", fmt.Errorf("seed is not valid base64: %w", err)
		}
	}
	if i := bytes.IndexByte(raw, 0); i >= 0 {
		raw = raw[:i]
	}
	raw = raw[:len(raw)-len(raw)%aes.BlockSize]
	if len(raw) == 0 {
		return "", nil
	}
	plain, err := ecbCrypt(pwodKey, raw, false)
	if err != nil {
		return "", err
	}
	if i := bytes.IndexByte(plain, 0); i >= 0 {
		plain = plain[:i]
	}
	if len(plain) == 0 {
		return "", nil
	}
	if n := int(plain[len(plain)-1]); n != 0 {
		if n > len(plain) {
			n = len(plain)
		}
		plain = plain[:len(plain)-n]
	}
	return latin1String(plain), nil
}

// ecbCrypt walks the data one block at a time. crypto/cipher has no ECB mode
// because it is not authenticated, but this is a fixed 16-byte firmware format
// and there is nothing to choose here.
func ecbCrypt(key, data []byte, encrypt bool) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	if len(data)%block.BlockSize() != 0 {
		return nil, fmt.Errorf("seed is %d bytes, not a multiple of %d", len(data), block.BlockSize())
	}
	out := make([]byte, len(data))
	for i := 0; i < len(data); i += block.BlockSize() {
		if encrypt {
			block.Encrypt(out[i:i+block.BlockSize()], data[i:i+block.BlockSize()])
		} else {
			block.Decrypt(out[i:i+block.BlockSize()], data[i:i+block.BlockSize()])
		}
	}
	return out, nil
}

// latin1Bytes turns a seed string into the byte string the firmware hashes.
// A DOCSIS seed is bytes, not text; the CLI takes it in Latin-1 so a typed seed
// matches the reference implementation byte for byte. Above U+00FF there is no
// single-byte mapping, so the encrypted form is the only exact input.
func latin1Bytes(s string) ([]byte, error) {
	out := make([]byte, 0, len(s))
	for _, r := range s {
		if r > 0xFF {
			return nil, fmt.Errorf("seed %q contains a character above U+00FF; pass the encrypted seed instead", s)
		}
		out = append(out, byte(r))
	}
	return out, nil
}

// latin1String is the inverse of latin1Bytes and keeps decrypted bytes valid
// UTF-8 when they are printed or put in JSON.
func latin1String(data []byte) string {
	runes := make([]rune, len(data))
	for i, b := range data {
		runes[i] = rune(b)
	}
	return string(runes)
}
