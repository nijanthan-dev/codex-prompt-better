package identity

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAliasUsesKeyedHMAC(t *testing.T) {
	identifier := []byte("synthetic-low-entropy-id")
	keyA := bytes.Repeat([]byte{1}, keySize)
	keyB := bytes.Repeat([]byte{2}, keySize)
	digestA, err := Alias(keyA, identifier)
	if err != nil {
		t.Fatal(err)
	}
	digestB, err := Alias(keyB, identifier)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(digestA[:], digestB[:]) || !Verify(keyA, identifier, digestA[:]) {
		t.Fatal("keyed alias separation or verification failed")
	}
	plain := sha256.Sum256(identifier)
	if bytes.Equal(digestA[:], plain[:]) {
		t.Fatal("alias unexpectedly equals plain SHA-256")
	}
}

func TestFileStoreLifecycleAndEncryption(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "keys.pbk")
	master := bytes.Repeat([]byte{9}, keySize)
	store, err := NewFileStore(path, master)
	if err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{7}, keySize)
	if err := store.Put(ctx, "identity-v1", key); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(ctx, "identity-v1", key); !errors.Is(err, ErrKeyExists) {
		t.Fatalf("duplicate error = %v", err)
	}
	stored, err := store.Get(ctx, "identity-v1")
	if err != nil || !bytes.Equal(stored, key) {
		t.Fatalf("stored key mismatch: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, key) || bytes.Contains(data, []byte("identity-v1")) {
		t.Fatal("key store contains plaintext material")
	}
	if err := store.Delete(ctx, "identity-v1"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(ctx, "identity-v1"); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("deleted key error = %v", err)
	}
	wrong, err := NewFileStore(path, bytes.Repeat([]byte{8}, keySize))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wrong.Get(ctx, "identity-v1"); err == nil {
		t.Fatal("wrong master key decrypted store")
	}
}
