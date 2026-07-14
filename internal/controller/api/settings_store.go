package api

import (
	"context"
	"database/sql"
	"strconv"
	"time"
)

const (
	settingKeySiteTitle            = "site_title"
	settingKeySiteSubtitle         = "site_subtitle"
	settingKeyLogoURL              = "logo_url"
	settingKeyTheme                = "theme"
	settingKeyAgentControllerURL   = "agent_controller_url"
	settingKeyBackgroundURL        = "background_url"
	settingKeyDesktopBackgroundURL = "desktop_background_url"
	settingKeyMobileBackgroundURL  = "mobile_background_url"
	settingKeyAppearancePreset     = "appearance_preset"
	settingKeyCardOpacity          = "card_opacity"
	settingKeyCardBlur             = "card_blur"
	settingKeyCardRadius           = "card_radius"
	settingKeyBorderStrength       = "border_strength"
	settingKeyShadowStrength       = "shadow_strength"
	settingKeyBackgroundOverlay    = "background_overlay"
	settingKeyThemeColor           = "theme_color"
	settingKeyCustomCode           = "custom_code"
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
	now := time.Now().UTC().Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SiteSettings{}, err
	}
	defer rollbackUnlessCommitted(tx)
	// PATCH is deliberately sparse: persisting a read/modify/write snapshot of
	// every setting lets two disjoint concurrent requests overwrite each other
	// with stale values. Only keys represented by this request are upserted.
	values := make(map[string]string)
	if update.SiteTitle != nil {
		values[settingKeySiteTitle] = *update.SiteTitle
	}
	if update.SiteSubtitle != nil {
		values[settingKeySiteSubtitle] = *update.SiteSubtitle
	}
	if update.LogoURL != nil {
		values[settingKeyLogoURL] = *update.LogoURL
	}
	if update.Theme != nil {
		values[settingKeyTheme] = *update.Theme
	}
	if update.AgentControllerURL != nil {
		values[settingKeyAgentControllerURL] = *update.AgentControllerURL
	}
	if update.BackgroundURL != nil {
		values[settingKeyBackgroundURL] = *update.BackgroundURL
		if update.DesktopBackgroundURL == nil {
			values[settingKeyDesktopBackgroundURL] = *update.BackgroundURL
		}
	}
	if update.DesktopBackgroundURL != nil {
		// background_url is the legacy desktop alias and must remain in sync,
		// but no unrelated setting is touched.
		values[settingKeyDesktopBackgroundURL] = *update.DesktopBackgroundURL
		values[settingKeyBackgroundURL] = *update.DesktopBackgroundURL
	}
	if update.MobileBackgroundURL != nil {
		values[settingKeyMobileBackgroundURL] = *update.MobileBackgroundURL
	}
	if update.AppearancePreset != nil {
		values[settingKeyAppearancePreset] = *update.AppearancePreset
	}
	if update.CardOpacity != nil {
		values[settingKeyCardOpacity] = formatSettingsFloat(*update.CardOpacity)
	}
	if update.CardBlur != nil {
		values[settingKeyCardBlur] = formatSettingsFloat(*update.CardBlur)
	}
	if update.CardRadius != nil {
		values[settingKeyCardRadius] = formatSettingsFloat(*update.CardRadius)
	}
	if update.BorderStrength != nil {
		values[settingKeyBorderStrength] = formatSettingsFloat(*update.BorderStrength)
	}
	if update.ShadowStrength != nil {
		values[settingKeyShadowStrength] = formatSettingsFloat(*update.ShadowStrength)
	}
	if update.BackgroundOverlay != nil {
		values[settingKeyBackgroundOverlay] = formatSettingsFloat(*update.BackgroundOverlay)
	}
	if update.ThemeColor != nil {
		values[settingKeyThemeColor] = *update.ThemeColor
	}
	if update.CustomCode != nil {
		values[settingKeyCustomCode] = *update.CustomCode
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
	// Re-read after commit so the response includes concurrent disjoint updates
	// rather than the stale pre-PATCH snapshot.
	return s.siteSettings(ctx)
}

func (s *SQLiteStore) siteSettings(ctx context.Context) (SiteSettings, error) {
	settings := defaultSiteSettings()
	rows, err := s.db.QueryContext(ctx, `
		SELECT key, value, updated_at
		FROM settings
		WHERE key IN (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, settingKeySiteTitle, settingKeySiteSubtitle, settingKeyLogoURL, settingKeyTheme, settingKeyAgentControllerURL, settingKeyBackgroundURL, settingKeyDesktopBackgroundURL, settingKeyMobileBackgroundURL, settingKeyAppearancePreset, settingKeyCardOpacity, settingKeyCardBlur, settingKeyCardRadius, settingKeyBorderStrength, settingKeyShadowStrength, settingKeyBackgroundOverlay, settingKeyThemeColor, settingKeyCustomCode)
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
		case settingKeyAgentControllerURL:
			settings.AgentControllerURL = value
		case settingKeyBackgroundURL:
			settings.BackgroundURL = value
		case settingKeyDesktopBackgroundURL:
			settings.DesktopBackgroundURL = value
		case settingKeyMobileBackgroundURL:
			settings.MobileBackgroundURL = value
		case settingKeyAppearancePreset:
			if validAppearancePreset(value) {
				settings.AppearancePreset = value
			}
		case settingKeyCardOpacity:
			settings.CardOpacity = parseSettingsFloat(value, settings.CardOpacity)
		case settingKeyCardBlur:
			settings.CardBlur = parseSettingsFloat(value, settings.CardBlur)
		case settingKeyCardRadius:
			settings.CardRadius = parseSettingsFloat(value, settings.CardRadius)
		case settingKeyBorderStrength:
			settings.BorderStrength = parseSettingsFloat(value, settings.BorderStrength)
		case settingKeyShadowStrength:
			settings.ShadowStrength = parseSettingsFloat(value, settings.ShadowStrength)
		case settingKeyBackgroundOverlay:
			settings.BackgroundOverlay = parseSettingsFloat(value, settings.BackgroundOverlay)
		case settingKeyThemeColor:
			if settingsThemeColorPattern.MatchString(value) {
				settings.ThemeColor = value
			}
		case settingKeyCustomCode:
			settings.CustomCode = value
		}
		if updatedAt.Valid && (!latest.Valid || updatedAt.Int64 > latest.Int64) {
			latest = updatedAt
		}
	}
	if err := rows.Err(); err != nil {
		return SiteSettings{}, err
	}
	if settings.DesktopBackgroundURL == "" {
		settings.DesktopBackgroundURL = settings.BackgroundURL
	}
	if settings.BackgroundURL == "" {
		settings.BackgroundURL = settings.DesktopBackgroundURL
	}
	if latest.Valid && latest.Int64 > 0 {
		settings.UpdatedAt = time.Unix(latest.Int64, 0).UTC().Format(time.RFC3339)
	}
	return settings, nil
}

func formatSettingsFloat(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func parseSettingsFloat(value string, fallback float64) float64 {
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return fallback
	}
	return parsed
}
