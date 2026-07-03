package payments

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/Nakedjustice/remnaWake/internal/autoupdate"
	"github.com/Nakedjustice/remnaWake/internal/store"
)

const (
	actionSeverityCritical = "critical"
	actionSeverityWarning  = "warning"
	actionSeverityInfo     = "info"

	actionSourceOK       = "ok"
	actionSourceDisabled = "disabled"
	actionSourceDegraded = "degraded"
)

// WebAdminActionCenter is the read-only triage snapshot for the Mini App admin
// hub. It deliberately contains navigation targets, not mutating actions.
type WebAdminActionCenter struct {
	GeneratedAt string                 `json:"generated_at"`
	Summary     WebAdminActionSummary  `json:"summary"`
	Items       []WebAdminActionItem   `json:"items"`
	Sources     []WebAdminActionSource `json:"sources"`
}

type WebAdminActionSummary struct {
	Total    int `json:"total"`
	Critical int `json:"critical"`
	Warning  int `json:"warning"`
	Info     int `json:"info"`
}

type WebAdminActionItem struct {
	ID       string            `json:"id"`
	Category string            `json:"category"`
	Severity string            `json:"severity"`
	Title    string            `json:"title"`
	Detail   string            `json:"detail"`
	Count    int               `json:"count"`
	Target   string            `json:"target"`
	Filter   map[string]string `json:"filter,omitempty"`
}

type WebAdminActionSource struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// AdminActionCenter returns the current read-only admin triage state. Local
// SQLite failures fail the request; optional external sources degrade in place.
func (s *Service) AdminActionCenter(ctx context.Context, telegramID int64) (*WebAdminActionCenter, error) {
	if err := s.adminGuard(telegramID); err != nil {
		return nil, err
	}

	now := s.now().UTC()
	out := &WebAdminActionCenter{
		GeneratedAt: now.Format(time.RFC3339),
		Items:       []WebAdminActionItem{},
		Sources:     []WebAdminActionSource{},
	}

	if err := s.addLocalActionItems(ctx, out, now); err != nil {
		return nil, err
	}
	out.addSource("local_store", actionSourceOK, "")
	s.addPanelActionItems(ctx, out, now)
	s.addProxyActionItems(ctx, out)
	if err := s.addInfraActionItems(ctx, out); err != nil {
		return nil, err
	}
	s.addUpdateActionItems(ctx, out)
	out.finish()
	return out, nil
}

func (s *Service) addLocalActionItems(ctx context.Context, out *WebAdminActionCenter, now time.Time) error {
	requests, err := s.store.ListPaymentRequestsByStatus(ctx, "pending")
	if err != nil {
		return fmt.Errorf("list pending payment requests: %w", err)
	}
	out.addCountItem(len(requests), WebAdminActionItem{
		ID:       "pending_payments",
		Category: "payments",
		Severity: actionSeverityWarning,
		Title:    "Заявки на оплату ждут решения",
		Detail:   "Откройте список заявок на оплату.",
		Target:   "payment_requests",
	})

	gifts, err := s.store.ListGiftCodesByStatus(ctx, "pending")
	if err != nil {
		return fmt.Errorf("list pending gift codes: %w", err)
	}
	out.addCountItem(len(gifts), WebAdminActionItem{
		ID:       "pending_gifts",
		Category: "gifts",
		Severity: actionSeverityWarning,
		Title:    "Заявки на подарки ждут решения",
		Detail:   "Откройте список заявок на подарки.",
		Target:   "gift_requests",
	})

	invites, err := s.store.ListInviteRequestsByStatus(ctx, "pending")
	if err != nil {
		return fmt.Errorf("list pending invite requests: %w", err)
	}
	out.addCountItem(len(invites), WebAdminActionItem{
		ID:       "pending_invites",
		Category: "invites",
		Severity: actionSeverityWarning,
		Title:    "Приглашения ждут одобрения",
		Detail:   "Откройте список заявок на приглашения.",
		Target:   "invite_requests",
	})

	trials, err := s.store.ListTrialRequestsByStatus(ctx, "pending")
	if err != nil {
		return fmt.Errorf("list pending trial requests: %w", err)
	}
	out.addCountItem(len(trials), WebAdminActionItem{
		ID:       "pending_trials",
		Category: "trials",
		Severity: actionSeverityWarning,
		Title:    "Пробные периоды ждут одобрения",
		Detail:   "Откройте список заявок на пробный период.",
		Target:   "trial_requests",
	})

	rejectedOnline, err := s.rejectedOnlineProviderPayments(ctx, now.Add(-7*24*time.Hour))
	if err != nil {
		return fmt.Errorf("read provider payment failures: %w", err)
	}
	out.addCountItem(rejectedOnline, WebAdminActionItem{
		ID:       "provider_rejections",
		Category: "providers",
		Severity: actionSeverityWarning,
		Title:    "Есть отклонённые онлайн-платежи",
		Detail:   "Проверьте историю платежей за 7 дней.",
		Target:   "payments",
		Filter: map[string]string{
			"days":     "7",
			"status":   "rejected",
			"provider": "all",
			"kind":     "payment",
		},
	})
	return nil
}

func (s *Service) rejectedOnlineProviderPayments(ctx context.Context, since time.Time) (int, error) {
	report, err := s.store.ReadPaymentReport(ctx, store.PaymentReportFilter{
		Since: since, Status: "rejected", Provider: "all", Kind: "payment", Limit: 1,
	})
	if err != nil {
		return 0, err
	}
	total := 0
	for _, p := range report.Analytics.Providers {
		switch p.Provider {
		case ProviderPlatega, ProviderTelegramStars:
			total += p.Rejected
		}
	}
	return total, nil
}

func (s *Service) addPanelActionItems(ctx context.Context, out *WebAdminActionCenter, now time.Time) {
	subs, err := s.finder.ListAll(ctx)
	if err != nil {
		out.addSource("remnawave", actionSourceDegraded, err.Error())
		return
	}
	out.addSource("remnawave", actionSourceOK, "")

	expiringSoon := 0
	lowTraffic := 0
	for i := range subs {
		c := classifyUser(&subs[i], now)
		if c.expiringSoon {
			expiringSoon++
		}
		if c.active && lowTrafficUser(&subs[i]) {
			lowTraffic++
		}
	}
	out.addCountItem(expiringSoon, WebAdminActionItem{
		ID:       "expiring_users",
		Category: "users",
		Severity: actionSeverityWarning,
		Title:    "Подписки скоро истекают",
		Detail:   "Откройте пользователей, истекающих в ближайшие 7 дней.",
		Target:   "users",
		Filter:   map[string]string{"cohort": "expiring_soon"},
	})
	out.addCountItem(lowTraffic, WebAdminActionItem{
		ID:       "low_traffic_users",
		Category: "users",
		Severity: actionSeverityWarning,
		Title:    "Пользователи почти исчерпали трафик",
		Detail:   "Осталось 10% трафика или меньше.",
		Target:   "users",
		Filter:   map[string]string{"cohort": "low_traffic"},
	})
}

func lowTrafficUser(u *Subscriber) bool {
	if u.TrafficLimitBytes <= 0 {
		return false
	}
	remaining := u.TrafficLimitBytes - u.UsedTrafficBytes
	if remaining < 0 {
		remaining = 0
	}
	return float64(remaining)/float64(u.TrafficLimitBytes) <= 0.10
}

func (s *Service) addProxyActionItems(ctx context.Context, out *WebAdminActionCenter) {
	health, err := s.proxyHealth(ctx)
	if err != nil {
		out.addSource("xray_checker", actionSourceDegraded, err.Error())
		return
	}
	if health == nil || !health.Configured {
		out.addSource("xray_checker", actionSourceDisabled, "")
		return
	}
	out.addSource("xray_checker", actionSourceOK, "")
	out.addCountItem(health.Down, WebAdminActionItem{
		ID:       "proxy_down",
		Category: "proxy",
		Severity: actionSeverityCritical,
		Title:    "Прокси недоступны",
		Detail:   "Откройте состояние прокси.",
		Target:   "proxy_health",
	})
}

func (s *Service) addInfraActionItems(ctx context.Context, out *WebAdminActionCenter) error {
	servers, err := s.store.ListInfraServers(ctx)
	if err != nil {
		return fmt.Errorf("list infra servers: %w", err)
	}
	out.addSource("infra", actionSourceOK, "")
	now := s.now()
	overdue := 0
	soon := 0
	for i := range servers {
		if servers[i].NextDueAt.IsZero() {
			continue
		}
		switch infraDueStatus(servers[i].NextDueAt, now) {
		case "overdue":
			overdue++
		case "soon":
			soon++
		}
	}
	out.addCountItem(overdue, WebAdminActionItem{
		ID:       "infra_overdue",
		Category: "infra",
		Severity: actionSeverityCritical,
		Title:    "Просрочены оплаты серверов",
		Detail:   "Откройте инфраструктуру.",
		Target:   "infra",
		Filter:   map[string]string{"due_status": "overdue"},
	})
	out.addCountItem(soon, WebAdminActionItem{
		ID:       "infra_due_soon",
		Category: "infra",
		Severity: actionSeverityWarning,
		Title:    "Скоро нужно оплатить серверы",
		Detail:   "Откройте инфраструктуру.",
		Target:   "infra",
		Filter:   map[string]string{"due_status": "soon"},
	})
	return nil
}

func (s *Service) addUpdateActionItems(ctx context.Context, out *WebAdminActionCenter) {
	checker := s.updateCheckerLocked()
	if checker == nil {
		out.addSource("autoupdate", actionSourceDisabled, "")
		return
	}
	snapshot, err := checker.Snapshot(ctx)
	if err != nil {
		out.addSource("autoupdate", actionSourceDegraded, err.Error())
		return
	}
	out.addSource("autoupdate", actionSourceOK, "")
	switch snapshot.Status {
	case autoupdate.CheckStatusUpdateAvailable, autoupdate.CheckStatusAlreadyNotified:
		out.addCountItem(1, WebAdminActionItem{
			ID:       "update_available",
			Category: "updates",
			Severity: actionSeverityWarning,
			Title:    "Доступно обновление",
			Detail:   "Откройте настройки обновлений.",
			Count:    1,
			Target:   "updates",
		})
	}
}

func (out *WebAdminActionCenter) addCountItem(count int, item WebAdminActionItem) {
	if count <= 0 {
		return
	}
	item.Count = count
	out.Items = append(out.Items, item)
}

func (out *WebAdminActionCenter) addSource(name, status, err string) {
	src := WebAdminActionSource{Name: name, Status: status}
	if err != "" {
		src.Error = err
	}
	out.Sources = append(out.Sources, src)
}

func (out *WebAdminActionCenter) finish() {
	sort.SliceStable(out.Items, func(i, j int) bool {
		return actionSeverityRank(out.Items[i].Severity) < actionSeverityRank(out.Items[j].Severity)
	})
	for _, item := range out.Items {
		out.Summary.Total++
		switch item.Severity {
		case actionSeverityCritical:
			out.Summary.Critical++
		case actionSeverityWarning:
			out.Summary.Warning++
		default:
			out.Summary.Info++
		}
	}
}

func actionSeverityRank(severity string) int {
	switch severity {
	case actionSeverityCritical:
		return 0
	case actionSeverityWarning:
		return 1
	default:
		return 2
	}
}
