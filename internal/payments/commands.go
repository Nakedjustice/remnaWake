package payments

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	tg "github.com/Nakedjustice/remnaWake/internal/telegram"
)

// HandleAdminCommand processes a tariff admin command. Returns true if the
// message was a recognized admin command (handled), false otherwise.
func (s *Service) HandleAdminCommand(ctx context.Context, m *tg.Message) bool {
	if m == nil || s.adminID == 0 || m.Chat.ID != s.adminID {
		return false
	}
	// A pending admin input flow captures the next non-command admin message.
	if s.consumeAdminInput(ctx, m) {
		return true
	}
	fields := strings.Fields(m.Text)
	if len(fields) == 0 {
		return false
	}
	switch fields[0] {
	case "/admin":
		s.SendAdminMenu(ctx)
		return true
	case "/tariffs":
		s.cmdListTariffs(ctx)
		return true
	case "/settariff":
		s.cmdSetTariff(ctx, fields)
		return true
	case "/deltariff":
		s.cmdDelTariff(ctx, fields)
		return true
	case "/setrequisites":
		s.cmdSetRequisites(ctx)
		return true
	case "/requisites":
		s.cmdShowRequisites(ctx)
		return true
	default:
		return false
	}
}

// cmdSetRequisites starts the two-step flow: the next non-command admin message
// becomes the stored requisites text.
func (s *Service) cmdSetRequisites(ctx context.Context) {
	s.mu.Lock()
	s.adminInput.step = adminInputRequisites
	s.mu.Unlock()
	_ = s.bot.SendPlain(ctx, s.adminID, "Отправьте текст реквизитов следующим сообщением.")
}

// cmdShowRequisites replies with the currently stored requisites, or a notice
// that none are set.
func (s *Service) cmdShowRequisites(ctx context.Context) {
	s.mu.Lock()
	req := s.requisites
	s.mu.Unlock()
	if req == "" {
		_ = s.bot.SendPlain(ctx, s.adminID, "Реквизиты не заданы.")
		return
	}
	_ = s.bot.SendPlain(ctx, s.adminID, "Реквизиты для оплаты:\n\n"+req)
}

func (s *Service) consumeAdminInput(ctx context.Context, m *tg.Message) bool {
	s.mu.Lock()
	step := s.adminInput.step
	s.mu.Unlock()
	if step == adminInputNone {
		return false
	}
	text := strings.TrimSpace(m.Text)
	if text == "" || strings.HasPrefix(text, "/") {
		return false
	}
	switch step {
	case adminInputRequisites:
		return s.consumeRequisitesText(ctx, text)
	case adminInputTariffMonths:
		return s.consumeTariffMonths(ctx, text)
	case adminInputTariffPrice:
		return s.consumeTariffPrice(ctx, text)
	}
	return false
}

func (s *Service) consumeRequisitesText(ctx context.Context, text string) bool {
	if err := s.store.UpsertSetting(ctx, requisitesKey, text); err != nil {
		s.logger.Error("save requisites failed", "err", err.Error())
		s.mu.Lock()
		s.adminInput.step = adminInputNone
		s.mu.Unlock()
		_ = s.bot.SendPlain(ctx, s.adminID, "Ошибка сохранения реквизитов.")
		return true
	}
	s.mu.Lock()
	s.requisites = text
	s.adminInput.step = adminInputNone
	s.mu.Unlock()
	_ = s.bot.SendPlain(ctx, s.adminID, "Реквизиты сохранены.")
	return true
}

func (s *Service) consumeTariffMonths(ctx context.Context, text string) bool {
	months, err := strconv.Atoi(text)
	if err != nil || months < 1 {
		_ = s.bot.SendPlain(ctx, s.adminID, "Введите целое число ≥ 1. Пример: 3")
		return true
	}
	s.mu.Lock()
	s.adminInput.pendingMonths = months
	s.adminInput.step = adminInputTariffPrice
	s.mu.Unlock()
	_ = s.bot.SendPlain(ctx, s.adminID, "Введите цену (целое ≥ 0):")
	return true
}

func (s *Service) consumeTariffPrice(ctx context.Context, text string) bool {
	price, err := strconv.Atoi(text)
	if err != nil || price < 0 {
		_ = s.bot.SendPlain(ctx, s.adminID, "Введите целое число ≥ 0. Пример: 500")
		return true
	}
	s.mu.Lock()
	months := s.adminInput.pendingMonths
	s.adminInput = adminInputState{}
	s.mu.Unlock()
	if err := s.store.UpsertTariff(ctx, months, price); err != nil {
		s.logger.Error("upsert tariff failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, s.adminID, "Ошибка сохранения тарифа.")
		return true
	}
	_ = s.bot.SendPlain(ctx, s.adminID, fmt.Sprintf("Тариф сохранён: %d мес. — %s", months, s.priceLabel(price)))
	return true
}

func (s *Service) cmdListTariffs(ctx context.Context) {
	tariffs, err := s.store.ListTariffs(ctx)
	if err != nil {
		s.logger.Error("list tariffs failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, s.adminID, "Ошибка чтения тарифов.")
		return
	}
	if len(tariffs) == 0 {
		_ = s.bot.SendPlain(ctx, s.adminID, "Тарифы не заданы. Добавьте: /settariff <месяцев> <цена>")
		return
	}
	var b strings.Builder
	b.WriteString("Тарифы:\n")
	for _, t := range tariffs {
		b.WriteString(fmt.Sprintf("%d мес. — %s\n", t.Months, s.priceLabel(t.Price)))
	}
	_ = s.bot.SendPlain(ctx, s.adminID, strings.TrimRight(b.String(), "\n"))
}

func (s *Service) cmdSetTariff(ctx context.Context, fields []string) {
	if len(fields) != 3 {
		_ = s.bot.SendPlain(ctx, s.adminID, "Использование: /settariff <месяцев> <цена>")
		return
	}
	months, err1 := strconv.Atoi(fields[1])
	price, err2 := strconv.Atoi(fields[2])
	if err1 != nil || err2 != nil || months < 1 || price < 0 {
		_ = s.bot.SendPlain(ctx, s.adminID, "Месяцев — целое ≥ 1, цена — целое ≥ 0. Пример: /settariff 3 450")
		return
	}
	if err := s.store.UpsertTariff(ctx, months, price); err != nil {
		s.logger.Error("upsert tariff failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, s.adminID, "Ошибка сохранения тарифа.")
		return
	}
	_ = s.bot.SendPlain(ctx, s.adminID, fmt.Sprintf("Тариф сохранён: %d мес. — %s", months, s.priceLabel(price)))
}

func (s *Service) SendAdminMenu(ctx context.Context) {
	kb := &tg.InlineKeyboardMarkup{
		InlineKeyboard: [][]tg.InlineKeyboardButton{
			{{Text: "📋 Посмотреть тарифы", CallbackData: "adm:tariffs"}},
			{{Text: "➕ Добавить тариф", CallbackData: "adm:addtariff"}},
			{{Text: "❌ Удалить тариф", CallbackData: "adm:del_list"}},
			{{Text: "💳 Посмотреть реквизиты", CallbackData: "adm:req"}},
			{{Text: "✏️ Изменить реквизиты", CallbackData: "adm:setreq"}},
		},
	}
	_ = s.bot.SendPlainWithKeyboard(ctx, s.adminID, "Меню администратора", kb)
}

func (s *Service) cmdDelTariff(ctx context.Context, fields []string) {
	if len(fields) != 2 {
		_ = s.bot.SendPlain(ctx, s.adminID, "Использование: /deltariff <месяцев>")
		return
	}
	months, err := strconv.Atoi(fields[1])
	if err != nil || months < 1 {
		_ = s.bot.SendPlain(ctx, s.adminID, "Месяцев — целое ≥ 1. Пример: /deltariff 3")
		return
	}
	deleted, err := s.store.DeleteTariff(ctx, months)
	if err != nil {
		s.logger.Error("delete tariff failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, s.adminID, "Ошибка удаления тарифа.")
		return
	}
	if !deleted {
		_ = s.bot.SendPlain(ctx, s.adminID, fmt.Sprintf("Тариф на %d мес. не найден.", months))
		return
	}
	_ = s.bot.SendPlain(ctx, s.adminID, fmt.Sprintf("Тариф на %d мес. удалён.", months))
}
