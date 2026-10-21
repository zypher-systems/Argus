package api

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"

	"golang.org/x/crypto/scrypt"
)

// An export is a restorable backup, so it carries credentials in clear. A
// passphrase seals it for the trip to wherever backups are kept.

const (
	encryptedExportFormat = "argus-encrypted"
	minExportPassphrase   = 12
	scryptN               = 1 << 15
	scryptR               = 8
	scryptP               = 1
)

var errExportPassphrase = errors.New("the passphrase does not open this export")

type encryptedExport struct {
	Format     string `json:"format"`
	Version    int    `json:"version"`
	KDF        string `json:"kdf"`
	N          int    `json:"n"`
	R          int    `json:"r"`
	P          int    `json:"p"`
	Salt       string `json:"salt"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

func exportKey(passphrase string, salt []byte, n, r, p int) ([]byte, error) {
	return scrypt.Key([]byte(passphrase), salt, n, r, p, 32)
}

func sealExport(plain []byte, passphrase string) (encryptedExport, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return encryptedExport{}, err
	}
	key, err := exportKey(passphrase, salt, scryptN, scryptR, scryptP)
	if err != nil {
		return encryptedExport{}, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return encryptedExport{}, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return encryptedExport{}, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return encryptedExport{}, err
	}
	enc := base64.StdEncoding
	return encryptedExport{
		Format: encryptedExportFormat, Version: 1, KDF: "scrypt",
		N: scryptN, R: scryptR, P: scryptP,
		Salt:       enc.EncodeToString(salt),
		Nonce:      enc.EncodeToString(nonce),
		Ciphertext: enc.EncodeToString(aead.Seal(nil, nonce, plain, []byte(encryptedExportFormat))),
	}, nil
}

// isEncryptedExport reports whether raw is a sealed export.
func isEncryptedExport(raw []byte) bool {
	var probe struct {
		Format string `json:"format"`
	}
	return json.Unmarshal(raw, &probe) == nil && probe.Format == encryptedExportFormat
}

// maxExportKeyMemory is the most memory opening an export may ask scrypt for.
const maxExportKeyMemory = 64 << 20

func openExport(raw []byte, passphrase string) ([]byte, error) {
	var e encryptedExport
	if err := json.Unmarshal(raw, &e); err != nil || e.Format != encryptedExportFormat {
		return nil, errors.New("not an encrypted Argus export")
	}
	if e.Version != 1 || e.KDF != "scrypt" {
		return nil, errors.New("this export was made by a newer Argus")
	}
	// Refuse parameters that would tie up the server: a file is untrusted
	// input, and scrypt takes 128*N*r bytes before the passphrase can even
	// be checked. Argus writes N=2^15, r=8 (32 MiB); the ceiling is twice
	// that, where the old per-number limits allowed 2 GiB.
	//
	// p repeats the whole derivation, so it counts toward the same ceiling:
	// the largest r is only allowed with p of 1.
	if e.N < 1<<14 || e.N&(e.N-1) != 0 || e.R < 1 || e.P < 1 || e.P > 4 ||
		e.N > maxExportKeyMemory/128 || e.R*e.P > maxExportKeyMemory/128/e.N {
		return nil, errors.New("export uses unsupported key parameters")
	}
	dec := base64.StdEncoding
	salt, err1 := dec.DecodeString(e.Salt)
	nonce, err2 := dec.DecodeString(e.Nonce)
	ct, err3 := dec.DecodeString(e.Ciphertext)
	if err1 != nil || err2 != nil || err3 != nil {
		return nil, errors.New("export is damaged")
	}
	key, err := exportKey(passphrase, salt, e.N, e.R, e.P)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(nonce) != aead.NonceSize() {
		return nil, errors.New("export is damaged")
	}
	plain, err := aead.Open(nil, nonce, ct, []byte(encryptedExportFormat))
	if err != nil {
		return nil, errExportPassphrase
	}
	return plain, nil
}
