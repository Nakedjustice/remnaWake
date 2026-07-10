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

	actionGroupNeedsAction = "needs_action"
	actionGroupIncident    = "incident"
	actionGroupOpportunity = "opportunity"

	actionSourceOK       = "ok"
	actionSourceDisabled = "disabled"
	actionSourceDegraded = "degraded"

	paymentActionStaleAfter = 2 * time.Hour
	adminActionStaleAfter   = 24 * time.Hour
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
	Total         int `json:"total"`
	Affected      int `json:"affected"`
	Critical      int `json:"critical"`
	Warning       int `json:"warning"`
	Info          int `json:"info"`
	NeedsAction   int `json:"needs_action"`
	Incidents     int `json:"incidents"`
	Opportunities int `json:"opportunities"`
}

type WebAdminActionItem struct {
	ID       string            `json:"id"`
	Group    string            `json:"group"`
	Category string            `json:"category"`
	Severity string            `json:"severity"`
	Title    string            `json:"title"`
	Detail   string            `json:"detail"`
	Count    int               `json:"count"`
	Target   string            `json:"target"`
	Filter   map[string]string `json:"filter,omitempty"`
	OldestAt string            `json:"oldest_at,omitempty"`
	DueAt    string            `json:"due_at,omitempty"`
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
	out.addTimedCountItem(len(requests), oldestCreatedAt(requests, func(item store.PaymentRequest) time.Time { return item.CreatedAt }), paymentActionStaleAfter, now, WebAdminActionItem{
		ID:       "pending_payments",
		Group:    actionGroupNeedsAction,
		Category: "payments",
		Title:    "Заявки на оплату ждут решения",
		Detail:   "Откройте список заявок на оплату.",
		Target:   "payment_requests",
	})

	gifts, err := s.store.ListGiftCodesByStatus(ctx, "pending")
	if err != nil {
		return fmt.Errorf("list pending gift codes: %w", err)
	}
	out.addTimedCountItem(len(gifts), oldestCreatedAt(gifts, func(item store.GiftCode) time.Time { return item.CreatedAt }), adminActionStaleAfter, now, WebAdminActionItem{
		ID:       "pending_gifts",
		Group:    actionGroupNeedsAction,
		Category: "gifts",
		Title:    "Заявки на подарки ждут решения",
		Detail:   "Откройте список заявок на подарки.",
		Target:   "gift_requests",
	})

	invites, err := s.store.ListInviteRequestsByStatus(ctx, "pending")
	if err != nil {
		return fmt.Errorf("list pending invite requests: %w", err)
	}
	out.addTimedCountItem(len(invites), oldestCreatedAt(invites, func(item store.InviteRequest) time.Time { return item.CreatedAt }), adminActionStaleAfter, now, WebAdminActionItem{
		ID:       "pending_invites",
		Group:    actionGroupNeedsAction,
		Category: "invites",
		Title:    "Приглашения ждут одобрения",
		Detail:   "Откройте список заявок на приглашения.",
		Target:   "invite_requests",
	})

	trials, err := s.store.ListTrialRequestsByStatus(ctx, "pending")
	if err != nil {
		return fmt.Errorf("list pending trial requests: %w", err)
	}
	out.addTimedCountItem(len(trials), oldestCreatedAt(trials, func(item store.TrialRequest) time.Time { return item.CreatedAt }), adminActionStaleAfter, now, WebAdminActionItem{
		ID:       "pending_trials",
		Group:    actionGroupNeedsAction,
		Category: "trials",
		Title:    "Пробные периоды ждут одобрения",
		Detail:   "Откройте список заявок на пробный период.",
		Target:   "trial_requests",
	})

	support, err := s.store.ReadAdminSupportAttention(ctx)
	if err != nil {
		return fmt.Errorf("read unread support: %w", err)
	}
	out.addTimedCountItem(support.Tickets, support.OldestUnreadAt, adminActionStaleAfter, now, WebAdminActionItem{
		ID:       "unread_support",
		Group:    actionGroupNeedsAction,
		Category: "support",
		Title:    "Есть непрочитанные обращения в поддержку",
		Detail:   "Откройте чат поддержки и ответьте пользователям.",
		Target:   "support",
	})

	blocked, err := s.store.ListBlockedRegistrationGuards(ctx)
	if err != nil {
		return fmt.Errorf("list blocked registrations: %w", err)
	}
	out.addCountItem(len(blocked), WebAdminActionItem{
		ID:       "blocked_registrations",
		Group:    actionGroupNeedsAction,
		Category: "registration_guard",
		Severity: actionSeverityWarning,
		Title:    "Есть заблокированные регистрации",
		Detail:   "Проверьте блокировки и снимите ложные срабатывания.",
		Target:   "registration_guard",
		OldestAt: formatActionTime(oldestCreatedAt(blocked, func(item store.RegistrationGuard) time.Time { return item.CreatedAt })),
	})

	rejectedOnline, err := s.rejectedOnlineProviderPayments(ctx, now.Add(-7*24*time.Hour))
	if err != nil {
		return fmt.Errorf("read provider payment failures: %w", err)
	}
	out.addCountItem(rejectedOnline, WebAdminActionItem{
		ID:       "provider_rejections",
		Group:    actionGroupNeedsAction,
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
		out.addCountItem(1, WebAdminActionItem{
			ID: "remnawave_unavailable", Group: actionGroupIncident, Category: "users",
			Severity: actionSeverityCritical, Title: "Не удалось загрузить данные Remnawave",
			Detail: "Панель временно недоступна, данные пользователей могут быть неполными.", Target: "action_center_refresh",
		})
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
		Group:    actionGroupOpportunity,
		Category: "users",
		Severity: actionSeverityWarning,
		Title:    "Подписки скоро истекают",
		Detail:   "Откройте пользователей, истекающих в ближайшие 7 дней.",
		Target:   "users",
		Filter:   map[string]string{"cohort": "expiring_soon"},
	})
	out.addCountItem(lowTraffic, WebAdminActionItem{
		ID:       "low_traffic_users",
		Group:    actionGroupOpportunity,
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
		out.addCountItem(1, WebAdminActionItem{
			ID: "xray_checker_unavailable", Group: actionGroupIncident, Category: "proxy",
			Severity: actionSeverityCritical, Title: "Не удалось проверить состояние прокси",
			Detail: "Источник мониторинга временно недоступен.", Target: "action_center_refresh",
		})
		return
	}
	if health == nil || !health.Configured {
		out.addSource("xray_checker", actionSourceDisabled, "")
		return
	}
	out.addSource("xray_checker", actionSourceOK, "")
	out.addCountItem(health.Down, WebAdminActionItem{
		ID:       "proxy_down",
		Group:    actionGroupIncident,
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
	var overdueDueAt, soonDueAt time.Time
	for i := range servers {
		if servers[i].NextDueAt.IsZero() {
			continue
		}
		switch infraDueStatus(servers[i].NextDueAt, now) {
		case "overdue":
			overdue++
			overdueDueAt = earliestActionTime(overdueDueAt, servers[i].NextDueAt)
		case "soon":
			soon++
			soonDueAt = earliestActionTime(soonDueAt, servers[i].NextDueAt)
		}
	}
	out.addCountItem(overdue, WebAdminActionItem{
		ID:       "infra_overdue",
		Group:    actionGroupIncident,
		Category: "infra",
		Severity: actionSeverityCritical,
		Title:    "Просрочены оплаты серверов",
		Detail:   "Откройте инфраструктуру.",
		Target:   "infra",
		Filter:   map[string]string{"due_status": "overdue"},
		DueAt:    formatActionTime(overdueDueAt),
	})
	out.addCountItem(soon, WebAdminActionItem{
		ID:       "infra_due_soon",
		Group:    actionGroupNeedsAction,
		Category: "infra",
		Severity: actionSeverityWarning,
		Title:    "Скоро нужно оплатить серверы",
		Detail:   "Откройте инфраструктуру.",
		Target:   "infra",
		Filter:   map[string]string{"due_status": "soon"},
		DueAt:    formatActionTime(soonDueAt),
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
		out.addCountItem(1, WebAdminActionItem{
			ID: "autoupdate_unavailable", Group: actionGroupIncident, Category: "updates",
			Severity: actionSeverityWarning, Title: "Не удалось прочитать состояние обновлений",
			Detail: "Сохранённое состояние проверки временно недоступно.", Target: "action_center_refresh",
		})
		return
	}
	out.addSource("autoupdate", actionSourceOK, "")
	switch snapshot.Status {
	case autoupdate.CheckStatusUpdateAvailable, autoupdate.CheckStatusAlreadyNotified:
		out.addCountItem(1, WebAdminActionItem{
			ID:       "update_available",
			Group:    actionGroupNeedsAction,
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

func (out *WebAdminActionCenter) addTimedCountItem(count int, oldest time.Time, threshold time.Duration, now time.Time, item WebAdminActionItem) {
	item.Severity = actionSeverityForAge(now, oldest, threshold)
	item.OldestAt = formatActionTime(oldest)
	out.addCountItem(count, item)
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
		left, right := out.Items[i], out.Items[j]
		if actionGroupRank(left.Group) != actionGroupRank(right.Group) {
			return actionGroupRank(left.Group) < actionGroupRank(right.Group)
		}
		if actionSeverityRank(left.Severity) != actionSeverityRank(right.Severity) {
			return actionSeverityRank(left.Severity) < actionSeverityRank(right.Severity)
		}
		leftAt, rightAt := actionItemTime(left), actionItemTime(right)
		if leftAt != "" && rightAt != "" && leftAt != rightAt {
			return leftAt < rightAt
		}
		return false
	})
	for _, item := range out.Items {
		out.Summary.Total++
		out.Summary.Affected += item.Count
		switch item.Severity {
		case actionSeverityCritical:
			out.Summary.Critical++
		case actionSeverityWarning:
			out.Summary.Warning++
		default:
			out.Summary.Info++
		}
		switch item.Group {
		case actionGroupIncident:
			out.Summary.Incidents++
		case actionGroupOpportunity:
			out.Summary.Opportunities++
		default:
			out.Summary.NeedsAction++
		}
	}
}

func oldestCreatedAt[T any](items []T, createdAt func(T) time.Time) time.Time {
	var oldest time.Time
	for _, item := range items {
		at := createdAt(item)
		if at.IsZero() {
			continue
		}
		if oldest.IsZero() || at.Before(oldest) {
			oldest = at
		}
	}
	return oldest
}

func earliestActionTime(current, candidate time.Time) time.Time {
	if candidate.IsZero() || (!current.IsZero() && !candidate.Before(current)) {
		return current
	}
	return candidate
}

func actionSeverityForAge(now, oldest time.Time, threshold time.Duration) string {
	if !oldest.IsZero() && !now.Before(oldest.Add(threshold)) {
		return actionSeverityCritical
	}
	return actionSeverityWarning
}

func formatActionTime(at time.Time) string {
	if at.IsZero() {
		return ""
	}
	return at.UTC().Format(time.RFC3339)
}

func actionItemTime(item WebAdminActionItem) string {
	if item.DueAt != "" {
		return item.DueAt
	}
	return item.OldestAt
}

func actionGroupRank(group string) int {
	switch group {
	case actionGroupIncident:
		return 1
	case actionGroupOpportunity:
		return 2
	default:
		return 0
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
