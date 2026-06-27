package payments

import (
	"context"
	"fmt"
	"strings"

	"github.com/Nakedjustice/remnaWake/internal/i18n"
	tg "github.com/Nakedjustice/remnaWake/internal/telegram"
)

// ProxyStatus is one proxy's latest health result, kept payments-local so this
// package stays decoupled from the xraychecker package; main.go adapts the
// concrete client to XrayChecker.
type ProxyStatus struct {
	Name      string
	Protocol  string
	Address   string
	SubName   string
	Up        bool
	LatencyMs int
}

// XrayChecker is the subset of the xray-checker client the admin views need: the
// current per-proxy status. Wired once at startup via SetXrayChecker.
type XrayChecker interface {
	Status(ctx context.Context) ([]ProxyStatus, error)
}

// WebProxyRow is one proxy's health row shown in the bot report and Mini App.
// NotificationsMuted reports whether admins have switched off this server's
// up/down alerts from the Proxy Health tab.
type WebProxyRow struct {
	Name               string `json:"name"`
	Protocol           string `json:"protocol"`
	Address            string `json:"address"`
	SubName            string `json:"sub_name"`
	Up                 bool   `json:"up"`
	LatencyMs          int    `json:"latency_ms"`
	NotificationsMuted bool   `json:"notifications_muted"`
}

// WebProxyHealth is the proxy-monitoring snapshot shared by the bot report and
// the Mini App admin panel. Configured is false when no xray-checker is wired in.
// DashboardURL, when set, is the public URL of the checker's own web dashboard;
// it is surfaced independently of Configured so the link works even when the bot
// does not poll /metrics.
type WebProxyHealth struct {
	Configured   bool          `json:"configured"`
	Proxies      []WebProxyRow `json:"proxies"`
	Up           int           `json:"up"`
	Down         int           `json:"down"`
	DashboardURL string        `json:"dashboard_url,omitempty"`
}

// SetXrayChecker wires the optional xray-checker client. Called once at startup
// when XRAY_CHECKER_URL is configured; until then proxy monitoring is off.
func (s *Service) SetXrayChecker(c XrayChecker) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.xrayChecker = c
}

func (s *Service) getXrayChecker() XrayChecker {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.xrayChecker
}

// xrayCheckerConfigured reports whether the xray-checker client is wired in.
func (s *Service) xrayCheckerConfigured() bool {
	return s.getXrayChecker() != nil
}

// AdminProxyHealth returns the current proxy-health snapshot from the optional
// xray-checker sidecar. The caller identity is always checked because Web API
// requests reach this method directly.
func (s *Service) AdminProxyHealth(ctx context.Context, telegramID int64) (*WebProxyHealth, error) {
	if err := s.adminGuard(telegramID); err != nil {
		return nil, err
	}
	return s.proxyHealth(ctx)
}

// proxyHealth fetches and aggregates the current per-proxy status.
func (s *Service) proxyHealth(ctx context.Context) (*WebProxyHealth, error) {
	dashboardURL := s.getCheckerURL()
	checker := s.getXrayChecker()
	if checker == nil {
		return &WebProxyHealth{Configured: false, DashboardURL: dashboardURL}, nil
	}
	statuses, err := checker.Status(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: xray checker: %v", ErrPanelUnavailable, err)
	}
	// Bulk-load the muted set once; degrade to "none muted" on error so a storage
	// hiccup never hides the health view.
	muted, err := s.store.MutedProxyKeys(ctx)
	if err != nil {
		s.logger.Error("xray checker: muted keys lookup failed", "err", err.Error())
		muted = nil
	}
	out := &WebProxyHealth{Configured: true, DashboardURL: dashboardURL, Proxies: make([]WebProxyRow, 0, len(statuses))}
	for _, p := range statuses {
		out.Proxies = append(out.Proxies, WebProxyRow{
			Name: p.Name, Protocol: p.Protocol, Address: p.Address, SubName: p.SubName,
			Up: p.Up, LatencyMs: p.LatencyMs,
			NotificationsMuted: muted[proxyMuteKey(p.Address, p.Name, p.SubName)],
		})
		if p.Up {
			out.Up++
		} else {
			out.Down++
		}
	}
	return out, nil
}

// proxyMuteKey is the canonical per-proxy identity used as the mute key. It must
// match xraychecker's stateIdentity (address|name|sub_name) so the Mini App
// toggle and the monitor's alert suppression act on the same key.
func proxyMuteKey(address, name, subName string) string {
	return address + "|" + name + "|" + subName
}

// NotifyProxyDown DMs every admin that a monitored proxy went down. Implements
// xraychecker.Notifier.
func (s *Service) NotifyProxyDown(ctx context.Context, name, protocol, address string) {
	s.broadcastProxyAlert(ctx, fmt.Sprintf(i18n.T("🔴 Прокси недоступен: %s"), proxyLabel(name, protocol, address)))
}

// NotifyProxyRecovered DMs every admin that a monitored proxy recovered.
// Implements xraychecker.Notifier.
func (s *Service) NotifyProxyRecovered(ctx context.Context, name, protocol, address string) {
	s.broadcastProxyAlert(ctx, fmt.Sprintf(i18n.T("🟢 Прокси снова доступен: %s"), proxyLabel(name, protocol, address)))
}

func (s *Service) broadcastProxyAlert(ctx context.Context, text string) {
	if s.dryRun {
		s.logger.Info("xray checker: alert suppressed (dry run)", "text", text)
		return
	}
	for _, adminID := range s.adminIDs {
		if err := s.bot.SendPlain(ctx, adminID, text); err != nil {
			s.logger.Error("xray checker: notify admin failed", "admin_id", adminID, "err", err.Error())
		}
	}
}

// proxyLabel renders a human-readable identifier for a proxy: its name, with
// protocol/address in parentheses when they add information.
func proxyLabel(name, protocol, address string) string {
	label := strings.TrimSpace(name)
	if label == "" {
		label = address
	}
	var extra []string
	if protocol != "" {
		extra = append(extra, protocol)
	}
	if address != "" && address != label {
		extra = append(extra, address)
	}
	if len(extra) > 0 {
		return fmt.Sprintf("%s (%s)", label, strings.Join(extra, ", "))
	}
	if label == "" {
		return i18n.T("прокси")
	}
	return label
}

// sendProxyHealth renders the 🩺 proxy-health report in the bot admin menu.
func (s *Service) sendProxyHealth(ctx context.Context, chatID int64) {
	rows := [][]tg.InlineKeyboardButton{}
	if url := s.getCheckerURL(); url != "" {
		rows = append(rows, []tg.InlineKeyboardButton{{Text: i18n.T("🌐 Веб-панель прокси"), URL: url}})
	}
	rows = append(rows,
		[]tg.InlineKeyboardButton{{Text: i18n.T("🔄 Обновить"), CallbackData: "adm:checker"}},
		[]tg.InlineKeyboardButton{{Text: i18n.T("← Меню"), CallbackData: "adm:menu"}},
	)
	backKb := &tg.InlineKeyboardMarkup{InlineKeyboard: rows}

	health, err := s.proxyHealth(ctx)
	if err != nil {
		s.logger.Error("xray checker: proxy health failed", "err", err.Error())
		_, _ = s.bot.SendPlainWithKeyboard(ctx, chatID, i18n.T("Не удалось получить состояние прокси."), backKb)
		return
	}
	if !health.Configured {
		_, _ = s.bot.SendPlainWithKeyboard(ctx, chatID, i18n.T("Мониторинг прокси не настроен."), backKb)
		return
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf(i18n.T("🩺 Состояние прокси\n\nДоступно: %d / Недоступно: %d\n\n"), health.Up, health.Down))
	if len(health.Proxies) == 0 {
		b.WriteString(i18n.T("Прокси не найдены. Проверьте подписку xray-checker."))
	} else {
		for _, p := range health.Proxies {
			mark := "✅"
			detail := fmt.Sprintf(i18n.T("%d мс"), p.LatencyMs)
			if !p.Up {
				mark = "❌"
				detail = i18n.T("нет связи")
			}
			b.WriteString(fmt.Sprintf("%s %s — %s\n", mark, proxyLabel(p.Name, "", p.Address), detail))
		}
	}
	_, _ = s.bot.SendPlainWithKeyboard(ctx, chatID, strings.TrimRight(b.String(), "\n"), backKb)
}
