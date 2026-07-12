package corpus

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/chromato99/krx-rule-mcp/internal/model"
)

const IndexSourceSchemaVersion = 2

const (
	maxSourceRequestBytes  = 64 << 10
	maxSourceContentBytes  = 64 << 20
	maxDocumentTextBytes   = 64 << 20
	maxAttachmentTextBytes = 64 << 20
)

var sourceRequestSecretKeys = map[string]struct{}{
	"cookie":        {},
	"cookies":       {},
	"csrf":          {},
	"_csrf":         {},
	"x-csrf-token":  {},
	"authorization": {},
}

var canonicalQualityCodes = map[string]struct{}{
	"body_hash_mismatch":              {},
	"raw_file_hash_mismatch":          {},
	"converted_text_hash_mismatch":    {},
	"manifest_metadata_mismatch":      {},
	"required_source_missing":         {},
	"required_conversion_failed":      {},
	"path_outside_data_root":          {},
	"duplicate_document_id":           {},
	"duplicate_attachment_id":         {},
	"duplicate_asset_id":              {},
	"invalid_status_combination":      {},
	"formula_source_count_mismatch":   {},
	"document_empty_body":             {},
	"pdf_text_layer_too_sparse":       {},
	"pdf_comparison_structure_lost":   {},
	"image_content_unindexed":         {},
	"inline_image_missing":            {},
	"hwp_picture_missing":             {},
	"html_text_boundary_collapsed":    {},
	"formula_generated_latex_invalid": {},
	"source_inspection_failed":        {},
	"stale_due_to_refresh_failure":    {},
	// Transitional v1 codes accepted by the producer during coordinated v2 migration.
	"empty_text":                             {},
	"very_short_text":                        {},
	"replacement_characters":                 {},
	"very_long_lines":                        {},
	"raw_table_hints_without_table_text":     {},
	"raw_table_cells_may_be_flattened":       {},
	"raw_formula_hints_without_formula_text": {},
	"conversion_failed":                      {},
	"conversion_pending":                     {},
	"missing_text_path":                      {},
	"missing_converted_file":                 {},
}

// LoadedCorpus is a validated corpus handoff. AttachmentTexts contains
// canonical converted Markdown for converted attachments, including sources
// explicitly marked searchable=false so MCP can still return their metadata.
type LoadedCorpus struct {
	Documents       []model.Document
	AttachmentTexts map[string]string
	ReleaseHash     string
}

type LoadOptions struct {
	RequireManifest bool
}

// Load validates the producer/consumer corpus contract and reads converted
// attachment text. It rejects invalid IDs, status/path combinations, hash
// mismatches, missing converted text, symlinks, and cross-bundle references.
func Load(root string) (LoadedCorpus, error) {
	return LoadWithOptions(root, LoadOptions{})
}

func LoadWithOptions(root string, options LoadOptions) (LoadedCorpus, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return LoadedCorpus{}, fmt.Errorf("resolve data root: %w", err)
	}
	rootInfo, err := os.Stat(rootAbs)
	if err != nil {
		return LoadedCorpus{}, fmt.Errorf("stat data root: %w", err)
	}
	if !rootInfo.IsDir() {
		return LoadedCorpus{}, fmt.Errorf("data root %q is not a directory", root)
	}

	docs, err := loadDocumentMetadata(rootAbs)
	if err != nil {
		return LoadedCorpus{}, err
	}
	if len(docs) == 0 {
		return LoadedCorpus{}, fmt.Errorf("corpus contains no documents")
	}

	loaded := LoadedCorpus{
		Documents:       docs,
		AttachmentTexts: make(map[string]string),
	}
	allIDs := make(map[string]string, len(docs))
	for i := range loaded.Documents {
		doc := &loaded.Documents[i]
		if previous, ok := allIDs[doc.ID]; ok {
			return LoadedCorpus{}, fmt.Errorf("duplicate document id %q in %s and %s", doc.ID, previous, doc.Path)
		}
		allIDs[doc.ID] = "document " + doc.Path
	}
	for i := range loaded.Documents {
		doc := &loaded.Documents[i]
		if err := validateDocument(rootAbs, doc, loaded.AttachmentTexts, allIDs); err != nil {
			return LoadedCorpus{}, err
		}
	}
	manifest, err := validateReleaseManifest(rootAbs, loaded.Documents, loaded.AttachmentTexts, options.RequireManifest)
	if err != nil {
		return LoadedCorpus{}, err
	}
	loaded.ReleaseHash = manifest.ReleaseHash
	return loaded, nil
}

func validateDocument(root string, doc *model.Document, texts map[string]string, allIDs map[string]string) error {
	doc.Path = filepath.Clean(doc.Path)
	if _, err := checkedExistingPath(root, root, doc.Path); err != nil {
		return fmt.Errorf("document %q path: %w", doc.ID, err)
	}
	bundle := filepath.Dir(doc.Path)
	publicSourceURL, ok := model.SafeAbsoluteHTTPURL(doc.SourceURL)
	if !ok {
		return fmt.Errorf("document %q: source_url must be an absolute HTTP(S) URL without credentials or control characters", doc.ID)
	}
	doc.SourceURL = publicSourceURL
	if fileName, ok := model.PortableFileName(doc.FileName); !ok {
		return fmt.Errorf("document %q: file_name must be a portable basename", doc.ID)
	} else {
		doc.FileName = fileName
	}
	if doc.CollectedAt.IsZero() {
		return fmt.Errorf("document %q: collected_at is required", doc.ID)
	}
	if doc.SchemaVersion != 0 && doc.SchemaVersion != 1 && doc.SchemaVersion != IndexSourceSchemaVersion {
		return fmt.Errorf("document %q: unsupported schema_version %d", doc.ID, doc.SchemaVersion)
	}
	if err := validateDate("effective_date", doc.EffectiveDate); err != nil {
		return fmt.Errorf("document %q: %w", doc.ID, err)
	}
	if err := validateDate("published_date", doc.PublishedDate); err != nil {
		return fmt.Errorf("document %q: %w", doc.ID, err)
	}
	bodyEmpty := strings.TrimSpace(doc.Body) == ""
	actualBodyHash := model.HashText(doc.Body)
	if doc.SchemaVersion >= IndexSourceSchemaVersion && strings.TrimSpace(doc.BodyHash) == "" {
		return fmt.Errorf("document %q: body_hash is required for schema v%d", doc.ID, IndexSourceSchemaVersion)
	}
	if strings.TrimSpace(doc.BodyHash) != "" {
		wantBodyHash, err := requireSHA256("body_hash", doc.BodyHash)
		if err != nil {
			return fmt.Errorf("document %q: %w", doc.ID, err)
		}
		if actualBodyHash != wantBodyHash {
			return fmt.Errorf("document %q: body_hash_mismatch: got %s want %s", doc.ID, actualBodyHash, wantBodyHash)
		}
	}
	if strings.TrimSpace(doc.ContentHash) != "" {
		wantLegacyHash, err := requireSHA256("content_hash", doc.ContentHash)
		if err != nil {
			return fmt.Errorf("document %q: %w", doc.ID, err)
		}
		gotLegacyHash := model.HashText(doc.Title + "\n" + doc.Body)
		if gotLegacyHash != wantLegacyHash {
			return fmt.Errorf("document %q: content_hash_mismatch: got %s want %s", doc.ID, gotLegacyHash, wantLegacyHash)
		}
	} else if strings.TrimSpace(doc.BodyHash) == "" {
		return fmt.Errorf("document %q: body_hash or content_hash is required", doc.ID)
	}
	conversionStatus := doc.EffectiveConversionStatus()
	if err := validateEntityStatuses(conversionStatus, doc.PreservationStatus, doc.IsSearchable(), doc.QualityStatus); err != nil {
		return fmt.Errorf("document %q: %w", doc.ID, err)
	}
	if doc.PreservationStatus == "missing" || doc.PreservationStatus == "failed" {
		return fmt.Errorf("document %q: required_source_missing: preservation_status=%s", doc.ID, doc.PreservationStatus)
	}
	doc.QualityCodes = doc.EffectiveQualityCodes()
	if err := validateQualityCodes(doc.QualityCodes); err != nil {
		return fmt.Errorf("document %q: %w", doc.ID, err)
	}
	if bodyEmpty {
		switch {
		case doc.Searchable == nil || *doc.Searchable:
			return fmt.Errorf("document %q: empty body requires explicit searchable=false", doc.ID)
		case doc.QualityStatus != "warn":
			return fmt.Errorf("document %q: empty body requires quality_status=warn", doc.ID)
		case !containsString(doc.QualityCodes, "document_empty_body"):
			return fmt.Errorf("document %q: empty body requires document_empty_body quality code", doc.ID)
		case conversionStatus == string(model.AttachmentFailed):
			return fmt.Errorf("document %q: required_conversion_failed: empty body conversion failed", doc.ID)
		}
	}
	if err := loadSourceProvenance(root, bundle, doc); err != nil {
		return fmt.Errorf("document %q source provenance: %w", doc.ID, err)
	}

	if doc.RawPath != "" {
		rawPath, err := checkedBundleFile(root, bundle, doc.RawPath, "raw")
		if err != nil {
			return fmt.Errorf("document %q raw_path: %w", doc.ID, err)
		}
		if declared := doc.EffectiveRawFileHash(); declared != "" {
			if err := verifyFileHash(rawPath, declared); err != nil {
				return fmt.Errorf("document %q: %w", doc.ID, err)
			}
		}
	}
	if doc.TextPath != "" {
		textPath, err := checkedBundleFile(root, bundle, doc.TextPath, "attachments")
		if err != nil {
			return fmt.Errorf("document %q text_path: %w", doc.ID, err)
		}
		data, err := readFileBounded(textPath, maxDocumentTextBytes)
		if err != nil {
			return fmt.Errorf("document %q text_path: %w", doc.ID, err)
		}
		if !utf8.Valid(data) {
			return fmt.Errorf("document %q text_path is not valid UTF-8", doc.ID)
		}
		if model.HashText(string(data)) != actualBodyHash {
			return fmt.Errorf("document %q: document text_path does not match body_hash", doc.ID)
		}
	}
	documentContentPath, err := filepath.Rel(root, doc.Path)
	if err != nil {
		return fmt.Errorf("document %q: derive content path: %w", doc.ID, err)
	}
	for i := range doc.Assets {
		if err := validateAsset(root, bundle, filepath.ToSlash(documentContentPath), doc.SourceURL, "document "+doc.ID, &doc.Assets[i], allIDs); err != nil {
			return err
		}
	}

	for j := range doc.Attachments {
		att := &doc.Attachments[j]
		if strings.TrimSpace(att.ID) == "" {
			return fmt.Errorf("document %q: attachment id is required", doc.ID)
		}
		if previous, ok := allIDs[att.ID]; ok {
			return fmt.Errorf("duplicate attachment id %q in %s and %s", att.ID, previous, doc.Path)
		}
		allIDs[att.ID] = "attachment " + doc.Path
		if err := validateAttachment(root, bundle, doc.ID, doc.SourceURL, doc.SchemaVersion, att, texts, allIDs); err != nil {
			return err
		}
	}
	if bodyEmpty {
		hasSearchableAttachmentFallback := false
		for _, attachment := range doc.Attachments {
			text := strings.TrimSpace(texts[attachment.ID])
			if attachment.EffectiveConversionStatus() == model.AttachmentConverted && attachment.IsSearchable() && text != "" {
				hasSearchableAttachmentFallback = true
				break
			}
		}
		if !hasSearchableAttachmentFallback {
			return fmt.Errorf("document %q: document_empty_body requires at least one validated converted searchable attachment", doc.ID)
		}
	}
	return nil
}

func loadSourceProvenance(root, bundle string, doc *model.Document) error {
	contentHash := strings.TrimSpace(doc.SourceContentHash)
	contentPath := strings.TrimSpace(doc.SourceContentPath)
	requestPath := strings.TrimSpace(doc.SourceRequestPath)
	if contentHash == "" && contentPath == "" && requestPath == "" {
		return nil
	}
	if contentHash == "" || contentPath == "" || requestPath == "" {
		return fmt.Errorf("source_content_hash, source_content_path, and source_request_path must be provided together")
	}
	declaredHash, err := requireSHA256("source_content_hash", contentHash)
	if err != nil {
		return err
	}
	checkedContentPath, err := checkedBundleFile(root, bundle, contentPath, "raw")
	if err != nil {
		return fmt.Errorf("source_content_path: %w", err)
	}
	content, err := readFileBounded(checkedContentPath, maxSourceContentBytes)
	if err != nil {
		return fmt.Errorf("read source_content_path: %w", err)
	}
	if !utf8.Valid(content) {
		return fmt.Errorf("source_content_path is not valid UTF-8")
	}
	actualHash := model.HashText(string(content))
	if actualHash != declaredHash {
		return fmt.Errorf("source_content_hash_mismatch: got %s want %s", actualHash, declaredHash)
	}

	checkedRequestPath, err := checkedBundleFile(root, bundle, requestPath, "raw")
	if err != nil {
		return fmt.Errorf("source_request_path: %w", err)
	}
	requestJSON, err := readFileBounded(checkedRequestPath, maxSourceRequestBytes)
	if err != nil {
		return fmt.Errorf("read source_request_path: %w", err)
	}
	requestDescriptor, err := decodeSourceRequestDescriptor(requestJSON)
	if err != nil {
		return fmt.Errorf("invalid source request descriptor: %w", err)
	}
	if requestDescriptor.SourceContentHash != declaredHash {
		return fmt.Errorf("request source_content_hash %q does not match document source_content_hash %q", requestDescriptor.SourceContentHash, declaredHash)
	}
	if err := validateSourceRequestForDocument(requestDescriptor, doc.DocumentType); err != nil {
		return err
	}
	doc.SourceContentHash = declaredHash
	doc.SourceContentPath = contentPath
	doc.SourceRequestPath = requestPath
	doc.SourceRequest = requestDescriptor
	return nil
}

func readFileBounded(path string, maxBytes int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("file is not regular")
	}
	if info.Size() > maxBytes {
		return nil, fmt.Errorf("file exceeds %d-byte limit", maxBytes)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("file exceeds %d-byte limit", maxBytes)
	}
	return data, nil
}

func decodeSourceRequestDescriptor(data []byte) (model.SourceRequestDescriptor, error) {
	if !utf8.Valid(data) {
		return model.SourceRequestDescriptor{}, fmt.Errorf("request JSON is not valid UTF-8")
	}
	if err := validateStrictSourceRequestJSON(data); err != nil {
		return model.SourceRequestDescriptor{}, err
	}
	var raw map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&raw); err != nil {
		return model.SourceRequestDescriptor{}, err
	}
	if raw == nil {
		return model.SourceRequestDescriptor{}, fmt.Errorf("request descriptor must be a JSON object")
	}
	allowed := map[string]struct{}{
		"endpoint":            {},
		"bookid":              {},
		"noformyn":            {},
		"statehistoryid":      {},
		"BBSID":               {},
		"Menuid":              {},
		"source_content_hash": {},
	}
	values := make(map[string]string, len(raw))
	for key, encoded := range raw {
		if _, ok := allowed[key]; !ok {
			return model.SourceRequestDescriptor{}, fmt.Errorf("unknown field %q", key)
		}
		var value string
		if err := json.Unmarshal(encoded, &value); err != nil {
			return model.SourceRequestDescriptor{}, fmt.Errorf("field %q must be a string", key)
		}
		value = strings.TrimSpace(value)
		if len(value) > 4096 || strings.ContainsAny(value, "\x00\r\n") {
			return model.SourceRequestDescriptor{}, fmt.Errorf("field %q contains an invalid value", key)
		}
		values[key] = value
	}
	descriptor := model.SourceRequestDescriptor{
		Endpoint:          values["endpoint"],
		BookID:            values["bookid"],
		NoFormYN:          values["noformyn"],
		StateHistoryID:    values["statehistoryid"],
		BBSID:             values["BBSID"],
		MenuID:            values["Menuid"],
		SourceContentHash: values["source_content_hash"],
	}
	if descriptor.SourceContentHash == "" {
		return model.SourceRequestDescriptor{}, fmt.Errorf("source_content_hash is required")
	}
	validatedHash, err := requireSHA256("request source_content_hash", descriptor.SourceContentHash)
	if err != nil {
		return model.SourceRequestDescriptor{}, err
	}
	descriptor.SourceContentHash = validatedHash
	return descriptor, nil
}

func validateSourceRequestForDocument(descriptor model.SourceRequestDescriptor, documentType model.DocumentType) error {
	switch descriptor.Endpoint {
	case "/out/regulation/regulationViewPop.do":
		if documentType != model.DocumentTypeRule || descriptor.BookID == "" || descriptor.NoFormYN == "" || descriptor.BBSID != "" || descriptor.MenuID != "" {
			return fmt.Errorf("rule source request requires bookid, noformyn, and rule document type")
		}
	case "/out/pds/pdsViewPop.do":
		if documentType != model.DocumentTypeNotice || descriptor.BBSID == "" || descriptor.MenuID == "" || descriptor.BookID != "" || descriptor.NoFormYN != "" || descriptor.StateHistoryID != "" {
			return fmt.Errorf("notice source request requires BBSID, Menuid, and notice document type")
		}
	default:
		return fmt.Errorf("unsupported source request endpoint %q", descriptor.Endpoint)
	}
	return nil
}

func validateStrictSourceRequestJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := validateStrictJSONValue(decoder, 0); err != nil {
		return err
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return err
	}
	return nil
}

func validateStrictJSONValue(decoder *json.Decoder, depth int) error {
	if depth > 64 {
		return fmt.Errorf("JSON nesting exceeds 64 levels")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]struct{}{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("JSON object key must be a string")
			}
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("duplicate field %q", key)
			}
			seen[key] = struct{}{}
			if _, secret := sourceRequestSecretKeys[strings.ToLower(strings.TrimSpace(key))]; secret {
				return fmt.Errorf("secret field %q is forbidden", key)
			}
			if err := validateStrictJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim('}') {
			return fmt.Errorf("malformed JSON object")
		}
	case '[':
		for decoder.More() {
			if err := validateStrictJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim(']') {
			return fmt.Errorf("malformed JSON array")
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
	return nil
}

func validateAttachment(root, bundle, documentID, documentSourceURL string, schemaVersion int, att *model.Attachment, texts map[string]string, allIDs map[string]string) error {
	if sourceURL, ok := model.SafeAttachmentSourceURL(att.SourceURL); !ok {
		return fmt.Errorf("document %q attachment %q: source_url must be a safe HTTP(S) URL or supported KRX endpoint", documentID, att.ID)
	} else {
		att.SourceURL = sourceURL
	}
	if fileName, ok := model.PortableFileName(att.FileName); !ok {
		return fmt.Errorf("document %q attachment %q: file_name must be a portable basename", documentID, att.ID)
	} else {
		att.FileName = fileName
	}
	if strings.TrimSpace(att.ConversionStatus) != "" && att.Status != "" && string(att.Status) != strings.TrimSpace(att.ConversionStatus) {
		return fmt.Errorf("document %q attachment %q: status and conversion_status differ", documentID, att.ID)
	}
	att.Status = att.EffectiveConversionStatus()
	switch att.Status {
	case model.AttachmentPending, model.AttachmentConverted, model.AttachmentFailed:
	default:
		return fmt.Errorf("document %q attachment %q: invalid status %q", documentID, att.ID, att.Status)
	}
	if err := validateEntityStatuses(string(att.Status), att.PreservationStatus, att.IsSearchable(), att.QualityStatus); err != nil {
		return fmt.Errorf("document %q attachment %q: %w", documentID, att.ID, err)
	}
	att.QualityCodes = att.EffectiveQualityCodes()
	if err := validateQualityCodes(att.QualityCodes); err != nil {
		return fmt.Errorf("document %q attachment %q: %w", documentID, att.ID, err)
	}

	if att.RawPath != "" {
		rawPath, err := checkedBundleFile(root, bundle, att.RawPath, "raw")
		if err != nil {
			return fmt.Errorf("document %q attachment %q raw_path: %w", documentID, att.ID, err)
		}
		declared := att.EffectiveRawFileHash()
		if declared == "" {
			return fmt.Errorf("document %q attachment %q: raw_file_hash is required when raw_path is present", documentID, att.ID)
		}
		if err := verifyFileHash(rawPath, declared); err != nil {
			return fmt.Errorf("document %q attachment %q: %w", documentID, att.ID, err)
		}
	}
	for i := range att.Assets {
		if err := validateAsset(root, bundle, filepath.ToSlash(att.TextPath), documentSourceURL, "attachment "+att.ID+" in document "+documentID, &att.Assets[i], allIDs); err != nil {
			return err
		}
	}

	if att.Status != model.AttachmentConverted {
		if strings.TrimSpace(att.TextPath) != "" {
			return fmt.Errorf("document %q attachment %q: text_path requires converted status", documentID, att.ID)
		}
		if att.Searchable != nil && *att.Searchable {
			return fmt.Errorf("document %q attachment %q: searchable=true requires converted status", documentID, att.ID)
		}
		return nil
	}
	if strings.TrimSpace(att.TextPath) == "" {
		return fmt.Errorf("document %q attachment %q: converted attachment requires text_path", documentID, att.ID)
	}
	textPath, err := checkedBundleFile(root, bundle, att.TextPath, "attachments")
	if err != nil {
		return fmt.Errorf("document %q attachment %q text_path: %w", documentID, att.ID, err)
	}
	data, err := readFileBounded(textPath, maxAttachmentTextBytes)
	if err != nil {
		return fmt.Errorf("document %q attachment %q: read converted text: %w", documentID, att.ID, err)
	}
	if !utf8.Valid(data) {
		return fmt.Errorf("document %q attachment %q: converted text is not valid UTF-8", documentID, att.ID)
	}
	text := model.CanonicalText(string(data))
	if text == "" {
		return fmt.Errorf("document %q attachment %q: converted text is empty", documentID, att.ID)
	}
	actualHash := model.HashText(text)
	if schemaVersion >= IndexSourceSchemaVersion && strings.TrimSpace(att.ConvertedTextHash) == "" {
		return fmt.Errorf("document %q attachment %q: converted_text_hash is required for schema v%d", documentID, att.ID, IndexSourceSchemaVersion)
	}
	if declared := strings.TrimSpace(att.ConvertedTextHash); declared != "" {
		want, err := requireSHA256("converted_text_hash", declared)
		if err != nil {
			return fmt.Errorf("document %q attachment %q: %w", documentID, att.ID, err)
		}
		if actualHash != want {
			return fmt.Errorf("document %q attachment %q: converted_text_hash_mismatch: got %s want %s", documentID, att.ID, actualHash, want)
		}
	}
	texts[att.ID] = text
	return nil
}

func checkedBundleFile(root, bundle, metadataPath, subdirectory string) (string, error) {
	if filepath.IsAbs(metadataPath) {
		return "", fmt.Errorf("absolute path is not allowed")
	}
	for _, part := range strings.Split(filepath.ToSlash(metadataPath), "/") {
		if part == ".." {
			return "", fmt.Errorf("parent traversal is not allowed")
		}
	}
	clean := filepath.Clean(metadataPath)
	if clean == "." || clean == "" {
		return "", fmt.Errorf("empty path")
	}
	target := filepath.Join(root, clean)
	expected := filepath.Join(bundle, subdirectory)
	return checkedExistingPath(root, expected, target)
}

func checkedExistingPath(root, expectedParent, target string) (string, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	expectedAbs, err := filepath.Abs(expectedParent)
	if err != nil {
		return "", err
	}
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	if !pathWithin(rootAbs, targetAbs) {
		return "", fmt.Errorf("path escapes data root")
	}
	if !pathWithin(expectedAbs, targetAbs) {
		return "", fmt.Errorf("path escapes document bundle")
	}
	rel, err := filepath.Rel(rootAbs, targetAbs)
	if err != nil {
		return "", err
	}
	current := rootAbs
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("symlink is not allowed: %s", current)
		}
	}
	info, err := os.Stat(targetAbs)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("path is not a regular file")
	}
	return targetAbs, nil
}

func pathWithin(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func verifyFileHash(path, declared string) error {
	want, err := requireSHA256("raw_file_hash/content_hash", declared)
	if err != nil {
		return err
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("read raw file: %w", err)
	}
	defer file.Close()
	h := sha256.New()
	if _, err := io.Copy(h, file); err != nil {
		return fmt.Errorf("hash raw file: %w", err)
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != want {
		return fmt.Errorf("raw_file_hash_mismatch: got %s want %s", got, want)
	}
	return nil
}

func requireSHA256(name, value string) (string, error) {
	value = strings.TrimSpace(value)
	if len(value) != sha256.Size*2 {
		return "", fmt.Errorf("%s must be a 64-character SHA-256 hex digest", name)
	}
	if _, err := hex.DecodeString(value); err != nil {
		return "", fmt.Errorf("%s must be a SHA-256 hex digest", name)
	}
	if value != strings.ToLower(value) {
		return "", fmt.Errorf("%s must be a lowercase SHA-256 hex digest", name)
	}
	return value, nil
}

func validateEntityStatuses(conversionStatus, preservationStatus string, searchable bool, qualityStatus string) error {
	if conversionStatus != "" && conversionStatus != string(model.AttachmentPending) && conversionStatus != string(model.AttachmentConverted) && conversionStatus != string(model.AttachmentFailed) {
		return fmt.Errorf("invalid conversion_status %q", conversionStatus)
	}
	if preservationStatus != "" && preservationStatus != "preserved" && preservationStatus != "missing" && preservationStatus != "failed" {
		return fmt.Errorf("invalid preservation_status %q", preservationStatus)
	}
	if qualityStatus != "" && qualityStatus != "ok" && qualityStatus != "warn" && qualityStatus != "fail" {
		return fmt.Errorf("invalid quality_status %q", qualityStatus)
	}
	if searchable && conversionStatus == string(model.AttachmentFailed) {
		return fmt.Errorf("invalid_status_combination: failed conversion must not be searchable")
	}
	if searchable && qualityStatus == "fail" {
		return fmt.Errorf("invalid_status_combination: failed quality must not be searchable")
	}
	return nil
}

func validateQualityCodes(codes []string) error {
	for _, code := range codes {
		if _, ok := canonicalQualityCodes[code]; !ok {
			return fmt.Errorf("unknown quality code %q", code)
		}
	}
	return nil
}

func validateDate(name, value string) error {
	if value == "" {
		return nil
	}
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil || parsed.Format("2006-01-02") != value {
		return fmt.Errorf("%s must use YYYY-MM-DD", name)
	}
	return nil
}

// CanonicalIndexSource returns deterministic UTF-8 JSON shared by producer and
// consumer fixtures. It intentionally excludes Go index implementation
// versions; those belong to index_build_hash.
func CanonicalIndexSource(docs []model.Document, attachmentTexts map[string]string) ([]byte, error) {
	documents := make([]map[string]any, 0, len(docs))
	for _, doc := range docs {
		item, err := canonicalDocument(doc, attachmentTexts)
		if err != nil {
			return nil, err
		}
		documents = append(documents, item)
	}
	sort.Slice(documents, func(i, j int) bool { return documents[i]["id"].(string) < documents[j]["id"].(string) })
	projection := map[string]any{
		"documents":      documents,
		"schema_version": IndexSourceSchemaVersion,
	}
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(projection); err != nil {
		return nil, err
	}
	return bytes.TrimSpace(out.Bytes()), nil
}

func canonicalDocument(doc model.Document, attachmentTexts map[string]string) (map[string]any, error) {
	attachments := make([]map[string]any, 0, len(doc.Attachments))
	for _, att := range doc.Attachments {
		text, ok := attachmentTexts[att.ID]
		convertedHash := model.CanonicalText(att.ConvertedTextHash)
		if convertedHash == "" && ok {
			convertedHash = model.HashText(text)
		}
		if att.EffectiveConversionStatus() == model.AttachmentConverted && convertedHash == "" {
			return nil, fmt.Errorf("attachment %q has no converted text", att.ID)
		}
		attachments = append(attachments, map[string]any{
			"conversion_status":   string(att.EffectiveConversionStatus()),
			"converted_text_hash": convertedHash,
			"file_name":           model.CanonicalText(att.FileName),
			"id":                  model.CanonicalText(att.ID),
			"quality_codes":       att.EffectiveQualityCodes(),
			"quality_status":      model.CanonicalText(att.QualityStatus),
			"searchable":          att.IsSearchable(),
			"title":               model.CanonicalText(att.Title),
		})
	}
	sort.Slice(attachments, func(i, j int) bool { return attachments[i]["id"].(string) < attachments[j]["id"].(string) })
	return map[string]any{
		"attachments":    attachments,
		"body_hash":      model.HashText(doc.Body),
		"category":       model.CanonicalText(doc.Category),
		"collected_at":   doc.CollectedAt.UTC().Format(time.RFC3339Nano),
		"document_type":  model.CanonicalText(string(doc.DocumentType)),
		"effective_date": model.CanonicalText(doc.EffectiveDate),
		"file_name":      model.CanonicalText(doc.FileName),
		"id":             model.CanonicalText(doc.ID),
		"language":       model.CanonicalText(doc.Language),
		"published_date": model.CanonicalText(doc.PublishedDate),
		"quality_codes":  doc.EffectiveQualityCodes(),
		"quality_status": model.CanonicalText(doc.QualityStatus),
		"searchable":     doc.IsSearchable(),
		"source_id":      model.CanonicalText(doc.SourceID),
		"source_url":     model.CanonicalText(doc.SourceURL),
		"title":          model.CanonicalText(doc.Title),
	}, nil
}

func IndexSourceHash(docs []model.Document, attachmentTexts map[string]string) (string, error) {
	data, err := CanonicalIndexSource(docs, attachmentTexts)
	if err != nil {
		return "", err
	}
	return model.HashBytes(data), nil
}

func DocumentIndexSourceHash(doc model.Document, attachmentTexts map[string]string) (string, error) {
	projection, err := canonicalDocument(doc, attachmentTexts)
	if err != nil {
		return "", err
	}
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(projection); err != nil {
		return "", err
	}
	return model.HashBytes(bytes.TrimSpace(out.Bytes())), nil
}
