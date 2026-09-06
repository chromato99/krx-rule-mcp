package evaluation

import (
	"fmt"
	"regexp"
	"strings"

	searchindex "github.com/chromato99/krx-rule-mcp/internal/index"
	"github.com/chromato99/krx-rule-mcp/internal/model"
)

// FixtureGroundingIssue identifies an expectation that is inconsistent with
// the corpus and immutable index being evaluated. This validates the labelled
// target, not retrieval quality: a valid target may still rank poorly.
type FixtureGroundingIssue struct {
	CaseID      string
	TargetIndex int
	Reason      string
}

func (issue FixtureGroundingIssue) String() string {
	return fmt.Sprintf("case %q target %d: %s", issue.CaseID, issue.TargetIndex+1, issue.Reason)
}

type fixtureGroundingChunk struct {
	DocumentID      string
	Source          string
	AttachmentID    string
	AttachmentTitle string
	ArticleID       string
	HeadingPath     []string
	Text            string
}

var (
	groundingKoreanArticlePattern   = regexp.MustCompile(`^제\s*([0-9]+)\s*조(?:\s*의\s*([0-9]+))?`)
	groundingEnglishArticlePattern  = regexp.MustCompile(`(?i)^§\s*([0-9]+(?:\s*-\s*[0-9]+)?)\s*[.]`)
	groundingMarkdownHeadingPattern = regexp.MustCompile(`^#{1,6}\s+(.+)$`)
)

// AuditFixtureGrounding checks every labelled target against the exact corpus
// and chunks loaded for the evaluation. It catches stale IDs, anchors excluded
// by input filters, and evidence phrases that do not exist in the labelled
// article or attachment.
func AuditFixtureGrounding(fixture Fixture, repo *searchindex.Repository) []FixtureGroundingIssue {
	if repo == nil || repo.Engine == nil {
		return []FixtureGroundingIssue{{Reason: "repository is nil or has no search engine"}}
	}

	chunksByDocument := make(map[string][]fixtureGroundingChunk)
	for _, chunk := range repo.Engine.Chunks() {
		chunksByDocument[chunk.DocID] = append(chunksByDocument[chunk.DocID], fixtureGroundingChunk{
			DocumentID:      chunk.DocID,
			Source:          chunk.Source,
			AttachmentID:    chunk.AttachmentID,
			AttachmentTitle: chunk.AttachmentTitle,
			ArticleID:       chunk.ArticleID,
			HeadingPath:     append([]string(nil), chunk.HeadingPath...),
			Text:            chunk.Text,
		})
	}

	var issues []FixtureGroundingIssue
	for _, item := range fixture.Cases {
		for targetIndex, target := range item.Expectation.Targets {
			doc, ok := repo.Documents[target.DocumentID]
			if !ok {
				issues = append(issues, groundingIssue(item.ID, targetIndex, "document %q does not exist", target.DocumentID))
				continue
			}
			if reason := targetFilterMismatch(item.Input, doc); reason != "" {
				issues = append(issues, groundingIssue(item.ID, targetIndex, "%s", reason))
			}

			if target.AttachmentID != "" && !documentHasAttachment(doc, target.AttachmentID) {
				issues = append(issues, groundingIssue(item.ID, targetIndex,
					"attachment %q does not belong to document %q", target.AttachmentID, target.DocumentID))
				continue
			}
			if target.ArticleID == "" && target.AttachmentID == "" && target.Evidence == nil {
				continue
			}

			matching := matchingGroundingChunks(chunksByDocument[target.DocumentID], target)
			if len(matching) == 0 {
				anchor := "document body"
				if target.ArticleID != "" {
					anchor = "article " + target.ArticleID
				}
				if target.AttachmentID != "" {
					anchor = "attachment " + target.AttachmentID
					if target.ArticleID != "" {
						anchor += " article " + target.ArticleID
					}
				}
				issues = append(issues, groundingIssue(item.ID, targetIndex,
					"%s has no searchable chunk in document %q", anchor, target.DocumentID))
				continue
			}
			if target.Evidence == nil {
				continue
			}

			text := groundingTargetText(repo, doc, target, matching)
			for _, required := range target.Evidence.MustContainAll {
				if !containsGroundingText(text, required) {
					issues = append(issues, groundingIssue(item.ID, targetIndex,
						"must_contain_all phrase %q is absent from the labelled target", required))
				}
			}
			if len(target.Evidence.MustContainAny) > 0 && !containsAnyGroundingText(text, target.Evidence.MustContainAny) {
				issues = append(issues, groundingIssue(item.ID, targetIndex,
					"none of must_contain_any phrases are present in the labelled target: %q", target.Evidence.MustContainAny))
			}
			for _, forbidden := range target.Evidence.MustNotContain {
				if containsGroundingText(text, forbidden) {
					issues = append(issues, groundingIssue(item.ID, targetIndex,
						"must_not_contain phrase %q is present in the labelled target", forbidden))
				}
			}
			if item.Expectation.ClaimRelation == "contradicts" &&
				!containsAnyGroundingText(text, target.Evidence.RelationMustContainAny) {
				issues = append(issues, groundingIssue(item.ID, targetIndex,
					"none of relation_must_contain_any phrases are present in the labelled target: %q", target.Evidence.RelationMustContainAny))
			}
		}
	}
	return issues
}

func ValidateFixtureGrounding(fixture Fixture, repo *searchindex.Repository) error {
	issues := AuditFixtureGrounding(fixture, repo)
	if len(issues) == 0 {
		return nil
	}
	var message strings.Builder
	fmt.Fprintf(&message, "evaluation fixture has %d corpus-grounding issue(s)", len(issues))
	for _, issue := range issues {
		message.WriteString("\n- ")
		message.WriteString(issue.String())
	}
	return fmt.Errorf("%s", message.String())
}

func groundingIssue(caseID string, targetIndex int, format string, args ...any) FixtureGroundingIssue {
	return FixtureGroundingIssue{CaseID: caseID, TargetIndex: targetIndex, Reason: fmt.Sprintf(format, args...)}
}

func targetFilterMismatch(input CaseInput, doc model.Document) string {
	if input.Language != "" && model.NormalizeLanguage(doc.Language) != model.NormalizeLanguage(input.Language) {
		return fmt.Sprintf("document %q language %q is excluded by language filter %q", doc.ID, doc.Language, input.Language)
	}
	if input.DocumentType != "" && string(doc.DocumentType) != input.DocumentType {
		return fmt.Sprintf("document %q type %q is excluded by document_type filter %q", doc.ID, doc.DocumentType, input.DocumentType)
	}
	if input.Category != "" && doc.Category != input.Category {
		return fmt.Sprintf("document %q category %q is excluded by category filter %q", doc.ID, doc.Category, input.Category)
	}
	if input.EffectiveFrom != "" && (doc.EffectiveDate == "" || doc.EffectiveDate < input.EffectiveFrom) {
		return fmt.Sprintf("document %q effective_date %q is before effective_from %q", doc.ID, doc.EffectiveDate, input.EffectiveFrom)
	}
	if input.EffectiveTo != "" && (doc.EffectiveDate == "" || doc.EffectiveDate > input.EffectiveTo) {
		return fmt.Sprintf("document %q effective_date %q is after effective_to %q", doc.ID, doc.EffectiveDate, input.EffectiveTo)
	}
	return ""
}

func documentHasAttachment(doc model.Document, attachmentID string) bool {
	for _, attachment := range doc.Attachments {
		if attachment.ID == attachmentID {
			return true
		}
	}
	return false
}

func matchingGroundingChunks(chunks []fixtureGroundingChunk, target Target) []fixtureGroundingChunk {
	matching := make([]fixtureGroundingChunk, 0)
	for _, chunk := range chunks {
		if target.ArticleID != "" && chunk.ArticleID != target.ArticleID {
			continue
		}
		if target.AttachmentID != "" && chunk.AttachmentID != target.AttachmentID {
			continue
		}
		matching = append(matching, chunk)
	}
	return matching
}

func groundingText(chunks []fixtureGroundingChunk) string {
	var values []string
	for _, chunk := range chunks {
		values = append(values, strings.Join(chunk.HeadingPath, " "), chunk.AttachmentTitle, chunk.Text)
	}
	return normalizeEvidenceText(strings.Join(values, " "))
}

func groundingTargetText(repo *searchindex.Repository, doc model.Document, target Target, chunks []fixtureGroundingChunk) string {
	values := []string{groundingText(chunks)}
	if target.AttachmentID != "" {
		if attachment, ok := repo.Attachments[target.AttachmentID]; ok {
			values = append(values, attachment.Text)
		}
	} else if target.ArticleID != "" {
		values = append(values, rawArticleTargetText(doc.Body, target.ArticleID))
	}
	return normalizeEvidenceText(strings.Join(values, " "))
}

// rawArticleTargetText audits labels against the preserved corpus rather than
// trusting index anchors alone. PDF extraction can occasionally place a
// PART/CHAPTER heading immediately after an article heading; the index should
// still be tested separately, but that layout defect must not make a correct
// fixture label look factually wrong.
func rawArticleTargetText(body, targetArticleID string) string {
	lines := strings.Split(strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\r", "\n"), "\n")
	var sections []string
	var current []string
	collecting := false
	flush := func() {
		if collecting && len(current) > 0 {
			sections = append(sections, strings.Join(current, "\n"))
		}
		current = nil
	}
	for _, line := range lines {
		articleID := groundingArticleID(line)
		if articleID != "" {
			flush()
			collecting = articleID == targetArticleID
		}
		if collecting {
			current = append(current, line)
		}
	}
	flush()
	return strings.Join(sections, "\n")
}

func groundingArticleID(line string) string {
	value := strings.TrimSpace(line)
	if match := groundingMarkdownHeadingPattern.FindStringSubmatch(value); len(match) == 2 {
		value = strings.TrimSpace(match[1])
	}
	value = strings.TrimPrefix(value, "**")
	match := groundingKoreanArticlePattern.FindStringSubmatch(value)
	if len(match) > 0 {
		id := "제" + match[1] + "조"
		if len(match) > 2 && match[2] != "" {
			id += "의" + match[2]
		}
		return id
	}
	match = groundingEnglishArticlePattern.FindStringSubmatch(value)
	if len(match) > 0 {
		return "§" + strings.ReplaceAll(match[1], " ", "")
	}
	return ""
}

func containsGroundingText(normalizedText, phrase string) bool {
	return strings.Contains(normalizedText, normalizeEvidenceText(phrase))
}

func containsAnyGroundingText(normalizedText string, phrases []string) bool {
	for _, phrase := range phrases {
		if containsGroundingText(normalizedText, phrase) {
			return true
		}
	}
	return false
}
