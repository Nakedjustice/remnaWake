package payments

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/Nakedjustice/remnaWake/internal/i18n"
	"github.com/Nakedjustice/remnaWake/internal/store"
	tg "github.com/Nakedjustice/remnaWake/internal/telegram"
)

// pendingDrilldownButton maps an action-center item to the inline button that
// opens the matching pending list in chat. Only the four categories the bot
// can resolve inline get a button; everything else stays a text line.
func pendingDrilldownButton(itemID string, count int) *tg.InlineKeyboardButton {
	switch itemID {
	case "pending_payments":
		return &tg.InlineKeyboardButton{Text: fmt.Sprintf(i18n.T("💳 Оплаты (%d)"), count), CallbackData: "adm:pd:pay"}
	case "pending_gifts":
		return &tg.InlineKeyboardButton{Text: fmt.Sprintf(i18n.T("🎁 Подарки (%d)"), count), CallbackData: "adm:pd:gift"}
	case "pending_invites":
		return &tg.InlineKeyboardButton{Text: fmt.Sprintf(i18n.T("👥 Приглашения (%d)"), count), CallbackData: "adm:pd:inv"}
	case "pending_trials":
		return &tg.InlineKeyboardButton{Text: fmt.Sprintf(i18n.T("🧪 Пробные периоды (%d)"), count), CallbackData: "adm:pd:tr"}
	}
	return nil
}

func severityEmoji(severity string) string {
	switch severity {
	case actionSeverityCritical:
		return "🔴"
	case actionSeverityWarning:
		return "🟡"
	default:
		return "ℹ️"
	}
}

// sendPendingSummary renders the admin action center as a chat message:
// severity-tagged counts plus buttons that open the per-category pending
// lists. This is the recovery entry point when the original admin DM buttons
// are gone (the message refs are in-memory and lost on restart).
func (s *Service) sendPendingSummary(ctx context.Context, chatID int64) {
	ac, err := s.AdminActionCenter(ctx, chatID)
	if err != nil {
		s.logger.Error("pending summary failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Не удалось загрузить центр действий."))
		return
	}

	var b strings.Builder
	var rows [][]tg.InlineKeyboardButton
	if len(ac.Items) == 0 {
		b.WriteString(i18n.T("✅ Всё спокойно — заявок и предупреждений нет."))
	} else {
		b.WriteString(i18n.T("🚨 Центр действий"))
		b.WriteString("\n")
		for _, item := range ac.Items {
			// Titles are shared with the Mini App payload, so they reach i18n.T
			// as data; the en dictionary carries them explicitly.
			b.WriteString(fmt.Sprintf("\n%s %s — %d", severityEmoji(item.Severity), i18n.T(item.Title), item.Count))
			if btn := pendingDrilldownButton(item.ID, item.Count); btn != nil {
				rows = append(rows, []tg.InlineKeyboardButton{*btn})
			}
		}
	}
	degraded := false
	for _, src := range ac.Sources {
		if src.Status != actionSourceDegraded {
			continue
		}
		if !degraded {
			b.WriteString("\n")
			degraded = true
		}
		b.WriteString("\n" + fmt.Sprintf(i18n.T("⚠️ Источник %s недоступен"), src.Name))
	}

	if len(rows) == 0 {
		_ = s.bot.SendPlain(ctx, chatID, b.String())
		return
	}
	_, _ = s.bot.SendPlainWithKeyboard(ctx, chatID, b.String(), &tg.InlineKeyboardMarkup{InlineKeyboard: rows})
}

// pendingListLimit caps a category list so the message and its keyboard stay
// well under Telegram's limits; the overflow note points at the remainder.
const pendingListLimit = 10

// pendingEntry is one resolvable request in a category list. The callback
// data reuses the exact prefixes of the original admin DM buttons, so the
// existing handlers (and their idempotency guarantees) apply unchanged.
type pendingEntry struct {
	id          int64
	line        string
	okCB, rejCB string
}

// sendPendingCategory sends the pending list for one action-center category
// ("pay", "gift", "inv", "tr") with per-item confirm/reject buttons.
func (s *Service) sendPendingCategory(ctx context.Context, chatID int64, category string) {
	header, entries, err := s.pendingEntries(ctx, category)
	if err != nil {
		s.logger.Error("pending category failed", "category", category, "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Ошибка, попробуйте позже."))
		return
	}
	if len(entries) == 0 {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Нет ожидающих заявок."))
		return
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].id < entries[j].id })
	overflow := 0
	if len(entries) > pendingListLimit {
		overflow = len(entries) - pendingListLimit
		entries = entries[:pendingListLimit]
	}

	var b strings.Builder
	b.WriteString(header)
	b.WriteString("\n")
	var rows [][]tg.InlineKeyboardButton
	for _, e := range entries {
		b.WriteString("\n" + e.line)
		rows = append(rows, []tg.InlineKeyboardButton{
			{Text: fmt.Sprintf("✅ №%d", e.id), CallbackData: e.okCB},
			{Text: fmt.Sprintf("❌ №%d", e.id), CallbackData: e.rejCB},
		})
	}
	if overflow > 0 {
		b.WriteString("\n\n" + fmt.Sprintf(i18n.T("…и ещё %d — решите эти, чтобы увидеть остальные."), overflow))
	}
	_, _ = s.bot.SendPlainWithKeyboard(ctx, chatID, b.String(), &tg.InlineKeyboardMarkup{InlineKeyboard: rows})
}

func (s *Service) pendingEntries(ctx context.Context, category string) (string, []pendingEntry, error) {
	switch category {
	case "pay":
		requests, err := s.store.ListPaymentRequestsByStatus(ctx, "pending")
		if err != nil {
			return "", nil, err
		}
		entries := make([]pendingEntry, 0, len(requests))
		for _, r := range requests {
			line := fmt.Sprintf(i18n.T("№%d · %s · %d мес. · %s"), r.ID, r.Username, r.Months, s.priceLabel(r.Price))
			if r.Kind == store.PaymentKindTrafficExtension {
				line = fmt.Sprintf(i18n.T("№%d · %s · %d ГБ · %s"), r.ID, r.Username, r.TrafficGB, s.priceLabel(r.Price))
			}
			entries = append(entries, pendingEntry{
				id: r.ID, line: line,
				okCB: fmt.Sprintf("ok:%d", r.ID), rejCB: fmt.Sprintf("rej:%d", r.ID),
			})
		}
		return i18n.T("💳 Ожидающие заявки на оплату"), entries, nil
	case "gift":
		gifts, err := s.store.ListGiftCodesByStatus(ctx, "pending")
		if err != nil {
			return "", nil, err
		}
		entries := make([]pendingEntry, 0, len(gifts))
		for _, g := range gifts {
			buyer := g.BuyerUsername
			if buyer == "" {
				buyer = fmt.Sprintf("tg:%d", g.BuyerTelegramID)
			}
			entries = append(entries, pendingEntry{
				id:   g.ID,
				line: fmt.Sprintf(i18n.T("№%d · %s · %d мес. · %s"), g.ID, buyer, g.Months, s.priceLabel(g.Price)),
				okCB: fmt.Sprintf("gc_ok:%d", g.ID), rejCB: fmt.Sprintf("gc_rej:%d", g.ID),
			})
		}
		return i18n.T("🎁 Ожидающие заявки на подарки"), entries, nil
	case "inv":
		invites, err := s.store.ListInviteRequestsByStatus(ctx, "pending")
		if err != nil {
			return "", nil, err
		}
		entries := make([]pendingEntry, 0, len(invites))
		for _, r := range invites {
			entries = append(entries, pendingEntry{
				id:   r.ID,
				line: fmt.Sprintf(i18n.T("№%d · %s → %s · %d мес. · %s"), r.ID, r.InviterUsername, r.NewUsername, r.Months, s.priceLabel(r.Price)),
				okCB: fmt.Sprintf("inv_ok:%d", r.ID), rejCB: fmt.Sprintf("inv_rej:%d", r.ID),
			})
		}
		return i18n.T("👥 Ожидающие приглашения"), entries, nil
	case "tr":
		trials, err := s.store.ListTrialRequestsByStatus(ctx, "pending")
		if err != nil {
			return "", nil, err
		}
		entries := make([]pendingEntry, 0, len(trials))
		for _, r := range trials {
			entries = append(entries, pendingEntry{
				id:   r.ID,
				line: fmt.Sprintf("№%d · %s", r.ID, r.Username),
				okCB: fmt.Sprintf("tr_ok:%d", r.ID), rejCB: fmt.Sprintf("tr_rej:%d", r.ID),
			})
		}
		return i18n.T("🧪 Ожидающие заявки на пробный период"), entries, nil
	}
	return "", nil, fmt.Errorf("unknown pending category %q", category)
}
