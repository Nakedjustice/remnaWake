package payments

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/Nakedjustice/remnaWake/internal/i18n"
	tg "github.com/Nakedjustice/remnaWake/internal/telegram"
)

func (s *Service) HandleAdminCommand(ctx context.Context, m *tg.Message) bool {
	if m == nil || !s.isEnabled() || !s.isAdmin(m.Chat.ID) {
		return false
	}
	chatID := m.Chat.ID
	if s.consumeAdminInput(ctx, m) {
		return true
	}
	fields := strings.Fields(m.Text)
	if len(fields) == 0 {
		return false
	}
	switch fields[0] {
	case "/admin":
		s.SendAdminMenu(ctx, chatID)
		return true
	case "/tariffs":
		s.cmdListTariffs(ctx, chatID)
		return true
	case "/settariff":
		s.cmdSetTariff(ctx, chatID, fields)
		return true
	case "/deltariff":
		s.cmdDelTariff(ctx, chatID, fields)
		return true
	case "/setrequisites":
		s.cmdSetRequisites(ctx, chatID)
		return true
	case "/requisites":
		s.cmdShowRequisites(ctx, chatID)
		return true
	case "/stats":
		s.sendAdminStats(ctx, chatID)
		return true
	default:
		return false
	}
}

func (s *Service) cmdSetRequisites(ctx context.Context, chatID int64) {
	s.mu.Lock()
	state := s.adminInput[chatID]
	state.step = adminInputRequisites
	s.adminInput[chatID] = state
	s.mu.Unlock()
	_ = s.bot.SendPlain(ctx, chatID, i18n.T("Отправьте текст реквизитов следующим сообщением."))
}

func (s *Service) cmdShowRequisites(ctx context.Context, chatID int64) {
	s.mu.Lock()
	req := s.requisites
	s.mu.Unlock()
	if req == "" {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Реквизиты не заданы."))
		return
	}
	_ = s.bot.SendPlain(ctx, chatID, i18n.T("Реквизиты для оплаты:\n\n")+req)
}

func (s *Service) consumeAdminInput(ctx context.Context, m *tg.Message) bool {
	chatID := m.Chat.ID
	s.mu.Lock()
	state := s.adminInput[chatID]
	s.mu.Unlock()
	if state.step == adminInputNone {
		return false
	}
	text := strings.TrimSpace(m.Text)
	if text == "" || strings.HasPrefix(text, "/") {
		return false
	}
	switch state.step {
	case adminInputRequisites:
		return s.consumeRequisitesText(ctx, chatID, text)
	case adminInputTariffMonths:
		return s.consumeTariffMonths(ctx, chatID, text)
	case adminInputTariffPrice:
		return s.consumeTariffPrice(ctx, chatID, text)
	case adminInputBroadcast:
		return s.consumeBroadcastText(ctx, chatID, text)
	case adminInputUserLookup:
		return s.consumeUserLookup(ctx, chatID, text)
	case adminInputUserHwid:
		return s.consumeUserHwid(ctx, chatID, text)
	case adminInputUserTraffic:
		return s.consumeUserTraffic(ctx, chatID, text)
	case adminInputUserExpiry:
		return s.consumeUserExpiry(ctx, chatID, text)
	case adminInputTrialDays:
		return s.consumeTrialDays(ctx, chatID, text)
	case adminInputReferralInviter:
		return s.consumeReferralBonus(ctx, chatID, text, true)
	case adminInputReferralInvitee:
		return s.consumeReferralBonus(ctx, chatID, text, false)
	}
	return false
}

// consumeBroadcastText stores the broadcast draft and asks for confirmation.
// The step goes back to adminInputNone so further chat is not intercepted; the
// draft stays in pendingBroadcast until the admin presses a confirm button.
func (s *Service) consumeBroadcastText(ctx context.Context, chatID int64, text string) bool {
	s.mu.Lock()
	state := s.adminInput[chatID]
	state.step = adminInputNone
	state.pendingBroadcast = text
	s.adminInput[chatID] = state
	s.mu.Unlock()
	kb := &tg.InlineKeyboardMarkup{
		InlineKeyboard: [][]tg.InlineKeyboardButton{{
			{Text: i18n.T("✅ Отправить"), CallbackData: "adm:bc_send"},
			{Text: i18n.T("❌ Отмена"), CallbackData: "adm:bc_cancel"},
		}},
	}
	_, _ = s.bot.SendPlainWithKeyboard(ctx, chatID,
		i18n.T("Текст рассылки:\n\n")+text+i18n.T("\n\nОтправить всем пользователям?"), kb)
	return true
}

func (s *Service) consumeRequisitesText(ctx context.Context, chatID int64, text string) bool {
	if err := s.store.UpsertSetting(ctx, requisitesKey, text); err != nil {
		s.logger.Error("save requisites failed", "err", err.Error())
		s.mu.Lock()
		delete(s.adminInput, chatID)
		s.mu.Unlock()
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Ошибка сохранения реквизитов."))
		return true
	}
	s.mu.Lock()
	s.requisites = text
	delete(s.adminInput, chatID)
	s.mu.Unlock()
	_ = s.bot.SendPlain(ctx, chatID, i18n.T("Реквизиты сохранены."))
	return true
}

func (s *Service) consumeTariffMonths(ctx context.Context, chatID int64, text string) bool {
	months, err := strconv.Atoi(text)
	if err != nil || months < 1 {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Введите целое число ≥ 1. Пример: 3"))
		return true
	}
	s.mu.Lock()
	state := s.adminInput[chatID]
	state.pendingMonths = months
	state.step = adminInputTariffPrice
	s.adminInput[chatID] = state
	s.mu.Unlock()
	_ = s.bot.SendPlain(ctx, chatID, i18n.T("Введите цену (целое ≥ 0):"))
	return true
}

func (s *Service) consumeTariffPrice(ctx context.Context, chatID int64, text string) bool {
	price, err := strconv.Atoi(text)
	if err != nil || price < 0 {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Введите целое число ≥ 0. Пример: 500"))
		return true
	}
	s.mu.Lock()
	months := s.adminInput[chatID].pendingMonths
	delete(s.adminInput, chatID)
	s.mu.Unlock()
	if err := s.store.UpsertTariff(ctx, months, price); err != nil {
		s.logger.Error("upsert tariff failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Ошибка сохранения тарифа."))
		return true
	}
	_ = s.bot.SendPlain(ctx, chatID, fmt.Sprintf(i18n.T("Тариф сохранён: %d мес. — %s"), months, s.priceLabel(price)))
	return true
}

func (s *Service) cmdListTariffs(ctx context.Context, chatID int64) {
	tariffs, err := s.store.ListTariffs(ctx)
	if err != nil {
		s.logger.Error("list tariffs failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Ошибка чтения тарифов."))
		return
	}
	if len(tariffs) == 0 {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Тарифы не заданы. Добавьте: /settariff <месяцев> <цена>"))
		return
	}
	var b strings.Builder
	b.WriteString(i18n.T("Тарифы:\n"))
	for _, t := range tariffs {
		b.WriteString(fmt.Sprintf(i18n.T("%d мес. — %s\n"), t.Months, s.priceLabel(t.Price)))
	}
	_ = s.bot.SendPlain(ctx, chatID, strings.TrimRight(b.String(), "\n"))
}

func (s *Service) cmdSetTariff(ctx context.Context, chatID int64, fields []string) {
	if len(fields) != 3 {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Использование: /settariff <месяцев> <цена>"))
		return
	}
	months, err1 := strconv.Atoi(fields[1])
	price, err2 := strconv.Atoi(fields[2])
	if err1 != nil || err2 != nil || months < 1 || price < 0 {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Месяцев — целое ≥ 1, цена — целое ≥ 0. Пример: /settariff 3 450"))
		return
	}
	if err := s.store.UpsertTariff(ctx, months, price); err != nil {
		s.logger.Error("upsert tariff failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Ошибка сохранения тарифа."))
		return
	}
	_ = s.bot.SendPlain(ctx, chatID, fmt.Sprintf(i18n.T("Тариф сохранён: %d мес. — %s"), months, s.priceLabel(price)))
}

func (s *Service) SendAdminMenu(ctx context.Context, chatID int64) {
	shotLabel := i18n.T("📸 Чек об оплате: выкл")
	if s.getRequireScreenshot() {
		shotLabel = i18n.T("📸 Чек об оплате: вкл")
	}
	rows := [][]tg.InlineKeyboardButton{
		{{Text: i18n.T("📊 Статистика"), CallbackData: "adm:stats"}},
		{{Text: i18n.T("📋 Посмотреть тарифы"), CallbackData: "adm:tariffs"}},
		{{Text: i18n.T("➕ Добавить тариф"), CallbackData: "adm:addtariff"}},
		{{Text: i18n.T("❌ Удалить тариф"), CallbackData: "adm:del_list"}},
		{{Text: i18n.T("💳 Посмотреть реквизиты"), CallbackData: "adm:req"}},
		{{Text: i18n.T("🎁 Подарочные коды"), CallbackData: "adm:gifts"}},
		{{Text: i18n.T("✏️ Изменить реквизиты"), CallbackData: "adm:setreq"}},
		{{Text: shotLabel, CallbackData: "adm:shot_toggle"}},
		{{Text: i18n.T("🛡 Сквад по умолчанию"), CallbackData: "adm:squad"}},
		{{Text: i18n.T("👤 Управление пользователем"), CallbackData: "adm:user"}},
		{{Text: i18n.T("🔄 Сброс трафика (новые)"), CallbackData: "adm:treset"}},
		{{Text: i18n.T("🎁 Пробный период"), CallbackData: "adm:trial"}},
		{{Text: i18n.T("🎉 Реферальная программа"), CallbackData: "adm:referral"}},
		{{Text: i18n.T("📢 Рассылка всем"), CallbackData: "adm:bcast"}},
	}
	// The payment-provider picker is only meaningful when at least one automatic
	// provider (Platega or Telegram Stars) is configured beyond the default P2P.
	if s.plategaConfigured() || s.starsConfigured() {
		rows = append(rows, []tg.InlineKeyboardButton{
			{Text: i18n.T("💳 Способы оплаты"), CallbackData: "adm:providers"},
		})
	}
	_, _ = s.bot.SendPlainWithKeyboard(ctx, chatID, i18n.T("Меню администратора"),
		&tg.InlineKeyboardMarkup{InlineKeyboard: rows})
}

func (s *Service) cmdDelTariff(ctx context.Context, chatID int64, fields []string) {
	if len(fields) != 2 {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Использование: /deltariff <месяцев>"))
		return
	}
	months, err := strconv.Atoi(fields[1])
	if err != nil || months < 1 {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Месяцев — целое ≥ 1. Пример: /deltariff 3"))
		return
	}
	deleted, err := s.store.DeleteTariff(ctx, months)
	if err != nil {
		s.logger.Error("delete tariff failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Ошибка удаления тарифа."))
		return
	}
	if !deleted {
		_ = s.bot.SendPlain(ctx, chatID, fmt.Sprintf(i18n.T("Тариф на %d мес. не найден."), months))
		return
	}
	_ = s.bot.SendPlain(ctx, chatID, fmt.Sprintf(i18n.T("Тариф на %d мес. удалён."), months))
}
