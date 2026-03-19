package core

import (
	"crypto/aes"
	"crypto/cipher"
	crand "crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"

	"golang.org/x/crypto/scrypt"
)

const (
	scryptN      = 1 << 15
	scryptR      = 8
	scryptP      = 1
	scryptKeyLen = 32
	saltSize     = 32
	chunkSize    = 64 * 1024 // 64KB plaintext per chunk
)

func GenerateStrongPassword() (string, error) {
	buf := make([]byte, 32)
	if _, err := crand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func DerivePasswordHash(password string) (hashB64 string, salt []byte, err error) {
	salt = make([]byte, 16)
	if _, err = crand.Read(salt); err != nil {
		return
	}

	derived, err := scrypt.Key([]byte(password), salt, scryptN, scryptR, scryptP, scryptKeyLen)
	if err != nil {
		return
	}

	hashB64 = base64.StdEncoding.EncodeToString(derived)
	return
}

// DeriveKey derives a 32-byte AES key from a password and salt using Scrypt.
func DeriveKey(password string, salt []byte) ([]byte, error) {
	return scrypt.Key([]byte(password), salt, scryptN, scryptR, scryptP, scryptKeyLen)
}

// GenerateSalt returns a cryptographically random salt for key derivation.
func GenerateSalt() ([]byte, error) {
	salt := make([]byte, saltSize)
	if _, err := crand.Read(salt); err != nil {
		return nil, err
	}
	return salt, nil
}

// encryptWriter wraps a destination writer and encrypts data in chunks using AES-256-GCM.
// Format per chunk: [4-byte big-endian ciphertext length][12-byte nonce][ciphertext + GCM tag]
// EOF marker: 4 zero bytes.
type encryptWriter struct {
	dst    io.Writer
	aead   cipher.AEAD
	buf    []byte
	nonce  uint64
	closed bool
}

// NewEncryptWriter returns a WriteCloser that encrypts data in chunks.
// Call Close() to flush the final chunk and write the EOF marker.
func NewEncryptWriter(dst io.Writer, key []byte) (io.WriteCloser, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gcm: %w", err)
	}
	return &encryptWriter{
		dst:  dst,
		aead: aead,
		buf:  make([]byte, 0, chunkSize),
	}, nil
}

func (w *encryptWriter) Write(p []byte) (int, error) {
	written := 0
	for len(p) > 0 {
		space := chunkSize - len(w.buf)
		if space > len(p) {
			space = len(p)
		}
		w.buf = append(w.buf, p[:space]...)
		p = p[space:]
		written += space

		if len(w.buf) == chunkSize {
			if err := w.flushChunk(); err != nil {
				return written, err
			}
		}
	}
	return written, nil
}

func (w *encryptWriter) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true

	// Flush remaining data
	if len(w.buf) > 0 {
		if err := w.flushChunk(); err != nil {
			return err
		}
	}

	// Write EOF marker (4 zero bytes)
	var eof [4]byte
	_, err := w.dst.Write(eof[:])
	return err
}

func (w *encryptWriter) flushChunk() error {
	// Build nonce from counter
	nonce := make([]byte, w.aead.NonceSize())
	binary.BigEndian.PutUint64(nonce[4:], w.nonce)
	w.nonce++

	ciphertext := w.aead.Seal(nil, nonce, w.buf, nil)

	// Write length (of nonce + ciphertext)
	totalLen := uint32(len(nonce) + len(ciphertext))
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], totalLen)

	if _, err := w.dst.Write(lenBuf[:]); err != nil {
		return err
	}
	if _, err := w.dst.Write(nonce); err != nil {
		return err
	}
	if _, err := w.dst.Write(ciphertext); err != nil {
		return err
	}

	w.buf = w.buf[:0]
	return nil
}

// decryptReader reads and decrypts chunked AES-256-GCM data.
type decryptReader struct {
	src    io.Reader
	aead   cipher.AEAD
	buf    []byte // decrypted plaintext buffer
	offset int
	done   bool
}

// NewDecryptReader returns a Reader that decrypts chunked AES-256-GCM data.
func NewDecryptReader(src io.Reader, key []byte) (io.Reader, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gcm: %w", err)
	}
	return &decryptReader{
		src:  src,
		aead: aead,
	}, nil
}

func (r *decryptReader) Read(p []byte) (int, error) {
	// Serve from buffer first
	if r.offset < len(r.buf) {
		n := copy(p, r.buf[r.offset:])
		r.offset += n
		return n, nil
	}

	if r.done {
		return 0, io.EOF
	}

	// Read next chunk
	var lenBuf [4]byte
	if _, err := io.ReadFull(r.src, lenBuf[:]); err != nil {
		return 0, fmt.Errorf("read chunk length: %w", err)
	}

	totalLen := binary.BigEndian.Uint32(lenBuf[:])
	if totalLen == 0 {
		r.done = true
		return 0, io.EOF
	}

	chunk := make([]byte, totalLen)
	if _, err := io.ReadFull(r.src, chunk); err != nil {
		return 0, fmt.Errorf("read chunk data: %w", err)
	}

	nonceSize := r.aead.NonceSize()
	if int(totalLen) < nonceSize {
		return 0, fmt.Errorf("chunk too small: %d bytes", totalLen)
	}

	nonce := chunk[:nonceSize]
	ciphertext := chunk[nonceSize:]

	plaintext, err := r.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return 0, fmt.Errorf("decrypt chunk: %w", err)
	}

	r.buf = plaintext
	r.offset = 0

	n := copy(p, r.buf[r.offset:])
	r.offset += n
	return n, nil
}
