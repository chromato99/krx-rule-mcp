package model

import (
	"path"
	"regexp"
	"strings"
)

var markdownImageReferencePattern = regexp.MustCompile(`!\[([^\]\r\n]*)\]\(\s*<?([^)>\s]+)>?(?:\s+["'][^)\r\n]*["'])?\s*\)`)

// PublicAssetText replaces verified local asset links with stable opaque
// references. Unknown local/data/file image targets are reduced to a textual
// placeholder so host or corpus paths are never exposed publicly.
func PublicAssetText(text string, assets []Asset) string {
	return rewriteAssetImages(text, assets, false)
}

// AssetSearchText removes image targets while retaining their alt text and,
// for verified assets, the producer source anchor. Asset bytes and local paths
// therefore never become search input.
func AssetSearchText(text string, assets []Asset) string {
	return rewriteAssetImages(text, assets, true)
}

func rewriteAssetImages(text string, assets []Asset, search bool) string {
	byTarget := make(map[string]Asset, len(assets)*2)
	for _, asset := range assets {
		byTarget[asset.Reference()] = asset
		if target := canonicalAssetTarget(asset.ReferencePath); target != "" {
			byTarget[target] = asset
		}
	}
	return markdownImageReferencePattern.ReplaceAllStringFunc(text, func(markup string) string {
		match := markdownImageReferencePattern.FindStringSubmatch(markup)
		if len(match) != 3 {
			return markup
		}
		alt := strings.TrimSpace(match[1])
		target := strings.TrimSpace(match[2])
		asset, verified := byTarget[target]
		if !verified {
			asset, verified = byTarget[canonicalAssetTarget(target)]
		}
		if search {
			parts := make([]string, 0, 2)
			if alt != "" {
				parts = append(parts, alt)
			}
			if verified && asset.SourceAnchor != "" && !strings.Contains(alt, asset.SourceAnchor) {
				parts = append(parts, asset.SourceAnchor)
			}
			return strings.Join(parts, " ")
		}
		if verified {
			return "![" + alt + "](" + asset.Reference() + ")"
		}
		if isPublicRemoteAssetTarget(target) {
			return markup
		}
		if alt == "" {
			return "[image]"
		}
		return "[image: " + alt + "]"
	})
}

func canonicalAssetTarget(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, `\`, "/"))
	if value == "" || strings.HasPrefix(value, "krx-asset:") {
		return value
	}
	return path.Clean(value)
}

func isPublicRemoteAssetTarget(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	return strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "http://")
}
