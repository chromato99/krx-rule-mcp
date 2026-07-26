package security

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	MaxBearerTokenRegistryBytes = 1 << 20
	MaxBearerTokens             = 1024
	maxBearerTokenLength        = 4096
)

type AuthMode string

const (
	AuthModeRequired AuthMode = "required"
	AuthModeDisabled AuthMode = "disabled"
)

var (
	bearerTokenIDPattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	bearerTokenDigestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

type bearerTokenFile struct {
	Version int                     `yaml:"version"`
	Tokens  []bearerTokenFileRecord `yaml:"tokens"`
}

type bearerTokenFileRecord struct {
	ID      string `yaml:"id"`
	SHA256  string `yaml:"sha256"`
	Enabled *bool  `yaml:"enabled"`
}

// BearerTokenRegistry is an immutable startup snapshot. Updating the source
// file has no effect until the server is restarted and loads a new snapshot.
type BearerTokenRegistry struct {
	activeDigests [][sha256.Size]byte
	fileDigest    string
}

func ParseAuthMode(value string) (AuthMode, error) {
	mode := AuthMode(strings.ToLower(strings.TrimSpace(value)))
	switch mode {
	case AuthModeRequired, AuthModeDisabled:
		return mode, nil
	default:
		return "", fmt.Errorf("auth mode must be %q or %q", AuthModeRequired, AuthModeDisabled)
	}
}

func LoadBearerTokenRegistry(path string) (*BearerTokenRegistry, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("bearer token file is required")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open bearer token file: %w", err)
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, MaxBearerTokenRegistryBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read bearer token file: %w", err)
	}
	if len(data) > MaxBearerTokenRegistryBytes {
		return nil, fmt.Errorf("bearer token file exceeds %d bytes", MaxBearerTokenRegistryBytes)
	}
	return parseBearerTokenRegistry(data)
}

func parseBearerTokenRegistry(data []byte) (*BearerTokenRegistry, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var document bearerTokenFile
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("decode bearer token file: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, fmt.Errorf("bearer token file must contain exactly one YAML document")
		}
		return nil, fmt.Errorf("decode trailing bearer token data: %w", err)
	}
	if document.Version != 1 {
		return nil, fmt.Errorf("unsupported bearer token file version %d", document.Version)
	}
	if len(document.Tokens) == 0 {
		return nil, fmt.Errorf("bearer token file contains no tokens")
	}
	if len(document.Tokens) > MaxBearerTokens {
		return nil, fmt.Errorf("bearer token file contains %d tokens; maximum is %d", len(document.Tokens), MaxBearerTokens)
	}

	ids := make(map[string]struct{}, len(document.Tokens))
	hashes := make(map[string]struct{}, len(document.Tokens))
	active := make([][sha256.Size]byte, 0, len(document.Tokens))
	for i, record := range document.Tokens {
		if !bearerTokenIDPattern.MatchString(record.ID) {
			return nil, fmt.Errorf("token %d has invalid id", i+1)
		}
		if _, exists := ids[record.ID]; exists {
			return nil, fmt.Errorf("token %d has a duplicate id", i+1)
		}
		ids[record.ID] = struct{}{}
		if !bearerTokenDigestPattern.MatchString(record.SHA256) {
			return nil, fmt.Errorf("token %d sha256 must be 64 lowercase hexadecimal characters", i+1)
		}
		if _, exists := hashes[record.SHA256]; exists {
			return nil, fmt.Errorf("token %d has a duplicate sha256", i+1)
		}
		hashes[record.SHA256] = struct{}{}
		if record.Enabled == nil {
			return nil, fmt.Errorf("token %d must explicitly set enabled", i+1)
		}
		if !*record.Enabled {
			continue
		}
		decoded, err := hex.DecodeString(record.SHA256)
		if err != nil {
			return nil, fmt.Errorf("decode token %d sha256: %w", i+1, err)
		}
		var digest [sha256.Size]byte
		copy(digest[:], decoded)
		active = append(active, digest)
	}
	if len(active) == 0 {
		return nil, fmt.Errorf("bearer token file contains no enabled tokens")
	}
	fileSum := sha256.Sum256(data)
	return &BearerTokenRegistry{
		activeDigests: active,
		fileDigest:    hex.EncodeToString(fileSum[:]),
	}, nil
}

func (r *BearerTokenRegistry) ActiveTokenCount() int {
	if r == nil {
		return 0
	}
	return len(r.activeDigests)
}

func (r *BearerTokenRegistry) Digest() string {
	if r == nil {
		return ""
	}
	return r.fileDigest
}

func (r *BearerTokenRegistry) accepts(token string) bool {
	if r == nil || token == "" || len(token) > maxBearerTokenLength {
		return false
	}
	sum := sha256.Sum256([]byte(token))
	matched := 0
	for _, expected := range r.activeDigests {
		matched |= subtle.ConstantTimeCompare(sum[:], expected[:])
	}
	return matched == 1
}

func WithBearerAuth(mode AuthMode, registry *BearerTokenRegistry, next http.Handler) http.Handler {
	if mode == AuthModeDisabled {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mode != AuthModeRequired || registry == nil {
			http.Error(w, "bearer authentication is unavailable", http.StatusServiceUnavailable)
			return
		}
		got, ok := bearerToken(r.Header.Get("Authorization"))
		if !ok || !registry.accepts(got) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func bearerToken(header string) (string, bool) {
	fields := strings.Fields(header)
	if len(fields) != 2 || !strings.EqualFold(fields[0], "Bearer") || fields[1] == "" {
		return "", false
	}
	return fields[1], true
}
