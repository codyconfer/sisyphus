package backup

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const KeyBytes = 32

func NewKey() ([]byte, error) {
	k := make([]byte, KeyBytes)
	if _, err := rand.Read(k); err != nil {
		return nil, err
	}
	return k, nil
}

func Archive(ctx context.Context, paths []string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	n := 0
	seen := map[string]bool{}
	for _, p := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("reading %s: %w", p, err)
		}
		name := filepath.Base(p)
		if seen[name] {
			return nil, fmt.Errorf("duplicate backup entry %q: paths with the same basename collide", name)
		}
		seen[name] = true
		hdr := &tar.Header{
			Name:    name,
			Mode:    0o600,
			Size:    int64(len(data)),
			ModTime: time.Now(),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, err
		}
		if _, err := tw.Write(data); err != nil {
			return nil, err
		}
		n++
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, fmt.Errorf("nothing to back up (no files found)")
	}
	return buf.Bytes(), nil
}

func Encrypt(plaintext, key []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

func Decrypt(sealed, key []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	ns := gcm.NonceSize()
	if len(sealed) < ns {
		return nil, fmt.Errorf("ciphertext too short")
	}
	return gcm.Open(nil, sealed[:ns], sealed[ns:], nil)
}

func newGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != KeyBytes {
		return nil, fmt.Errorf("backup key must be %d bytes, got %d", KeyBytes, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func Restore(ctx context.Context, sealed, key []byte, destDir string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	plain, err := Decrypt(sealed, key)
	if err != nil {
		return nil, fmt.Errorf("decrypting backup (wrong key?): %w", err)
	}
	entries, err := Extract(plain)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(destDir, 0o700); err != nil {
		return nil, fmt.Errorf("creating %s: %w", destDir, err)
	}
	names := make([]string, 0, len(entries))
	for name, data := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		base := filepath.Base(name)
		if err := os.WriteFile(filepath.Join(destDir, base), data, 0o600); err != nil {
			return nil, fmt.Errorf("writing %s: %w", base, err)
		}
		names = append(names, base)
	}
	sort.Strings(names)
	return names, nil
}

const maxEntryBytes = 256 << 20

func Extract(archive []byte) (map[string][]byte, error) {
	out := map[string][]byte{}
	tr := tar.NewReader(bytes.NewReader(archive))
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		name := filepath.Base(filepath.Clean(hdr.Name))
		if name == "." || name == ".." || name == "" || name == string(filepath.Separator) {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(tr, maxEntryBytes+1))
		if err != nil {
			return nil, err
		}
		if int64(len(data)) > maxEntryBytes {
			return nil, fmt.Errorf("backup entry %q exceeds %d bytes", name, maxEntryBytes)
		}
		out[name] = data
	}
	return out, nil
}
