package corpus

import (
	"encoding/binary"
	"fmt"
	"io"
	"net/url"
	"os"
	pathpkg "path"
	"path/filepath"
	"strings"

	"github.com/chromato99/krx-rule-mcp/internal/model"
)

const (
	maxAssetBytes     int64 = 16 << 20
	maxAssetDimension int64 = 20_000
	maxAssetPixels    int64 = 40_000_000
)

const (
	assetSourceHTMLInline = "html_inline"
	assetSourceHWPBinData = "hwp_bindata"
)

type imageAssetInfo struct {
	mimeType string
	width    int64
	height   int64
}

func validateAsset(root, bundle, contentPath, documentSourceURL, owner string, asset *model.Asset, allIDs map[string]string) error {
	if strings.TrimSpace(asset.ID) == "" || asset.ID != strings.TrimSpace(asset.ID) {
		return fmt.Errorf("%s: asset id is required and must not contain surrounding whitespace", owner)
	}
	if previous, ok := allIDs[asset.ID]; ok {
		return fmt.Errorf("duplicate_asset_id %q in %s and %s", asset.ID, previous, owner)
	}
	allIDs[asset.ID] = "asset " + owner
	if err := validateAssetSource(asset, documentSourceURL); err != nil {
		return fmt.Errorf("%s asset %q: %w", owner, asset.ID, err)
	}
	if asset.Searchable == nil {
		return fmt.Errorf("%s asset %q: searchable=false must be explicit", owner, asset.ID)
	}
	if *asset.Searchable {
		return fmt.Errorf("%s asset %q: binary assets must have searchable=false", owner, asset.ID)
	}
	asset.QualityCodes = asset.EffectiveQualityCodes()
	if err := validateQualityCodes(asset.QualityCodes); err != nil {
		return fmt.Errorf("%s asset %q: %w", owner, asset.ID, err)
	}
	if !containsString(asset.QualityCodes, "image_content_unindexed") {
		return fmt.Errorf("%s asset %q: image_content_unindexed quality code is required", owner, asset.ID)
	}

	switch asset.PreservationStatus {
	case "preserved":
		if strings.TrimSpace(asset.Error) != "" {
			return fmt.Errorf("%s asset %q: preserved asset must not contain error", owner, asset.ID)
		}
		return validatePreservedAsset(root, bundle, contentPath, owner, asset)
	case "missing", "failed":
		if asset.Path != "" || asset.MIMEType != "" || asset.RawFileHash != "" || asset.Size != 0 || asset.Width != 0 || asset.Height != 0 {
			return fmt.Errorf("%s asset %q: non-preserved asset must not expose file metadata", owner, asset.ID)
		}
		missingCode := "hwp_picture_missing"
		if asset.SourceKind == assetSourceHTMLInline {
			missingCode = "inline_image_missing"
		}
		if !containsString(asset.QualityCodes, missingCode) {
			return fmt.Errorf("%s asset %q: %s quality code is required", owner, asset.ID, missingCode)
		}
		if asset.PreservationStatus == "failed" && strings.TrimSpace(asset.Error) == "" {
			return fmt.Errorf("%s asset %q: failed asset requires error", owner, asset.ID)
		}
		return nil
	default:
		return fmt.Errorf("%s asset %q: invalid preservation_status %q", owner, asset.ID, asset.PreservationStatus)
	}
}

func validateAssetSource(asset *model.Asset, documentSourceURL string) error {
	anchor := strings.TrimSpace(asset.SourceAnchor)
	if anchor == "" || anchor != asset.SourceAnchor || len(anchor) > 4096 || strings.ContainsAny(anchor, "\x00\r\n") {
		return fmt.Errorf("source_anchor is required and must be a bounded single-line value")
	}
	switch asset.SourceKind {
	case assetSourceHTMLInline:
		sourceURL := strings.TrimSpace(asset.SourceURL)
		if sourceURL == "" || sourceURL != asset.SourceURL || len(sourceURL) > 4096 {
			return fmt.Errorf("html_inline source_url is required")
		}
		parsed, err := url.Parse(sourceURL)
		if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") {
			return fmt.Errorf("html_inline source_url must be an absolute HTTP(S) URL without credentials")
		}
		ownerURL, err := url.Parse(strings.TrimSpace(documentSourceURL))
		if err != nil || ownerURL.Host == "" || !strings.EqualFold(ownerURL.Scheme, parsed.Scheme) || !strings.EqualFold(ownerURL.Host, parsed.Host) {
			return fmt.Errorf("html_inline source_url must use the owning document source origin")
		}
		if !strings.HasPrefix(parsed.EscapedPath(), "/dataFile/law/img/") || parsed.Fragment != "" {
			return fmt.Errorf("html_inline source_url must use /dataFile/law/img/ without a fragment")
		}
		if anchor != "html-img:"+sourceURL {
			return fmt.Errorf("html_inline source_anchor must equal html-img:<source_url>")
		}
	case assetSourceHWPBinData:
		if strings.TrimSpace(asset.SourceURL) != "" {
			return fmt.Errorf("hwp_bindata source_url must be empty")
		}
		lower := strings.ToLower(anchor)
		if !strings.HasPrefix(lower, "hwp:bindata/") || len(anchor) == len("hwp:BinData/") {
			return fmt.Errorf("hwp_bindata source_anchor must identify hwp:BinData/<stream>")
		}
		stream := anchor[len("hwp:BinData/"):]
		if strings.HasPrefix(stream, "/") || strings.Contains(stream, `\`) || pathpkg.Clean(stream) != stream || strings.HasPrefix(stream, "../") {
			return fmt.Errorf("hwp_bindata source_anchor contains an invalid stream path")
		}
	default:
		return fmt.Errorf("invalid asset source_kind %q", asset.SourceKind)
	}
	return nil
}

func validatePreservedAsset(root, bundle, contentPath, owner string, asset *model.Asset) error {
	for name, value := range map[string]string{
		"path":          asset.Path,
		"mime_type":     asset.MIMEType,
		"raw_file_hash": asset.RawFileHash,
	} {
		if strings.TrimSpace(value) == "" || value != strings.TrimSpace(value) {
			return fmt.Errorf("%s asset %q: %s is required without surrounding whitespace", owner, asset.ID, name)
		}
	}
	if asset.Size <= 0 || asset.Size > maxAssetBytes {
		return fmt.Errorf("%s asset %q: size must be in 1..%d", owner, asset.ID, maxAssetBytes)
	}
	if err := validateAssetDimensions(asset.Width, asset.Height); err != nil {
		return fmt.Errorf("%s asset %q: %w", owner, asset.ID, err)
	}
	wantHash, err := requireSHA256("asset raw_file_hash", asset.RawFileHash)
	if err != nil {
		return fmt.Errorf("%s asset %q: %w", owner, asset.ID, err)
	}
	checkedPath, err := checkedAssetFile(root, bundle, asset.Path)
	if err != nil {
		return fmt.Errorf("%s asset %q path: %w", owner, asset.ID, err)
	}
	file, err := os.Open(checkedPath)
	if err != nil {
		return fmt.Errorf("%s asset %q: open: %w", owner, asset.ID, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("%s asset %q: stat: %w", owner, asset.ID, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s asset %q: path is not a regular file", owner, asset.ID)
	}
	if info.Size() != asset.Size {
		return fmt.Errorf("%s asset %q: asset size mismatch: got %d want %d", owner, asset.ID, info.Size(), asset.Size)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxAssetBytes+1))
	if err != nil {
		return fmt.Errorf("%s asset %q: read: %w", owner, asset.ID, err)
	}
	if int64(len(data)) != asset.Size || int64(len(data)) > maxAssetBytes {
		return fmt.Errorf("%s asset %q: bounded asset size mismatch", owner, asset.ID)
	}
	if got := model.HashBytes(data); got != wantHash {
		return fmt.Errorf("%s asset %q: raw_file_hash_mismatch: got %s want %s", owner, asset.ID, got, wantHash)
	}
	image, err := inspectImageAsset(data)
	if err != nil {
		return fmt.Errorf("%s asset %q: invalid image asset: %w", owner, asset.ID, err)
	}
	if asset.MIMEType != image.mimeType {
		return fmt.Errorf("%s asset %q: asset MIME/signature mismatch: got %s want %s", owner, asset.ID, asset.MIMEType, image.mimeType)
	}
	if asset.Width != image.width || asset.Height != image.height {
		return fmt.Errorf("%s asset %q: asset dimensions mismatch: got %dx%d want %dx%d", owner, asset.ID, asset.Width, asset.Height, image.width, image.height)
	}
	if contentPath != "" {
		reference, err := filepath.Rel(filepath.Dir(filepath.FromSlash(contentPath)), filepath.FromSlash(asset.Path))
		if err != nil {
			return fmt.Errorf("%s asset %q: derive content reference: %w", owner, asset.ID, err)
		}
		asset.ReferencePath = filepath.ToSlash(reference)
	}
	return nil
}

func checkedAssetFile(root, bundle, metadataPath string) (string, error) {
	if metadataPath == "" || filepath.IsAbs(metadataPath) || strings.Contains(metadataPath, `\`) {
		return "", fmt.Errorf("asset path must be a non-empty portable relative path")
	}
	for _, part := range strings.Split(filepath.ToSlash(metadataPath), "/") {
		if part == ".." {
			return "", fmt.Errorf("parent traversal is not allowed")
		}
	}
	clean := filepath.Clean(metadataPath)
	if clean == "." || clean != metadataPath {
		return "", fmt.Errorf("asset path must be clean")
	}
	return checkedExistingPath(root, bundle, filepath.Join(root, clean))
}

func inspectImageAsset(data []byte) (imageAssetInfo, error) {
	if len(data) == 0 || int64(len(data)) > maxAssetBytes {
		return imageAssetInfo{}, fmt.Errorf("image byte size must be in 1..%d", maxAssetBytes)
	}
	var info imageAssetInfo
	switch {
	case len(data) >= 10 && (string(data[:6]) == "GIF87a" || string(data[:6]) == "GIF89a"):
		info = imageAssetInfo{"image/gif", int64(binary.LittleEndian.Uint16(data[6:8])), int64(binary.LittleEndian.Uint16(data[8:10]))}
	case len(data) >= 24 && string(data[:8]) == "\x89PNG\r\n\x1a\n" && string(data[12:16]) == "IHDR":
		info = imageAssetInfo{"image/png", int64(binary.BigEndian.Uint32(data[16:20])), int64(binary.BigEndian.Uint32(data[20:24]))}
	case len(data) >= 26 && string(data[:2]) == "BM":
		dibSize := binary.LittleEndian.Uint32(data[14:18])
		switch {
		case dibSize == 12:
			info = imageAssetInfo{"image/bmp", int64(binary.LittleEndian.Uint16(data[18:20])), int64(binary.LittleEndian.Uint16(data[20:22]))}
		case dibSize >= 40:
			width := int64(int32(binary.LittleEndian.Uint32(data[18:22])))
			height := int64(int32(binary.LittleEndian.Uint32(data[22:26])))
			if height < 0 {
				height = -height
			}
			info = imageAssetInfo{"image/bmp", width, height}
		default:
			return imageAssetInfo{}, fmt.Errorf("unsupported BMP DIB header size %d", dibSize)
		}
	case len(data) >= 2 && data[0] == 0xff && data[1] == 0xd8:
		width, height, err := jpegAssetDimensions(data)
		if err != nil {
			return imageAssetInfo{}, err
		}
		info = imageAssetInfo{"image/jpeg", width, height}
	default:
		return imageAssetInfo{}, fmt.Errorf("unsupported or mismatched image signature")
	}
	if err := validateAssetDimensions(info.width, info.height); err != nil {
		return imageAssetInfo{}, err
	}
	return info, nil
}

func validateAssetDimensions(width, height int64) error {
	if width <= 0 || height <= 0 {
		return fmt.Errorf("image dimensions must be positive")
	}
	if width > maxAssetDimension || height > maxAssetDimension {
		return fmt.Errorf("image dimensions exceed %dpx", maxAssetDimension)
	}
	if width*height > maxAssetPixels {
		return fmt.Errorf("image pixel count exceeds %d", maxAssetPixels)
	}
	return nil
}

func jpegAssetDimensions(data []byte) (int64, int64, error) {
	sof := map[byte]struct{}{
		0xc0: {}, 0xc1: {}, 0xc2: {}, 0xc3: {}, 0xc5: {}, 0xc6: {}, 0xc7: {},
		0xc9: {}, 0xca: {}, 0xcb: {}, 0xcd: {}, 0xce: {}, 0xcf: {},
	}
	offset := 2
	for offset+4 <= len(data) {
		if data[offset] != 0xff {
			offset++
			continue
		}
		for offset < len(data) && data[offset] == 0xff {
			offset++
		}
		if offset >= len(data) {
			break
		}
		marker := data[offset]
		offset++
		if marker == 0x01 || (marker >= 0xd0 && marker <= 0xd9) {
			continue
		}
		if offset+2 > len(data) {
			break
		}
		segmentLength := int(binary.BigEndian.Uint16(data[offset : offset+2]))
		if segmentLength < 2 || offset+segmentLength > len(data) {
			return 0, 0, fmt.Errorf("malformed JPEG segment length")
		}
		if _, ok := sof[marker]; ok {
			if segmentLength < 7 {
				return 0, 0, fmt.Errorf("malformed JPEG SOF segment")
			}
			height := int64(binary.BigEndian.Uint16(data[offset+3 : offset+5]))
			width := int64(binary.BigEndian.Uint16(data[offset+5 : offset+7]))
			return width, height, nil
		}
		offset += segmentLength
	}
	return 0, 0, fmt.Errorf("JPEG has no supported SOF dimensions")
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
