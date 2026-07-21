package model

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"sort"
	"strings"
	"time"

	"golang.org/x/text/unicode/norm"
)

type DocumentType string

const (
	DocumentTypeRule       DocumentType = "rule"
	DocumentTypeNotice     DocumentType = "notice"
	DocumentTypeAttachment DocumentType = "attachment"
)

const (
	LanguageKorean  = "ko"
	LanguageEnglish = "en"
)

type AttachmentStatus string

const (
	AttachmentPending   AttachmentStatus = "pending"
	AttachmentConverted AttachmentStatus = "converted"
	AttachmentFailed    AttachmentStatus = "failed"
)

type Asset struct {
	ID                 string   `json:"id" yaml:"id"`
	SourceKind         string   `json:"source_kind" yaml:"source_kind"`
	SourceAnchor       string   `json:"source_anchor" yaml:"source_anchor"`
	SourceURL          string   `json:"source_url,omitempty" yaml:"source_url,omitempty"`
	Path               string   `json:"path,omitempty" yaml:"path,omitempty"`
	MIMEType           string   `json:"mime_type,omitempty" yaml:"mime_type,omitempty"`
	RawFileHash        string   `json:"raw_file_hash,omitempty" yaml:"raw_file_hash,omitempty"`
	Size               int64    `json:"size,omitempty" yaml:"size,omitempty"`
	Width              int64    `json:"width,omitempty" yaml:"width,omitempty"`
	Height             int64    `json:"height,omitempty" yaml:"height,omitempty"`
	PreservationStatus string   `json:"preservation_status" yaml:"preservation_status"`
	Searchable         *bool    `json:"searchable" yaml:"searchable"`
	QualityCodes       []string `json:"quality_codes,omitempty" yaml:"quality_codes,omitempty"`
	Error              string   `json:"error,omitempty" yaml:"error,omitempty"`

	// ReferencePath is the verified owner-content-relative legacy Markdown
	// target. It is populated by the corpus loader and never serialized.
	ReferencePath string `json:"-" yaml:"-"`
}

type Attachment struct {
	ID                     string           `json:"id" yaml:"id"`
	Title                  string           `json:"title" yaml:"title"`
	FileName               string           `json:"file_name" yaml:"file_name"`
	MIMEType               string           `json:"mime_type,omitempty" yaml:"mime_type,omitempty"`
	SourceURL              string           `json:"source_url,omitempty" yaml:"source_url,omitempty"`
	ServerFile             string           `json:"server_file,omitempty" yaml:"server_file,omitempty"`
	Folder                 string           `json:"folder,omitempty" yaml:"folder,omitempty"`
	RawPath                string           `json:"raw_path,omitempty" yaml:"raw_path,omitempty"`
	TextPath               string           `json:"text_path,omitempty" yaml:"text_path,omitempty"`
	ContentHash            string           `json:"content_hash,omitempty" yaml:"content_hash,omitempty"`
	RawFileHash            string           `json:"raw_file_hash,omitempty" yaml:"raw_file_hash,omitempty"`
	ConvertedTextHash      string           `json:"converted_text_hash,omitempty" yaml:"converted_text_hash,omitempty"`
	ConversionStatus       string           `json:"conversion_status,omitempty" yaml:"conversion_status,omitempty"`
	PreservationStatus     string           `json:"preservation_status,omitempty" yaml:"preservation_status,omitempty"`
	Searchable             *bool            `json:"searchable,omitempty" yaml:"searchable,omitempty"`
	Status                 AttachmentStatus `json:"status" yaml:"status"`
	Error                  string           `json:"error,omitempty" yaml:"error,omitempty"`
	Size                   int64            `json:"size,omitempty" yaml:"size,omitempty"`
	QualityStatus          string           `json:"quality_status,omitempty" yaml:"quality_status,omitempty"`
	QualityScore           int              `json:"quality_score,omitempty" yaml:"quality_score,omitempty"`
	QualityFlags           string           `json:"quality_flags,omitempty" yaml:"quality_flags,omitempty"`
	QualityCodes           []string         `json:"quality_codes,omitempty" yaml:"quality_codes,omitempty"`
	Assets                 []Asset          `json:"assets,omitempty" yaml:"assets,omitempty"`
	ConvertedTextChars     int64            `json:"converted_text_chars,omitempty" yaml:"converted_text_chars,omitempty"`
	ConvertedNonSpaceChars int64            `json:"converted_non_space_chars,omitempty" yaml:"converted_non_space_chars,omitempty"`
	TableRowCount          int64            `json:"table_row_count,omitempty" yaml:"table_row_count,omitempty"`
	FormulaBlockCount      int64            `json:"formula_block_count,omitempty" yaml:"formula_block_count,omitempty"`
	FormulaHintCount       int64            `json:"formula_hint_count,omitempty" yaml:"formula_hint_count,omitempty"`
	ReplacementCharCount   int64            `json:"replacement_char_count,omitempty" yaml:"replacement_char_count,omitempty"`
}

type FormulaNotice struct {
	Severity                string `json:"severity"`
	Code                    string `json:"code"`
	Message                 string `json:"message"`
	SourceEquationAvailable bool   `json:"source_equation_available"`
	GeneratedLatexAvailable bool   `json:"generated_latex_available"`
	FormulaCount            int64  `json:"formula_count,omitempty"`
}

// SourceRequestDescriptor is the sanitized, replay-oriented subset of the
// producer's collection request. It intentionally has no cookie, CSRF,
// authorization, header, or local-path fields.
type SourceRequestDescriptor struct {
	Endpoint          string `json:"endpoint"`
	BookID            string `json:"bookid,omitempty"`
	NoFormYN          string `json:"noformyn,omitempty"`
	StateHistoryID    string `json:"statehistoryid,omitempty"`
	BBSID             string `json:"BBSID,omitempty"`
	MenuID            string `json:"Menuid,omitempty"`
	SourceContentHash string `json:"source_content_hash"`
}

type Document struct {
	SchemaVersion      int                     `json:"schema_version,omitempty" yaml:"schema_version,omitempty"`
	ID                 string                  `json:"id" yaml:"id"`
	Title              string                  `json:"title" yaml:"title"`
	Category           string                  `json:"category,omitempty" yaml:"category,omitempty"`
	SourceURL          string                  `json:"source_url" yaml:"source_url"`
	SourceContentHash  string                  `json:"source_content_hash,omitempty" yaml:"source_content_hash,omitempty"`
	SourceContentPath  string                  `json:"source_content_path,omitempty" yaml:"source_content_path,omitempty"`
	SourceRequestPath  string                  `json:"source_request_path,omitempty" yaml:"source_request_path,omitempty"`
	SourceRequest      SourceRequestDescriptor `json:"-" yaml:"-"`
	EffectiveDate      string                  `json:"effective_date,omitempty" yaml:"effective_date,omitempty"`
	PublishedDate      string                  `json:"published_date,omitempty" yaml:"published_date,omitempty"`
	CollectedAt        time.Time               `json:"collected_at" yaml:"collected_at"`
	ContentHash        string                  `json:"content_hash" yaml:"content_hash"`
	BodyHash           string                  `json:"body_hash,omitempty" yaml:"body_hash,omitempty"`
	ConversionStatus   string                  `json:"conversion_status,omitempty" yaml:"conversion_status,omitempty"`
	PreservationStatus string                  `json:"preservation_status,omitempty" yaml:"preservation_status,omitempty"`
	Searchable         *bool                   `json:"searchable,omitempty" yaml:"searchable,omitempty"`
	QualityStatus      string                  `json:"quality_status,omitempty" yaml:"quality_status,omitempty"`
	QualityCodes       []string                `json:"quality_codes,omitempty" yaml:"quality_codes,omitempty"`
	Language           string                  `json:"language" yaml:"language"`
	SourceID           string                  `json:"source_id,omitempty" yaml:"source_id,omitempty"`
	FileName           string                  `json:"file_name,omitempty" yaml:"file_name,omitempty"`
	RawPath            string                  `json:"raw_path,omitempty" yaml:"raw_path,omitempty"`
	TextPath           string                  `json:"text_path,omitempty" yaml:"text_path,omitempty"`
	FileHash           string                  `json:"file_content_hash,omitempty" yaml:"file_content_hash,omitempty"`
	RawFileHash        string                  `json:"raw_file_hash,omitempty" yaml:"raw_file_hash,omitempty"`
	Assets             []Asset                 `json:"assets,omitempty" yaml:"assets,omitempty"`
	Attachments        []Attachment            `json:"attachments,omitempty" yaml:"attachments,omitempty"`
	DocumentType       DocumentType            `json:"document_type" yaml:"document_type"`
	Body               string                  `json:"body,omitempty" yaml:"-"`
	Path               string                  `json:"path,omitempty" yaml:"-"`
}

func (d Document) URI() string {
	switch d.DocumentType {
	case DocumentTypeNotice:
		return "krx-rule://notices/" + d.ID
	default:
		return "krx-rule://rules/" + d.ID
	}
}

func (a Attachment) URI() string {
	return "krx-rule://attachments/" + a.ID
}

func (a Asset) Reference() string {
	return "krx-asset:" + a.ID
}

func (a Asset) EffectiveQualityCodes() []string {
	seen := make(map[string]struct{}, len(a.QualityCodes))
	out := make([]string, 0, len(a.QualityCodes))
	for _, value := range a.QualityCodes {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func HashText(text string) string {
	sum := sha256.Sum256([]byte(CanonicalText(text)))
	return hex.EncodeToString(sum[:])
}

func HashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// CanonicalText is the cross-project text canonicalization used by corpus
// hashes. Producers and consumers normalize line endings, NFC, and surrounding
// whitespace before hashing UTF-8 bytes.
func CanonicalText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = norm.NFC.String(text)
	return strings.TrimSpace(text)
}

// EffectiveBodyHash prefers the v2 body_hash field and falls back to the
// legacy content_hash field.
func (d Document) EffectiveBodyHash() string {
	if value := strings.TrimSpace(d.BodyHash); value != "" {
		return value
	}
	return strings.TrimSpace(d.ContentHash)
}

func (d Document) IsSearchable() bool {
	if d.Searchable != nil {
		return *d.Searchable
	}
	return true
}

func (d Document) EffectiveConversionStatus() string {
	if value := strings.TrimSpace(d.ConversionStatus); value != "" {
		return value
	}
	return string(AttachmentConverted)
}

func (d Document) EffectiveQualityCodes() []string {
	seen := make(map[string]struct{}, len(d.QualityCodes))
	out := make([]string, 0, len(d.QualityCodes))
	for _, value := range d.QualityCodes {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

// EffectiveRawFileHash prefers the v2 raw_file_hash field and falls back to
// the legacy document-level file_content_hash field.
func (d Document) EffectiveRawFileHash() string {
	if value := strings.TrimSpace(d.RawFileHash); value != "" {
		return value
	}
	return strings.TrimSpace(d.FileHash)
}

// EffectiveRawFileHash prefers the v2 raw_file_hash field and falls back to
// the legacy attachment content_hash field.
func (a Attachment) EffectiveRawFileHash() string {
	if value := strings.TrimSpace(a.RawFileHash); value != "" {
		return value
	}
	return strings.TrimSpace(a.ContentHash)
}

// EffectiveQualityCodes prefers the v2 list and falls back to the legacy
// comma-separated quality_flags value. The canonical result is sorted and
// duplicate-free so it is safe to include in a digest.
func (a Attachment) EffectiveQualityCodes() []string {
	values := append([]string(nil), a.QualityCodes...)
	if len(values) == 0 && strings.TrimSpace(a.QualityFlags) != "" {
		values = strings.Split(a.QualityFlags, ",")
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

// IsSearchable returns the explicit v2 searchable value when present. Legacy
// converted attachments remain searchable by default.
func (a Attachment) IsSearchable() bool {
	if a.Searchable != nil {
		return *a.Searchable
	}
	return a.EffectiveConversionStatus() == AttachmentConverted
}

func (a Attachment) EffectiveConversionStatus() AttachmentStatus {
	if value := strings.TrimSpace(a.ConversionStatus); value != "" {
		return AttachmentStatus(value)
	}
	return a.Status
}

func Slug(text string) string {
	text = strings.TrimSpace(strings.ToLower(text))
	repl := strings.NewReplacer("/", "-", "\\", "-", " ", "-", "_", "-", ".", "-", ":", "-")
	text = repl.Replace(text)
	re := regexp.MustCompile(`[^0-9a-z가-힣-]+`)
	text = re.ReplaceAllString(text, "")
	text = regexp.MustCompile(`-+`).ReplaceAllString(text, "-")
	text = strings.Trim(text, "-")
	if text == "" {
		return "untitled"
	}
	return text
}

func NormalizeLanguage(value string) string {
	value = strings.ToLower(strings.TrimSpace(strings.ReplaceAll(value, "_", "-")))
	switch value {
	case "en", "eng", "english", "en-us", "en-gb":
		return LanguageEnglish
	default:
		return LanguageKorean
	}
}
