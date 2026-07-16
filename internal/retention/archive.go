// Package retention implements encrypted local archives before PostgreSQL purge.
package retention

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	store "github.com/nijanthan-dev/codex-prompt-better/internal/store/postgres"
)

const archiveVersion byte = 1

// FileArchiver writes authenticated encrypted normalized archives.
type FileArchiver struct {
	directory string
	keyRef    string
	aead      cipher.AEAD
}

// NewFileArchiver creates an archiver with externally supplied key material.
func NewFileArchiver(directory, keyReference string, key []byte) (*FileArchiver, error) {
	if directory == "" || keyReference == "" || len(key) != 32 {
		return nil, errors.New("invalid archive encryption configuration")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errors.New("initialize archive encryption")
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, errors.New("initialize archive authentication")
	}
	return &FileArchiver{directory: directory, keyRef: keyReference, aead: aead}, nil
}

// Archive encrypts, writes, rereads, and verifies one retention plan.
func (a *FileArchiver) Archive(ctx context.Context, batchID string, plan store.RetentionPlan) (store.ArchiveReceipt, error) {
	if err := ctx.Err(); err != nil {
		return store.ArchiveReceipt{}, err
	}
	if batchID == "" || len(plan.Records) == 0 {
		return store.ArchiveReceipt{}, errors.New("invalid archive request")
	}
	plaintext, err := store.EncodeArchive(plan)
	if err != nil {
		return store.ArchiveReceipt{}, err
	}
	plaintextDigest := sha256.Sum256(plaintext)
	if plaintextDigest != plan.Digest {
		return store.ArchiveReceipt{}, errors.New("archive plan digest mismatch")
	}
	nonce := make([]byte, a.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return store.ArchiveReceipt{}, errors.New("create archive nonce")
	}
	data := append([]byte{archiveVersion}, nonce...)
	data = a.aead.Seal(data, nonce, plaintext, []byte{archiveVersion})
	encryptedDigest := sha256.Sum256(data)
	name := hex.EncodeToString(encryptedDigest[:]) + ".pba"
	if err := os.MkdirAll(a.directory, 0700); err != nil {
		return store.ArchiveReceipt{}, errors.New("create archive directory")
	}
	path := filepath.Join(a.directory, name)
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, data, 0600); err != nil {
		return store.ArchiveReceipt{}, errors.New("write encrypted archive")
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return store.ArchiveReceipt{}, errors.New("replace encrypted archive")
	}
	if err := a.verify(path, plaintextDigest); err != nil {
		return store.ArchiveReceipt{}, err
	}
	return store.ArchiveReceipt{
		BatchID: batchID, ArchiveReference: "sha256:" + hex.EncodeToString(encryptedDigest[:]),
		EncryptionKeyReference: a.keyRef, EncryptedDigest: encryptedDigest,
		PlaintextDigest: plaintextDigest, VerifiedAt: time.Now().UTC(),
	}, nil
}

func (a *FileArchiver) verify(path string, expected [sha256.Size]byte) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return errors.New("read encrypted archive")
	}
	if len(data) < 1+a.aead.NonceSize() || data[0] != archiveVersion {
		return errors.New("invalid encrypted archive")
	}
	nonce := data[1 : 1+a.aead.NonceSize()]
	plaintext, err := a.aead.Open(nil, nonce, data[1+a.aead.NonceSize():], []byte{archiveVersion})
	if err != nil {
		return errors.New("verify encrypted archive")
	}
	if sha256.Sum256(plaintext) != expected {
		return errors.New("archive integrity mismatch")
	}
	return nil
}
