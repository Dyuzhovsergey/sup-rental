package password

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestHasherHashesAndVerifiesPassword(t *testing.T) {
	const plainPassword = "sup-more-27"

	hasher := NewHasher()
	firstHash, err := hasher.Hash(plainPassword)
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}
	secondHash, err := hasher.Hash(plainPassword)
	if err != nil {
		t.Fatalf("second Hash() error = %v", err)
	}

	if firstHash == secondHash {
		t.Error("two hashes are equal, want unique salt for each hash")
	}
	if strings.Contains(firstHash, plainPassword) {
		t.Error("encoded hash contains the plain password")
	}
	if !strings.HasPrefix(firstHash, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Errorf("hash = %q, want agreed Argon2id parameters", firstHash)
	}

	match, err := hasher.Verify(plainPassword, firstHash)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if !match {
		t.Error("Verify() = false, want true")
	}

	match, err = hasher.Verify("wrong-password", firstHash)
	if err != nil {
		t.Fatalf("Verify() wrong password error = %v", err)
	}
	if match {
		t.Error("Verify() wrong password = true, want false")
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name     string
		password string
		wantErr  error
	}{
		{name: "minimum length", password: "123456"},
		{name: "allows Unicode", password: "пароль"},
		{name: "preserves whitespace", password: " 1234 "},
		{name: "maximum length", password: strings.Repeat("я", MaxLength)},
		{name: "too short", password: "12345", wantErr: ErrTooShort},
		{
			name:     "too long",
			password: strings.Repeat("a", MaxLength+1),
			wantErr:  ErrTooLong,
		},
		{
			name:     "invalid UTF-8",
			password: string([]byte{0xff, 0xfe, 0xfd, 0xfc, 0xfb, 0xfa}),
			wantErr:  ErrInvalidUTF8,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := Validate(tt.password); !errors.Is(err, tt.wantErr) {
				t.Errorf("Validate() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestHasherRejectsInvalidPassword(t *testing.T) {
	hasher := NewHasher()

	if _, err := hasher.Hash("short"); !errors.Is(err, ErrTooShort) {
		t.Errorf("Hash() error = %v, want %v", err, ErrTooShort)
	}
	if _, err := hasher.Verify("short", "invalid"); !errors.Is(err, ErrTooShort) {
		t.Errorf("Verify() error = %v, want %v", err, ErrTooShort)
	}
}

func TestHasherRejectsMalformedHash(t *testing.T) {
	hasher := NewHasher()
	tests := []struct {
		name        string
		encodedHash string
		wantErr     error
	}{
		{name: "empty", encodedHash: "", wantErr: ErrInvalidHash},
		{name: "wrong algorithm", encodedHash: "$bcrypt$v=19$m=19456,t=2,p=1$c2FsdHNhbHRzYWx0c2FsdA$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaGhhc2g", wantErr: ErrInvalidHash},
		{name: "unsupported version", encodedHash: "$argon2id$v=20$m=19456,t=2,p=1$c2FsdHNhbHRzYWx0c2FsdA$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaGhhc2g", wantErr: ErrUnsupportedHashVersion},
		{name: "invalid parameters", encodedHash: "$argon2id$v=19$m=x,t=2,p=1$c2FsdHNhbHRzYWx0c2FsdA$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaGhhc2g", wantErr: ErrInvalidHash},
		{name: "unsafe memory", encodedHash: "$argon2id$v=19$m=999999,t=2,p=1$c2FsdHNhbHRzYWx0c2FsdA$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaGhhc2g", wantErr: ErrUnsafeHashParameters},
		{name: "invalid base64", encodedHash: "$argon2id$v=19$m=19456,t=2,p=1$%%%$%%%", wantErr: ErrInvalidHash},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			match, err := hasher.Verify("123456", tt.encodedHash)
			if match {
				t.Error("Verify() = true, want false")
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("Verify() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestHasherReportsRandomSourceFailure(t *testing.T) {
	hasher := &Hasher{random: errorReader{err: errors.New("random unavailable")}}

	_, err := hasher.Hash("123456")
	if err == nil || !strings.Contains(err.Error(), "random unavailable") {
		t.Fatalf("Hash() error = %v, want random source error", err)
	}
}

func TestHasherZeroValueIsNotUsable(t *testing.T) {
	var hasher Hasher

	_, err := hasher.Hash("123456")
	if !errors.Is(err, ErrRandomSourceUnavailable) {
		t.Fatalf("Hash() error = %v, want %v", err, ErrRandomSourceUnavailable)
	}
}

func TestHasherUsesCompleteRandomSalt(t *testing.T) {
	salt := bytes.Repeat([]byte{0x42}, saltLength)
	hasher := &Hasher{random: bytes.NewReader(salt)}

	encodedHash, err := hasher.Hash("123456")
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}

	_, decodedSalt, _, err := decodeHash(encodedHash)
	if err != nil {
		t.Fatalf("decodeHash() error = %v", err)
	}
	if !bytes.Equal(decodedSalt, salt) {
		t.Errorf("salt = %x, want %x", decodedSalt, salt)
	}
}

func BenchmarkHasherHash(b *testing.B) {
	hasher := NewHasher()
	b.ReportAllocs()

	for b.Loop() {
		if _, err := hasher.Hash("sup-more-27"); err != nil {
			b.Fatal(err)
		}
	}
}

type errorReader struct {
	err error
}

func (r errorReader) Read(_ []byte) (int, error) {
	return 0, r.err
}
