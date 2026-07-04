package api

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"path/filepath"
	"strings"
	"time"
)

const maxAdminAssetBytes = 4 * 1024 * 1024

func maxAdminAssetRequestBytes() int64 {
	return int64(maxAdminAssetBytes*2 + 8192)
}

func (s *SQLiteStore) UploadAdminAsset(ctx context.Context, request AdminAssetUploadRequest) (AdminAsset, error) {
	filename, contentType, extension, data, err := normalizeAdminAssetUpload(request)
	if err != nil {
		return AdminAsset{}, err
	}
	sum := sha256.Sum256(data)
	assetID := "asset_" + hex.EncodeToString(sum[:8]) + extension
	now := time.Now().UTC().Unix()
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO assets (id, filename, content_type, size_bytes, content, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			filename = excluded.filename,
			content_type = excluded.content_type,
			size_bytes = excluded.size_bytes,
			content = excluded.content,
			created_at = excluded.created_at
	`, assetID, filename, contentType, len(data), data, now); err != nil {
		return AdminAsset{}, err
	}
	return adminAssetDTO(assetID, filename, contentType, int64(len(data)), now), nil
}

func (s *SQLiteStore) DeleteAdminAsset(ctx context.Context, assetID string) error {
	if !validAdminAssetID(assetID) {
		return errAssetNotFound
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM assets WHERE id = ?`, assetID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return errAssetNotFound
	}
	return nil
}

func (s *SQLiteStore) PublicAsset(ctx context.Context, assetID string) (PublicAsset, error) {
	if !validAdminAssetID(assetID) {
		return PublicAsset{}, errAssetNotFound
	}
	var asset PublicAsset
	var createdAt int64
	if err := s.db.QueryRowContext(ctx, `
		SELECT id, filename, content_type, size_bytes, content, created_at
		FROM assets
		WHERE id = ?
	`, assetID).Scan(&asset.ID, &asset.Filename, &asset.ContentType, &asset.SizeBytes, &asset.Bytes, &createdAt); err != nil {
		if err == sql.ErrNoRows {
			return PublicAsset{}, errAssetNotFound
		}
		return PublicAsset{}, err
	}
	asset.CreatedAt = time.Unix(createdAt, 0).UTC().Format(time.RFC3339)
	return asset, nil
}

func normalizeAdminAssetUpload(request AdminAssetUploadRequest) (filename string, contentType string, extension string, data []byte, err error) {
	filename = sanitizeAdminAssetFilename(request.Filename)
	if filename == "" {
		return "", "", "", nil, errInvalidAdminAssetUpload
	}
	encoded := strings.TrimSpace(request.DataBase64)
	if encoded == "" {
		return "", "", "", nil, errInvalidAdminAssetUpload
	}
	if comma := strings.Index(encoded, ","); comma >= 0 && strings.Contains(encoded[:comma], ";base64") {
		encoded = encoded[comma+1:]
	}
	data, err = base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", "", "", nil, errInvalidAdminAssetUpload
	}
	if len(data) == 0 || len(data) > maxAdminAssetBytes {
		return "", "", "", nil, errInvalidAdminAssetUpload
	}
	contentType, extension, ok := detectAdminAssetImage(data)
	if !ok {
		return "", "", "", nil, errInvalidAdminAssetUpload
	}
	declared := strings.ToLower(strings.TrimSpace(strings.Split(request.ContentType, ";")[0]))
	if declared != "" && declared != contentType {
		return "", "", "", nil, errInvalidAdminAssetUpload
	}
	return filename, contentType, extension, data, nil
}

func sanitizeAdminAssetFilename(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	base := filepath.Base(strings.ReplaceAll(trimmed, "\\", "/"))
	base = strings.TrimSpace(base)
	if base == "." || base == "/" || base == "" {
		return ""
	}
	if len([]rune(base)) > 120 {
		return ""
	}
	return base
}

func detectAdminAssetImage(data []byte) (contentType string, extension string, ok bool) {
	if len(data) >= 8 && string(data[:8]) == "\x89PNG\r\n\x1a\n" {
		return "image/png", ".png", true
	}
	if len(data) >= 3 && data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff {
		return "image/jpeg", ".jpg", true
	}
	if len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
		return "image/webp", ".webp", true
	}
	return "", "", false
}

func validAdminAssetID(assetID string) bool {
	if assetID == "" || strings.Contains(assetID, "/") || strings.Contains(assetID, "\\") || strings.Contains(assetID, "..") {
		return false
	}
	return strings.HasPrefix(assetID, "asset_") && (strings.HasSuffix(assetID, ".png") || strings.HasSuffix(assetID, ".jpg") || strings.HasSuffix(assetID, ".webp"))
}

func adminAssetDTO(id, filename, contentType string, sizeBytes int64, createdAt int64) AdminAsset {
	return AdminAsset{
		ID:          id,
		Filename:    filename,
		ContentType: contentType,
		SizeBytes:   sizeBytes,
		URL:         "/api/public/v1/assets/" + id,
		CreatedAt:   time.Unix(createdAt, 0).UTC().Format(time.RFC3339),
	}
}
