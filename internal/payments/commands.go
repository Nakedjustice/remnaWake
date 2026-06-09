package payments

import (
	"context"
	"fmt"
	"strconv"
	"strings"

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
	_ = s.bot.SendPlain(ctx, chatID, "Отправьте текст реквизитов следующим сообщением.")
}

func (s *Service) cmdShowRequisites(ctx context.Context, chatID int64) {
	s.mu.Lock()
	req := s.requisites
	s.mu.Unlock()
	if req == "" {
		_ = s.bot.SendPlain(ctx, chatID, "Реквизиты не заданы.")
		return
	}
	_ = s.bot.SendPlain(ctx, chatID, "Реквизиты для оплаты:\n\n"+req)
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
	}
	return false
}

func (s *Service) consumeRequisitesText(ctx context.Context, chatID int64, text string) bool {
	if err := s.store.UpsertSetting(ctx, requisitesKey, text); err != nil {
		s.logger.Error("save requisites failed", "err", err.Error())
		s.mu.Lock()
		delete(s.adminInput, chatID)
		s.mu.Unlock()
		_ = s.bot.SendPlain(ctx, chatID, "Ошибка сохранения реквизитов.")
		return true
	}
	s.mu.Lock()
	s.requisites = text
	delete(s.adminInput, chatID)
	s.mu.Unlock()
	_ = s.bot.SendPlain(ctx, chatID, "Реквизиты сохранены.")
	return true
}

func (s *Service) consumeTariffMonths(ctx context.Context, chatID int64, text string) bool {
	months, err := strconv.Atoi(text)
	if err != nil || months < 1 {
		_ = s.bot.SendPlain(ctx, chatID, "Введите целое число ≥ 1. Пример: 3")
		return true
	}
	s.mu.Lock()
	state := s.adminInput[chatID]
	state.pendingMonths = months
	state.step = adminInputTariffPrice
	s.adminInput[chatID] = state
	s.mu.Unlock()
	_ = s.bot.SendPlain(ctx, chatID, "Введите цену (целое ≥ 0):")
	return true
}

func (s *Service) consumeTariffPrice(ctx context.Context, chatID int64, text string) bool {
	price, err := strconv.Atoi(text)
	if err != nil || price < 0 {
		_ = s.bot.SendPlain(ctx, chatID, "Введите целое число ≥ 0. Пример: 500")
		return true
	}
	s.mu.Lock()
	months := s.adminInput[chatID].pendingMonths
	delete(s.adminInput, chatID)
	s.mu.Unlock()
	if err := s.store.UpsertTariff(ctx, months, price); err != nil {
		s.logger.Error("upsert tariff failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, "Ошибка сохранения тарифа.")
		return true
	}
	_ = s.bot.SendPlain(ctx, chatID, fmt.Sprintf("Тариф сохранён: %d мес. — %s", months, s.priceLabel(price)))
	return true
}

func (s *Service) cmdListTariffs(ctx context.Context, chatID int64) {
	tariffs, err := s.store.ListTariffs(ctx)
	if err != nil {
		s.logger.Error("list tariffs failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, "Ошибка чтения тарифов.")
		return
	}
	if len(tariffs) == 0 {
		_ = s.bot.SendPlain(ctx, chatID, "Тарифы не заданы. Добавьте: /settariff <месяцев> <цена>")
		return
	}
	var b strings.Builder
	b.WriteString("Тарифы:\n")
	for _, t := range tariffs {
		b.WriteString(fmt.Sprintf("%d мес. — %s\n", t.Months, s.priceLabel(t.Price)))
	}
	_ = s.bot.SendPlain(ctx, chatID, strings.TrimRight(b.String(), "\n"))
}

func (s *Service) cmdSetTariff(ctx context.Context, chatID int64, fields []string) {
	if len(fields) != 3 {
		_ = s.bot.SendPlain(ctx, chatID, "Использование: /settariff <месяцев> <цена>")
		return
	}
	months, err1 := strconv.Atoi(fields[1])
	price, err2 := strconv.Atoi(fields[2])
	if err1 != nil || err2 != nil || months < 1 || price < 0 {
		_ = s.bot.SendPlain(ctx, chatID, "Месяцев — целое ≥ 1, цена — целое ≥ 0. Пример: /settariff 3 450")
		return
	}
	if err := s.store.UpsertTariff(ctx, months, price); err != nil {
		s.logger.Error("upsert tariff failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, "Ошибка сохранения тарифа.")
		return
	}
	_ = s.bot.SendPlain(ctx, chatID, fmt.Sprintf("Тариф сохранён: %d мес. — %s", months, s.priceLabel(price)))
}

func (s *Service) SendAdminMenu(ctx context.Context, chatID int64) {
	kb := &tg.InlineKeyboardMarkup{
		InlineKeyboard: [][]tg.InlineKeyboardButton{
			{{Text: "📋 Посмотреть тарифы", CallbackData: "adm:tariffs"}},
			{{Text: "➕ Добавить тариф", CallbackData: "adm:addtariff"}},
			{{Text: "❌ Удалить тариф", CallbackData: "adm:del_list"}},
			{{Text: "💳 Посмотреть реквизиты", CallbackData: "adm:req"}},
			{{Text: "✏️ Изменить реквизиты", CallbackData: "adm:setreq"}},
		},
	}
	_, _ = s.bot.SendPlainWithKeyboard(ctx, chatID, "Меню администратора", kb)
}

func (s *Service) cmdDelTariff(ctx context.Context, chatID int64, fields []string) {
	if len(fields) != 2 {
		_ = s.bot.SendPlain(ctx, chatID, "Использование: /deltariff <месяцев>")
		return
	}
	months, err := strconv.Atoi(fields[1])
	if err != nil || months < 1 {
		_ = s.bot.SendPlain(ctx, chatID, "Месяцев — целое ≥ 1. Пример: /deltariff 3")
		return
	}
	deleted, err := s.store.DeleteTariff(ctx, months)
	if err != nil {
		s.logger.Error("delete tariff failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, "Ошибка удаления тарифа.")
		return
	}
	if !deleted {
		_ = s.bot.SendPlain(ctx, chatID, fmt.Sprintf("Тариф на %d мес. не найден.", months))
		return
	}
	_ = s.bot.SendPlain(ctx, chatID, fmt.Sprintf("Тариф на %d мес. удалён.", months))
}
