package recovery

import (
	"bytes"
	"crypto/sha256"
	"testing"
)

func TestChunkedBackupRoundTripAndTamper(t *testing.T) {
	plaintext := bytes.Repeat([]byte("synthetic database archive block\n"), 200000)
	key := bytes.Repeat([]byte{6}, 32)
	var encrypted bytes.Buffer
	metadata, err := encrypt(bytes.NewReader(plaintext), &encrypted, key)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.PlaintextDigest != sha256.Sum256(plaintext) ||
		metadata.EncryptedDigest != sha256.Sum256(encrypted.Bytes()) ||
		metadata.PlaintextBytes != int64(len(plaintext)) {
		t.Fatalf("invalid backup metadata: %+v", metadata)
	}
	if bytes.Contains(encrypted.Bytes(), plaintext[:100]) {
		t.Fatal("backup contains plaintext")
	}
	var restored bytes.Buffer
	if err := decrypt(bytes.NewReader(encrypted.Bytes()), &restored, key); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(restored.Bytes(), plaintext) {
		t.Fatal("restored stream differs")
	}
	tampered := append([]byte(nil), encrypted.Bytes()...)
	tampered[len(tampered)/2] ^= 0xff
	if err := decrypt(bytes.NewReader(tampered), &bytes.Buffer{}, key); err == nil {
		t.Fatal("tampered backup authenticated")
	}
	if err := decrypt(bytes.NewReader(encrypted.Bytes()), &bytes.Buffer{},
		bytes.Repeat([]byte{7}, 32)); err == nil {
		t.Fatal("wrong backup key decrypted archive")
	}
}
