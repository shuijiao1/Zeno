package api

import (
	"context"
	"database/sql"
	"time"
)

const (
	settingKeySiteTitle     = "site_title"
	settingKeySiteSubtitle  = "site_subtitle"
	settingKeyLogoURL       = "logo_url"
	settingKeyTheme         = "theme"
	settingKeyBackgroundURL = "background_url"
)

func (s *SQLiteStore) PublicSettings(ctx context.Context) (SiteSettings, error) {
	return s.siteSettings(ctx)
}

func (s *SQLiteStore) AdminSettings(ctx context.Context) (SiteSettings, error) {
	return s.siteSettings(ctx)
}

func (s *SQLiteStore) UpdateAdminSettings(ctx context.Context, update AdminSettingsUpdateRequest) (SiteSettings, error) {
	if err := update.normalize(); err != nil {
		return SiteSettings{}, err
	}
	settings, err := s.siteSettings(ctx)
	if err != nil {
		return SiteSettings{}, err
	}
	if update.SiteTitle != nil {
		settings.SiteTitle = *update.SiteTitle
	}
	if update.SiteSubtitle != nil {
		settings.SiteSubtitle = *update.SiteSubtitle
	}
	if update.LogoURL != nil {
		settings.LogoURL = *update.LogoURL
	}
	if update.Theme != nil {
		settings.Theme = *update.Theme
	}
	if update.BackgroundURL != nil {
		settings.BackgroundURL = *update.BackgroundURL
	}

	now := time.Now().UTC().Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SiteSettings{}, err
	}
	defer rollbackUnlessCommitted(tx)
	values := map[string]string{
		settingKeySiteTitle:     settings.SiteTitle,
		settingKeySiteSubtitle:  settings.SiteSubtitle,
		settingKeyLogoURL:       settings.LogoURL,
		settingKeyTheme:         settings.Theme,
		settingKeyBackgroundURL: settings.BackgroundURL,
	}
	for key, value := range values {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO settings (key, value, updated_at)
			VALUES (?, ?, ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
		`, key, value, now); err != nil {
			return SiteSettings{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return SiteSettings{}, err
	}
	tx = nil
	settings.UpdatedAt = time.Unix(now, 0).UTC().Format(time.RFC3339)
	return settings, nil
}

func (s *SQLiteStore) siteSettings(ctx context.Context) (SiteSettings, error) {
	settings := defaultSiteSettings()
	rows, err := s.db.QueryContext(ctx, `
		SELECT key, value, updated_at
		FROM settings
		WHERE key IN (?, ?, ?, ?, ?)
	`, settingKeySiteTitle, settingKeySiteSubtitle, settingKeyLogoURL, settingKeyTheme, settingKeyBackgroundURL)
	if err != nil {
		return SiteSettings{}, err
	}
	defer rows.Close()
	var latest sql.NullInt64
	for rows.Next() {
		var key, value string
		var updatedAt sql.NullInt64
		if err := rows.Scan(&key, &value, &updatedAt); err != nil {
			return SiteSettings{}, err
		}
		switch key {
		case settingKeySiteTitle:
			settings.SiteTitle = value
		case settingKeySiteSubtitle:
			settings.SiteSubtitle = value
		case settingKeyLogoURL:
			settings.LogoURL = value
		case settingKeyTheme:
			settings.Theme = value
		case settingKeyBackgroundURL:
			settings.BackgroundURL = value
		}
		if updatedAt.Valid && (!latest.Valid || updatedAt.Int64 > latest.Int64) {
			latest = updatedAt
		}
	}
	if err := rows.Err(); err != nil {
		return SiteSettings{}, err
	}
	if latest.Valid && latest.Int64 > 0 {
		settings.UpdatedAt = time.Unix(latest.Int64, 0).UTC().Format(time.RFC3339)
	}
	return settings, nil
}
