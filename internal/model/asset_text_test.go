package model

import (
	"strings"
	"testing"
)

func TestAssetTextRewritesVerifiedLocalReference(t *testing.T) {
	asset := Asset{
		ID:            "asset-chart",
		SourceAnchor:  "hwp:BinData/BIN0001.png",
		ReferencePath: "../assets/attachment/chart.png",
	}
	text := "설명\n\n![차트](../assets/attachment/chart.png)"
	public := PublicAssetText(text, []Asset{asset})
	if !strings.Contains(public, "![차트](krx-asset:asset-chart)") || strings.Contains(public, "../assets") {
		t.Fatalf("PublicAssetText() = %q", public)
	}
	search := AssetSearchText(text, []Asset{asset})
	if search != "설명\n\n차트 hwp:BinData/BIN0001.png" || strings.Contains(search, "assets/") || strings.Contains(search, "krx-asset:") {
		t.Fatalf("AssetSearchText() = %q", search)
	}
}

func TestPublicAssetTextRemovesUnknownLocalAndDataTargets(t *testing.T) {
	text := "![로컬](../../private/image.png) ![인라인](data:image/png;base64,AAAA) ![공식](https://rule.krx.co.kr/dataFile/law/img/a.png)"
	got := PublicAssetText(text, nil)
	if strings.Contains(got, "../../private") || strings.Contains(got, "data:image") {
		t.Fatalf("PublicAssetText leaked non-public target: %q", got)
	}
	if !strings.Contains(got, "https://rule.krx.co.kr/dataFile/law/img/a.png") {
		t.Fatalf("PublicAssetText removed public remote target: %q", got)
	}
}

func TestPublicAssetTextKeepsVerifiedOpaqueReference(t *testing.T) {
	asset := Asset{ID: "asset-chart", SourceAnchor: "html-img:https://example.test/dataFile/law/img/chart.png"}
	got := PublicAssetText("![차트](krx-asset:asset-chart)", []Asset{asset})
	if got != "![차트](krx-asset:asset-chart)" {
		t.Fatalf("PublicAssetText() = %q", got)
	}
}
