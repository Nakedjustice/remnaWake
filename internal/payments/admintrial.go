package payments

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Nakedjustice/remnaWake/internal/i18n"
	tg "github.com/Nakedjustice/remnaWake/internal/telegram"
)

// sendTrialAdminCard renders the free-trial admin card: current state plus
// toggle and "change duration" buttons. Mirrors the web admin trial settings.
func (s *Service) sendTrialAdminCard(ctx context.Context, chatID int64) {
	cfg := s.trialConfig()
	state := i18n.T("выключен")
	toggle := i18n.T("✅ Включить")
	if cfg.Enabled {
		state = i18n.T("включён")
		toggle = i18n.T("🚫 Выключить")
	}
	squad := i18n.T("сквад по умолчанию")
	if cfg.SquadUUID != "" {
		squad = cfg.SquadUUID
	}
	approval := i18n.T("выключено")
	approvalToggle := i18n.T("✅ Включить подтверждение админом")
	if cfg.RequireApproval {
		approval = i18n.T("включено")
		approvalToggle = i18n.T("🚫 Выключить подтверждение админом")
	}
	text := fmt.Sprintf(i18n.T("🎁 Пробный период: %s\nДлительность: %d %s\nТрафик: %s\nУстройства: %s\nСквад: %s\nСброс трафика: NO_RESET\nПодтверждение админом (новые без реферала): %s"),
		state, cfg.Days, i18n.PluralDays(cfg.Days), formatLimit(int64(cfg.TrafficLimitGB), " GB"), formatLimit(int64(cfg.HwidDeviceLimit), ""), squad, approval)
	kb := &tg.InlineKeyboardMarkup{InlineKeyboard: [][]tg.InlineKeyboardButton{
		{{Text: toggle, CallbackData: "adm:trial:toggle"}},
		{{Text: approvalToggle, CallbackData: "adm:trial:approval"}},
		{{Text: i18n.T("✏️ Изменить длительность (дни)"), CallbackData: "adm:trial:days"}},
		{{Text: i18n.T("📊 Изменить лимит трафика"), CallbackData: "adm:trial:traffic"}},
		{{Text: i18n.T("📱 Изменить лимит устройств"), CallbackData: "adm:trial:hwid"}},
		{{Text: i18n.T("🛡 Выбрать сквад"), CallbackData: "adm:trial:squad"}},
		{{Text: i18n.T("← Меню"), CallbackData: "adm:menu"}},
	}}
	_, _ = s.bot.SendPlainWithKeyboard(ctx, chatID, text, kb)
}

// handleTrialToggle flips the trial on/off from the admin card and re-renders.
func (s *Service) handleTrialToggle(ctx context.Context, chatID int64) {
	cfg := s.trialConfig()
	if err := s.setTrialEnabled(ctx, !cfg.Enabled); err != nil {
		s.logger.Error("admin: save trial enabled failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Ошибка сохранения настройки."))
		return
	}
	s.sendTrialAdminCard(ctx, chatID)
}

// handleTrialApprovalToggle flips the "require admin approval" gate on/off from
// the admin card and re-renders.
func (s *Service) handleTrialApprovalToggle(ctx context.Context, chatID int64) {
	cfg := s.trialConfig()
	if err := s.setTrialApproval(ctx, !cfg.RequireApproval); err != nil {
		s.logger.Error("admin: save trial approval failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Ошибка сохранения настройки."))
		return
	}
	s.sendTrialAdminCard(ctx, chatID)
}

func (s *Service) startTrialLimitInput(ctx context.Context, chatID int64, step adminInputStep, prompt string) {
	s.mu.Lock()
	state := s.adminInput[chatID]
	state.step = step
	s.adminInput[chatID] = state
	s.mu.Unlock()
	_ = s.bot.SendPlain(ctx, chatID, prompt)
}

func (s *Service) consumeTrialLimit(ctx context.Context, chatID int64, text string, traffic bool) bool {
	limit, err := strconv.Atoi(text)
	if err != nil || limit < 0 {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Введите целое число ≥ 0. Значение 0 означает без ограничения."))
		return true
	}
	s.mu.Lock()
	delete(s.adminInput, chatID)
	s.mu.Unlock()
	if traffic {
		err = s.setTrialTrafficLimit(ctx, limit)
	} else {
		err = s.setTrialHwidLimit(ctx, limit)
	}
	if err != nil {
		s.logger.Error("admin: save trial limit failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Ошибка сохранения настройки."))
		return true
	}
	s.sendTrialAdminCard(ctx, chatID)
	return true
}

func (s *Service) sendTrialSquadList(ctx context.Context, chatID int64) {
	if s.squads == nil {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Список сквадов недоступен."))
		return
	}
	squads, err := s.squads.GetInternalSquads(ctx)
	if err != nil {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Ошибка получения сквадов из панели. Попробуйте позже."))
		return
	}
	selected := s.trialConfig().SquadUUID
	rows := make([][]tg.InlineKeyboardButton, 0, len(squads)+2)
	defaultText := i18n.T("Использовать сквад по умолчанию")
	if selected == "" {
		defaultText = "✅ " + defaultText
	}
	rows = append(rows, []tg.InlineKeyboardButton{{Text: defaultText, CallbackData: "adm:trial:sq:default"}})
	for _, squad := range squads {
		text := squad.Name
		if squad.UUID == selected {
			text = "✅ " + text
		}
		rows = append(rows, []tg.InlineKeyboardButton{{Text: text, CallbackData: "adm:trial:sq:" + squad.UUID}})
	}
	rows = append(rows, []tg.InlineKeyboardButton{{Text: i18n.T("← Назад"), CallbackData: "adm:trial"}})
	_, _ = s.bot.SendPlainWithKeyboard(ctx, chatID, i18n.T("Выберите сквад для пробных профилей:"), &tg.InlineKeyboardMarkup{InlineKeyboard: rows})
}

func (s *Service) handleTrialSquadPick(ctx context.Context, chatID int64, data string) {
	uuid := strings.TrimPrefix(data, "adm:trial:sq:")
	if uuid == "default" {
		uuid = ""
	}
	if err := s.setTrialSquad(ctx, uuid); err != nil {
		if errors.Is(err, ErrBadInput) {
			_ = s.bot.SendPlain(ctx, chatID, i18n.T("Сквад не найден в панели. Обновите список."))
		} else {
			_ = s.bot.SendPlain(ctx, chatID, i18n.T("Ошибка сохранения сквада."))
		}
		return
	}
	s.sendTrialAdminCard(ctx, chatID)
}

// startTrialDaysInput begins the "set trial duration" admin text flow.
func (s *Service) startTrialDaysInput(ctx context.Context, chatID int64) {
	s.mu.Lock()
	state := s.adminInput[chatID]
	state.step = adminInputTrialDays
	s.adminInput[chatID] = state
	s.mu.Unlock()
	_ = s.bot.SendPlain(ctx, chatID, i18n.T("Введите длительность пробного периода в днях (целое ≥ 1):"))
}

func (s *Service) consumeTrialDays(ctx context.Context, chatID int64, text string) bool {
	days, err := strconv.Atoi(text)
	if err != nil || days < 1 {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Введите целое число ≥ 1. Пример: 3"))
		return true
	}
	s.mu.Lock()
	delete(s.adminInput, chatID)
	s.mu.Unlock()
	if err := s.setTrialDays(ctx, days); err != nil {
		s.logger.Error("admin: save trial days failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Ошибка сохранения настройки."))
		return true
	}
	s.sendTrialAdminCard(ctx, chatID)
	return true
}

// sendReferralAdminCard renders the referral admin card: current state plus
// toggle and "change bonus" buttons. Mirrors the web admin referral settings.
func (s *Service) sendReferralAdminCard(ctx context.Context, chatID int64) {
	enabled, inviter, invitee := s.referralConfig()
	state := i18n.T("выключена")
	toggle := i18n.T("✅ Включить")
	if enabled {
		state = i18n.T("включена")
		toggle = i18n.T("🚫 Выключить")
	}
	text := fmt.Sprintf(
		i18n.T("🎉 Реферальная программа: %s\nБонус пригласившему: %d дн.\nБонус приглашённому: %d дн."),
		state, inviter, invitee)
	kb := &tg.InlineKeyboardMarkup{InlineKeyboard: [][]tg.InlineKeyboardButton{
		{{Text: toggle, CallbackData: "adm:referral:toggle"}},
		{{Text: i18n.T("✏️ Бонус пригласившему"), CallbackData: "adm:referral:inviter"}},
		{{Text: i18n.T("✏️ Бонус приглашённому"), CallbackData: "adm:referral:invitee"}},
		{{Text: i18n.T("← Меню"), CallbackData: "adm:menu"}},
	}}
	_, _ = s.bot.SendPlainWithKeyboard(ctx, chatID, text, kb)
}

// handleReferralToggle flips referrals on/off from the admin card and re-renders.
// Enabling with both bonuses at zero is rejected by setReferralEnabled.
func (s *Service) handleReferralToggle(ctx context.Context, chatID int64) {
	enabled, _, _ := s.referralConfig()
	if err := s.setReferralEnabled(ctx, !enabled); err != nil {
		if errors.Is(err, ErrBadInput) {
			_ = s.bot.SendPlain(ctx, chatID, i18n.T("Сначала задайте хотя бы один ненулевой бонус."))
		} else {
			s.logger.Error("admin: save referral enabled failed", "err", err.Error())
			_ = s.bot.SendPlain(ctx, chatID, i18n.T("Ошибка сохранения настройки."))
		}
		s.sendReferralAdminCard(ctx, chatID)
		return
	}
	s.sendReferralAdminCard(ctx, chatID)
}

func (s *Service) startReferralBonusInput(ctx context.Context, chatID int64, step adminInputStep) {
	s.mu.Lock()
	state := s.adminInput[chatID]
	state.step = step
	s.adminInput[chatID] = state
	s.mu.Unlock()
	_ = s.bot.SendPlain(ctx, chatID, i18n.T("Введите количество бонусных дней (целое ≥ 0):"))
}

func (s *Service) consumeReferralBonus(ctx context.Context, chatID int64, text string, inviterSide bool) bool {
	days, err := strconv.Atoi(text)
	if err != nil || days < 0 {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Введите целое число ≥ 0. Пример: 30"))
		return true
	}
	s.mu.Lock()
	delete(s.adminInput, chatID)
	s.mu.Unlock()
	_, inviter, invitee := s.referralConfig()
	if inviterSide {
		inviter = days
	} else {
		invitee = days
	}
	if err := s.setReferralDays(ctx, inviter, invitee); err != nil {
		s.logger.Error("admin: save referral days failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Ошибка сохранения настройки."))
		return true
	}
	s.sendReferralAdminCard(ctx, chatID)
	return true
}
