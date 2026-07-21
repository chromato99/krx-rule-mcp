package corpus

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/chromato99/krx-rule-mcp/internal/model"
	"golang.org/x/text/unicode/norm"
	"gopkg.in/yaml.v3"
)

const (
	releaseManifestFile     = "manifest.json"
	maxReleaseManifestBytes = 64 << 20
)

type ReleaseManifest struct {
	IndexSourceHash string
	ReleaseHash     string
}

var releaseOperationalFields = map[string]struct{}{
	"release_hash":           {},
	"generated_at":           {},
	"last_checked_at":        {},
	"source_response_hash":   {},
	"last_refresh_error":     {},
	"last_refresh_failed_at": {},
}

// legacyReleaseOperationalFields freezes the schema-v2 hash contract from
// before refresh-failure provenance became operational metadata.
var legacyReleaseOperationalFields = map[string]struct{}{
	"release_hash":         {},
	"generated_at":         {},
	"last_checked_at":      {},
	"source_response_hash": {},
}

var refreshFailureOperationalFields = map[string]struct{}{
	"last_refresh_error":     {},
	"last_refresh_failed_at": {},
}

func validateReleaseManifest(root string, docs []model.Document, attachmentTexts map[string]string, required bool) (ReleaseManifest, error) {
	path := filepath.Join(root, releaseManifestFile)
	if _, err := os.Lstat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) && !required {
			return ReleaseManifest{}, nil
		}
		if errors.Is(err, os.ErrNotExist) {
			return ReleaseManifest{}, fmt.Errorf("release manifest %q is required", path)
		}
		return ReleaseManifest{}, fmt.Errorf("stat release manifest: %w", err)
	}
	checked, err := checkedExistingPath(root, root, path)
	if err != nil {
		return ReleaseManifest{}, fmt.Errorf("release manifest path: %w", err)
	}
	data, err := readFileBounded(checked, maxReleaseManifestBytes)
	if err != nil {
		return ReleaseManifest{}, fmt.Errorf("read release manifest: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	var payload map[string]any
	if err := decoder.Decode(&payload); err != nil {
		return ReleaseManifest{}, fmt.Errorf("parse release manifest: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return ReleaseManifest{}, fmt.Errorf("parse release manifest: %w", err)
	}
	if schema, ok := payload["schema_version"].(float64); !ok || int(schema) != IndexSourceSchemaVersion || schema != float64(int(schema)) {
		return ReleaseManifest{}, fmt.Errorf("release manifest has unsupported schema_version")
	}
	if required {
		if err := validateStrictReleaseProfile(payload, docs); err != nil {
			return ReleaseManifest{}, err
		}
		if err := validateManifestDocumentParity(root, payload, docs); err != nil {
			return ReleaseManifest{}, err
		}
	}
	declaredIndexHash, ok := payload["index_source_hash"].(string)
	if !ok || strings.TrimSpace(declaredIndexHash) == "" {
		return ReleaseManifest{}, fmt.Errorf("release manifest index_source_hash is required")
	}
	declaredIndexHash, err = requireSHA256("manifest index_source_hash", declaredIndexHash)
	if err != nil {
		return ReleaseManifest{}, err
	}
	actualIndexHash, err := IndexSourceHash(docs, attachmentTexts)
	if err != nil {
		return ReleaseManifest{}, err
	}
	if declaredIndexHash != actualIndexHash {
		return ReleaseManifest{}, fmt.Errorf("release manifest index_source_hash_mismatch: got %s want %s", declaredIndexHash, actualIndexHash)
	}
	declaredReleaseHash, ok := payload["release_hash"].(string)
	if !ok || strings.TrimSpace(declaredReleaseHash) == "" {
		return ReleaseManifest{}, fmt.Errorf("release manifest release_hash is required")
	}
	declaredReleaseHash, err = requireSHA256("manifest release_hash", declaredReleaseHash)
	if err != nil {
		return ReleaseManifest{}, err
	}
	actualReleaseHash, err := releaseHash(payload)
	if err != nil {
		return ReleaseManifest{}, err
	}
	if declaredReleaseHash != actualReleaseHash {
		legacyReleaseHash, eligible, err := legacyV2ReleaseHash(payload)
		if err != nil {
			return ReleaseManifest{}, err
		}
		if !eligible || declaredReleaseHash != legacyReleaseHash {
			return ReleaseManifest{}, fmt.Errorf("release manifest release_hash_mismatch: got %s want %s", declaredReleaseHash, actualReleaseHash)
		}
	}
	// Keep the declared legacy digest so indexes built against that release
	// remain compatible after the hash contract migration.
	return ReleaseManifest{IndexSourceHash: actualIndexHash, ReleaseHash: declaredReleaseHash}, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); errors.Is(err, io.EOF) {
		return nil
	} else if err != nil {
		return err
	}
	return fmt.Errorf("trailing JSON value")
}

func releaseHash(payload map[string]any) (string, error) {
	scrubbed := scrubReleaseFields(payload)
	return canonicalJSONHash(scrubbed)
}

func legacyV2ReleaseHash(payload map[string]any) (string, bool, error) {
	schema, ok := payload["schema_version"].(float64)
	if !ok || schema != float64(IndexSourceSchemaVersion) || !containsLegacyRefreshField(payload) {
		return "", false, nil
	}
	scrubbed := scrubReleaseFieldsWith(payload, legacyReleaseOperationalFields)
	hash, err := canonicalJSONHash(scrubbed)
	if err != nil {
		return "", false, err
	}
	return hash, true, nil
}

func canonicalJSONHash(value any) (string, error) {
	normalized := normalizeReleaseJSON(value)
	var canonical bytes.Buffer
	encoder := json.NewEncoder(&canonical)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(normalized); err != nil {
		return "", fmt.Errorf("canonicalize release manifest: %w", err)
	}
	return model.HashBytes(bytes.TrimSpace(canonical.Bytes())), nil
}

func validateStrictReleaseProfile(payload map[string]any, docs []model.Document) error {
	profile, ok := payload["release_profile"].(map[string]any)
	if !ok {
		return fmt.Errorf("release manifest strict release_profile is required")
	}
	version, versionOK := profile["version"].(float64)
	defaultPolicy, defaultOK := profile["default"].(string)
	failures, failuresOK := profile["allowed_failure_ids"].([]any)
	if !versionOK || version != 1 || !defaultOK || defaultPolicy != "strict" || !failuresOK {
		return fmt.Errorf("release manifest has invalid strict release_profile")
	}
	attachments := make(map[string]model.Attachment)
	failed := make(map[string]struct{})
	for _, doc := range docs {
		for _, attachment := range doc.Attachments {
			attachments[attachment.ID] = attachment
			if attachment.EffectiveConversionStatus() == model.AttachmentFailed {
				failed[attachment.ID] = struct{}{}
			}
		}
	}
	allowed := make(map[string]struct{}, len(failures))
	for _, raw := range failures {
		id, ok := raw.(string)
		if !ok || id == "" || id != strings.TrimSpace(id) {
			return fmt.Errorf("release manifest allowed_failure_ids must contain non-empty attachment ID strings")
		}
		if _, duplicate := allowed[id]; duplicate {
			return fmt.Errorf("release manifest allowed_failure_ids contains duplicate %q", id)
		}
		attachment, known := attachments[id]
		if !known {
			return fmt.Errorf("release manifest allowed_failure_ids references unknown attachment %q", id)
		}
		if attachment.EffectiveConversionStatus() != model.AttachmentFailed || attachment.PreservationStatus != "preserved" || attachment.Searchable == nil || *attachment.Searchable {
			return fmt.Errorf("release manifest allowed failure %q must be failed, preserved, and searchable=false", id)
		}
		allowed[id] = struct{}{}
	}
	for id := range failed {
		if _, ok := allowed[id]; !ok {
			return fmt.Errorf("release manifest failed attachment %q is not in allowed_failure_ids", id)
		}
	}
	return nil
}

func validateManifestDocumentParity(root string, payload map[string]any, docs []model.Document) error {
	items, ok := payload["documents"].([]any)
	if !ok {
		return fmt.Errorf("release manifest documents must be a list")
	}
	diskByPath := make(map[string]model.Document, len(docs))
	for _, doc := range docs {
		if doc.SchemaVersion != IndexSourceSchemaVersion {
			return fmt.Errorf("release document %q must use schema_version %d", doc.ID, IndexSourceSchemaVersion)
		}
		rel, err := filepath.Rel(root, doc.Path)
		if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("release document %q has invalid path", doc.ID)
		}
		diskByPath[filepath.ToSlash(rel)] = doc
	}
	manifestByPath := make(map[string]map[string]any, len(items))
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("release manifest document entry must be an object")
		}
		path, ok := item["path"].(string)
		path = filepath.ToSlash(filepath.Clean(strings.TrimSpace(path)))
		if !ok || path == "" || path == "." || filepath.IsAbs(path) || strings.HasPrefix(path, "../") {
			return fmt.Errorf("release manifest document entry has invalid path")
		}
		if _, duplicate := manifestByPath[path]; duplicate {
			return fmt.Errorf("release manifest has duplicate document path %q", path)
		}
		manifestByPath[path] = item
	}
	if len(manifestByPath) != len(diskByPath) {
		return fmt.Errorf("release manifest document count %d does not match disk document count %d", len(manifestByPath), len(diskByPath))
	}
	paths := make([]string, 0, len(diskByPath))
	for path := range diskByPath {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		item, ok := manifestByPath[path]
		if !ok {
			return fmt.Errorf("release manifest is missing document path %q", path)
		}
		schema, ok := item["schema_version"].(float64)
		if !ok || schema != IndexSourceSchemaVersion {
			return fmt.Errorf("release manifest document %q must use schema_version %d", path, IndexSourceSchemaVersion)
		}
		frontmatter, err := readFrontmatterMapping(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			return err
		}
		manifestMapping := make(map[string]any, len(item)-1)
		for key, value := range item {
			if key != "path" {
				manifestMapping[key] = value
			}
		}
		diskHash, err := manifestMetadataHash(frontmatter)
		if err != nil {
			return err
		}
		manifestHash, err := manifestMetadataHash(manifestMapping)
		if err != nil {
			return err
		}
		if diskHash != manifestHash {
			return fmt.Errorf("release manifest manifest_metadata_mismatch for %s", path)
		}
	}
	for path := range manifestByPath {
		if _, ok := diskByPath[path]; !ok {
			return fmt.Errorf("release manifest references missing document path %q", path)
		}
	}
	return nil
}

func readFrontmatterMapping(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if !bytes.HasPrefix(data, []byte("---\n")) {
		return nil, fmt.Errorf("%s: missing YAML frontmatter", path)
	}
	rest := data[len("---\n"):]
	end := bytes.Index(rest, []byte("\n---"))
	if end < 0 {
		return nil, fmt.Errorf("%s: missing YAML frontmatter terminator", path)
	}
	var mapping map[string]any
	if err := yaml.Unmarshal(rest[:end], &mapping); err != nil {
		return nil, err
	}
	return mapping, nil
}

func manifestMetadataHash(value any) (string, error) {
	return canonicalJSONHash(scrubReleaseFieldsWith(value, refreshFailureOperationalFields))
}

func scrubReleaseFields(value any) any {
	return scrubReleaseFieldsWith(value, releaseOperationalFields)
}

func scrubReleaseFieldsWith(value any, excludedFields map[string]struct{}) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			if _, excluded := excludedFields[key]; excluded {
				continue
			}
			out[key] = scrubReleaseFieldsWith(item, excludedFields)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = scrubReleaseFieldsWith(item, excludedFields)
		}
		return out
	default:
		return typed
	}
}

func containsLegacyRefreshField(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, item := range typed {
			if _, operational := refreshFailureOperationalFields[key]; operational {
				return true
			}
			if containsLegacyRefreshField(item) {
				return true
			}
		}
	case []any:
		for _, item := range typed {
			if containsLegacyRefreshField(item) {
				return true
			}
		}
	}
	return false
}

func normalizeReleaseJSON(value any) any {
	switch typed := value.(type) {
	case string:
		typed = strings.ReplaceAll(typed, "\r\n", "\n")
		typed = strings.ReplaceAll(typed, "\r", "\n")
		return norm.NFC.String(typed)
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[key] = normalizeReleaseJSON(item)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = normalizeReleaseJSON(item)
		}
		return out
	default:
		return typed
	}
}
