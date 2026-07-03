package model

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
	"time"
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
	Status                 AttachmentStatus `json:"status" yaml:"status"`
	Error                  string           `json:"error,omitempty" yaml:"error,omitempty"`
	Size                   int64            `json:"size,omitempty" yaml:"size,omitempty"`
	QualityStatus          string           `json:"quality_status,omitempty" yaml:"quality_status,omitempty"`
	QualityScore           int              `json:"quality_score,omitempty" yaml:"quality_score,omitempty"`
	QualityFlags           string           `json:"quality_flags,omitempty" yaml:"quality_flags,omitempty"`
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

type Document struct {
	ID            string       `json:"id" yaml:"id"`
	Title         string       `json:"title" yaml:"title"`
	Category      string       `json:"category,omitempty" yaml:"category,omitempty"`
	SourceURL     string       `json:"source_url" yaml:"source_url"`
	EffectiveDate string       `json:"effective_date,omitempty" yaml:"effective_date,omitempty"`
	PublishedDate string       `json:"published_date,omitempty" yaml:"published_date,omitempty"`
	CollectedAt   time.Time    `json:"collected_at" yaml:"collected_at"`
	ContentHash   string       `json:"content_hash" yaml:"content_hash"`
	Language      string       `json:"language" yaml:"language"`
	SourceID      string       `json:"source_id,omitempty" yaml:"source_id,omitempty"`
	FileName      string       `json:"file_name,omitempty" yaml:"file_name,omitempty"`
	RawPath       string       `json:"raw_path,omitempty" yaml:"raw_path,omitempty"`
	TextPath      string       `json:"text_path,omitempty" yaml:"text_path,omitempty"`
	FileHash      string       `json:"file_content_hash,omitempty" yaml:"file_content_hash,omitempty"`
	Attachments   []Attachment `json:"attachments,omitempty" yaml:"attachments,omitempty"`
	DocumentType  DocumentType `json:"document_type" yaml:"document_type"`
	Body          string       `json:"body,omitempty" yaml:"-"`
	Path          string       `json:"path,omitempty" yaml:"-"`
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

func HashText(text string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(text)))
	return hex.EncodeToString(sum[:])
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
