// Package recovery implements encrypted PostgreSQL backup and isolated restore.
package recovery

import (
	"bufio"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

const (
	chunkSize = 1 << 20
	magic     = "PBDB1"
)

// Toolchain identifies reviewed PostgreSQL client binaries. Prefixes exist for
// isolated integration wrappers and must not contain user input.
type Toolchain struct {
	DumpPath      string
	DumpPrefix    []string
	RestorePath   string
	RestorePrefix []string
}

// DefaultToolchain uses PostgreSQL's pg_dump and pg_restore from PATH.
func DefaultToolchain() Toolchain {
	return Toolchain{DumpPath: "pg_dump", RestorePath: "pg_restore"}
}

// BackupMetadata proves the encrypted archive and source stream digests.
type BackupMetadata struct {
	PlaintextDigest [sha256.Size]byte
	EncryptedDigest [sha256.Size]byte
	PlaintextBytes  int64
}

// BackupFile streams a custom-format dump into an authenticated encrypted file.
func (t Toolchain) BackupFile(ctx context.Context, database, output string,
	key []byte, environment []string) (BackupMetadata, error) {
	if database == "" || output == "" || t.DumpPath == "" {
		return BackupMetadata{}, errors.New("invalid backup request")
	}
	if err := os.MkdirAll(filepath.Dir(output), 0700); err != nil {
		return BackupMetadata{}, errors.New("create backup directory")
	}
	temporary := output + ".tmp"
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return BackupMetadata{}, errors.New("create encrypted backup")
	}
	ok := false
	defer func() {
		_ = file.Close()
		if !ok {
			_ = os.Remove(temporary)
		}
	}()
	args := append([]string{}, t.DumpPrefix...)
	args = append(args, "--format=custom", "--no-owner", "--no-privileges", "--dbname="+database)
	command := exec.CommandContext(ctx, t.DumpPath, args...)
	command.Env = append(os.Environ(), environment...)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return BackupMetadata{}, errors.New("open database dump stream")
	}
	command.Stderr = io.Discard
	if err := command.Start(); err != nil {
		return BackupMetadata{}, errors.New("start database dump")
	}
	metadata, encryptErr := encrypt(stdout, file, key)
	waitErr := command.Wait()
	if encryptErr != nil {
		return BackupMetadata{}, encryptErr
	}
	if waitErr != nil {
		return BackupMetadata{}, errors.New("database dump failed")
	}
	if err := file.Sync(); err != nil {
		return BackupMetadata{}, errors.New("sync encrypted backup")
	}
	if err := file.Close(); err != nil {
		return BackupMetadata{}, errors.New("close encrypted backup")
	}
	if err := os.Rename(temporary, output); err != nil {
		return BackupMetadata{}, errors.New("publish encrypted backup")
	}
	ok = true
	return metadata, nil
}

// RestoreFile decrypts an archive directly into pg_restore. The target database
// must already exist and must be isolated from the source.
func (t Toolchain) RestoreFile(ctx context.Context, database, input string,
	key []byte, environment []string) error {
	if database == "" || input == "" || t.RestorePath == "" {
		return errors.New("invalid restore request")
	}
	file, err := os.Open(input)
	if err != nil {
		return errors.New("open encrypted backup")
	}
	defer file.Close()
	args := append([]string{}, t.RestorePrefix...)
	args = append(args, "--exit-on-error", "--single-transaction", "--no-owner", "--no-privileges", "--dbname="+database)
	command := exec.CommandContext(ctx, t.RestorePath, args...)
	command.Env = append(os.Environ(), environment...)
	stdin, err := command.StdinPipe()
	if err != nil {
		return errors.New("open database restore stream")
	}
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	if err := command.Start(); err != nil {
		return errors.New("start database restore")
	}
	decryptErr := decrypt(file, stdin, key)
	closeErr := stdin.Close()
	waitErr := command.Wait()
	if decryptErr != nil {
		return decryptErr
	}
	if closeErr != nil || waitErr != nil {
		return errors.New("database restore failed")
	}
	return nil
}

func encrypt(source io.Reader, destination io.Writer, key []byte) (BackupMetadata, error) {
	aead, err := newAEAD(key)
	if err != nil {
		return BackupMetadata{}, err
	}
	prefix := make([]byte, 8)
	if _, err := io.ReadFull(rand.Reader, prefix); err != nil {
		return BackupMetadata{}, errors.New("create backup nonce")
	}
	plainHash := sha256.New()
	encryptedHash := sha256.New()
	writer := io.MultiWriter(destination, encryptedHash)
	if _, err := writer.Write(append([]byte(magic), prefix...)); err != nil {
		return BackupMetadata{}, errors.New("write backup header")
	}
	buffer := make([]byte, chunkSize)
	var counter uint32
	var total int64
	for {
		count, readErr := source.Read(buffer)
		if count > 0 {
			if counter == ^uint32(0) {
				return BackupMetadata{}, errors.New("backup exceeds chunk limit")
			}
			counter++
			nonce := makeNonce(prefix, counter)
			var associated [4]byte
			binary.BigEndian.PutUint32(associated[:], counter)
			ciphertext := aead.Seal(nil, nonce, buffer[:count], associated[:])
			var size [4]byte
			binary.BigEndian.PutUint32(size[:], uint32(len(ciphertext)))
			if _, err := writer.Write(size[:]); err != nil {
				return BackupMetadata{}, errors.New("write backup chunk size")
			}
			if _, err := writer.Write(ciphertext); err != nil {
				return BackupMetadata{}, errors.New("write backup chunk")
			}
			_, _ = plainHash.Write(buffer[:count])
			total += int64(count)
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return BackupMetadata{}, errors.New("read database dump")
		}
	}
	if _, err := writer.Write([]byte{0, 0, 0, 0}); err != nil {
		return BackupMetadata{}, errors.New("finish encrypted backup")
	}
	var metadata BackupMetadata
	copy(metadata.PlaintextDigest[:], plainHash.Sum(nil))
	copy(metadata.EncryptedDigest[:], encryptedHash.Sum(nil))
	metadata.PlaintextBytes = total
	return metadata, nil
}

func decrypt(source io.Reader, destination io.Writer, key []byte) error {
	aead, err := newAEAD(key)
	if err != nil {
		return err
	}
	reader := bufio.NewReader(source)
	header := make([]byte, len(magic)+8)
	if _, err := io.ReadFull(reader, header); err != nil || string(header[:len(magic)]) != magic {
		return errors.New("invalid encrypted backup header")
	}
	prefix := header[len(magic):]
	var counter uint32
	for {
		var size [4]byte
		if _, err := io.ReadFull(reader, size[:]); err != nil {
			return errors.New("read encrypted backup chunk size")
		}
		length := binary.BigEndian.Uint32(size[:])
		if length == 0 {
			if _, err := reader.Peek(1); !errors.Is(err, io.EOF) {
				return errors.New("encrypted backup has trailing data")
			}
			return nil
		}
		if length > chunkSize+uint32(aead.Overhead()) {
			return errors.New("encrypted backup chunk exceeds limit")
		}
		ciphertext := make([]byte, length)
		if _, err := io.ReadFull(reader, ciphertext); err != nil {
			return errors.New("read encrypted backup chunk")
		}
		counter++
		nonce := makeNonce(prefix, counter)
		var associated [4]byte
		binary.BigEndian.PutUint32(associated[:], counter)
		plaintext, err := aead.Open(nil, nonce, ciphertext, associated[:])
		if err != nil {
			return errors.New("authenticate encrypted backup")
		}
		if _, err := destination.Write(plaintext); err != nil {
			return errors.New("write database restore stream")
		}
	}
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, errors.New("backup key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errors.New("initialize backup encryption")
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, errors.New("initialize backup authentication")
	}
	return aead, nil
}

func makeNonce(prefix []byte, counter uint32) []byte {
	nonce := make([]byte, 12)
	copy(nonce, prefix)
	binary.BigEndian.PutUint32(nonce[8:], counter)
	return nonce
}
