package identity

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

const storeVersion byte = 1

var (
	// ErrKeyNotFound reports a missing, lost, or deleted local key.
	ErrKeyNotFound = errors.New("identity key not found")
	// ErrKeyExists reports an attempted overwrite of immutable key material.
	ErrKeyExists = errors.New("identity key already exists")
)

// FileStore encrypts identity keys with a caller-supplied local master key.
type FileStore struct {
	path string
	aead cipher.AEAD
	mu   sync.Mutex
}

// NewFileStore creates an encrypted key store. The master key must come from a
// secret reference and is never written by this package.
func NewFileStore(path string, masterKey []byte) (*FileStore, error) {
	if path == "" {
		return nil, errors.New("key store path is required")
	}
	if len(masterKey) != keySize {
		return nil, errors.New("master key must be 32 bytes")
	}
	block, err := aes.NewCipher(masterKey)
	if err != nil {
		return nil, errors.New("initialize key encryption")
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, errors.New("initialize authenticated encryption")
	}
	return &FileStore{path: path, aead: aead}, nil
}

// Put stores new immutable key material.
func (s *FileStore) Put(ctx context.Context, reference string, key []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if reference == "" || len(reference) > 200 || len(key) != keySize {
		return errors.New("invalid identity key record")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	records, err := s.read()
	if err != nil {
		return err
	}
	if _, exists := records[reference]; exists {
		return ErrKeyExists
	}
	records[reference] = append([]byte(nil), key...)
	return s.write(records)
}

// Get returns a copy of key material.
func (s *FileStore) Get(ctx context.Context, reference string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	records, err := s.read()
	if err != nil {
		return nil, err
	}
	key, exists := records[reference]
	if !exists {
		return nil, ErrKeyNotFound
	}
	return append([]byte(nil), key...), nil
}

// Delete irreversibly removes one key reference.
func (s *FileStore) Delete(ctx context.Context, reference string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	records, err := s.read()
	if err != nil {
		return err
	}
	if _, exists := records[reference]; !exists {
		return ErrKeyNotFound
	}
	delete(records, reference)
	return s.write(records)
}

func (s *FileStore) read() (map[string][]byte, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string][]byte{}, nil
	}
	if err != nil {
		return nil, errors.New("read encrypted key store")
	}
	if len(data) < 1+s.aead.NonceSize() || data[0] != storeVersion {
		return nil, errors.New("invalid encrypted key store")
	}
	nonce := data[1 : 1+s.aead.NonceSize()]
	plaintext, err := s.aead.Open(nil, nonce, data[1+s.aead.NonceSize():], []byte{storeVersion})
	if err != nil {
		return nil, errors.New("decrypt key store")
	}
	return decodeRecords(plaintext)
}

func (s *FileStore) write(records map[string][]byte) error {
	plaintext := encodeRecords(records)
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return errors.New("create key store nonce")
	}
	data := append([]byte{storeVersion}, nonce...)
	data = s.aead.Seal(data, nonce, plaintext, []byte{storeVersion})
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return errors.New("create key store directory")
	}
	temporary := s.path + ".tmp"
	if err := os.WriteFile(temporary, data, 0600); err != nil {
		return errors.New("write encrypted key store")
	}
	if err := os.Rename(temporary, s.path); err != nil {
		_ = os.Remove(temporary)
		return errors.New("replace encrypted key store")
	}
	return nil
}

func encodeRecords(records map[string][]byte) []byte {
	keys := make([]string, 0, len(records))
	for key := range records {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	data := make([]byte, 4)
	binary.BigEndian.PutUint32(data, uint32(len(keys)))
	for _, reference := range keys {
		var size [2]byte
		binary.BigEndian.PutUint16(size[:], uint16(len(reference)))
		data = append(data, size[:]...)
		data = append(data, reference...)
		data = append(data, records[reference]...)
	}
	return data
}

func decodeRecords(data []byte) (map[string][]byte, error) {
	if len(data) < 4 {
		return nil, errors.New("invalid encrypted key records")
	}
	count := int(binary.BigEndian.Uint32(data[:4]))
	data = data[4:]
	if count > 1000 {
		return nil, errors.New("encrypted key store exceeds record limit")
	}
	records := make(map[string][]byte, count)
	for range count {
		if len(data) < 2 {
			return nil, errors.New("invalid encrypted key record")
		}
		size := int(binary.BigEndian.Uint16(data[:2]))
		data = data[2:]
		if size < 1 || size > 200 || len(data) < size+keySize {
			return nil, errors.New("invalid encrypted key record")
		}
		reference := string(data[:size])
		data = data[size:]
		if _, exists := records[reference]; exists {
			return nil, fmt.Errorf("duplicate encrypted key reference")
		}
		records[reference] = append([]byte(nil), data[:keySize]...)
		data = data[keySize:]
	}
	if len(data) != 0 {
		return nil, errors.New("invalid encrypted key store trailing data")
	}
	return records, nil
}
