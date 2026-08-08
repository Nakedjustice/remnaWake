package payments

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/Nakedjustice/remnaWake/internal/i18n"
	tg "github.com/Nakedjustice/remnaWake/internal/telegram"
)

// rememberUserCtl stores the admin's current "Manage user" target, evicting
// stale entries first.
func (s *Service) rememberUserCtl(chatID int64, sub *Subscriber) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for k, v := range s.userCtl {
		if now.Sub(v.createdAt) > userCtlTTL {
			delete(s.userCtl, k)
		}
	}
	s.userCtl[chatID] = &userCtlState{username: sub.Username, createdAt: now}
}

// userCtlTarget returns the remembered target's username, or false when the
// admin has no active (non-expired) "Manage user" session.
func (s *Service) userCtlTarget(chatID int64) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.userCtl[chatID]
	if st == nil || s.now().Sub(st.createdAt) > userCtlTTL {
		return "", false
	}
	return st.username, true
}

// startUserLookup begins the "Manage user" flow by asking for a username/link.
func (s *Service) startUserLookup(ctx context.Context, chatID int64) {
	s.mu.Lock()
	state := s.adminInput[chatID]
	state.step = adminInputUserLookup
	s.adminInput[chatID] = state
	s.mu.Unlock()
	_ = s.bot.SendPlain(ctx, chatID,
		i18n.T("Отправьте имя профиля или ссылку на подписку пользователя, которым хотите управлять:"))
}

// consumeUserLookup resolves the lookup query and shows the user card.
func (s *Service) consumeUserLookup(ctx context.Context, chatID int64, text string) bool {
	s.mu.Lock()
	delete(s.adminInput, chatID)
	s.mu.Unlock()
	sub, err := s.findManagedUser(ctx, text)
	switch {
	case err == ErrRequestNotFound:
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Пользователь не найден. Проверьте имя или ссылку и попробуйте ещё раз."))
		return true
	case err == ErrBadInput:
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Не удалось распознать запрос. Пришлите имя профиля или ссылку на подписку."))
		return true
	case err != nil:
		s.logger.Error("admin: find managed user failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Ошибка получения данных из панели. Попробуйте позже."))
		return true
	}
	s.rememberUserCtl(chatID, sub)
	s.sendUserCard(ctx, chatID, sub)
	return true
}

// reloadUserCard re-fetches the remembered target and re-renders its card so it
// always reflects the current panel state. Returns the fresh subscriber.
func (s *Service) reloadUserCard(ctx context.Context, chatID int64) *Subscriber {
	username, ok := s.userCtlTarget(chatID)
	if !ok {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Сессия управления пользователем истекла. Откройте её заново через меню."))
		return nil
	}
	sub, err := s.findManagedUser(ctx, username)
	if err != nil || sub == nil {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Не удалось обновить карточку пользователя. Попробуйте позже."))
		return nil
	}
	s.sendUserCard(ctx, chatID, sub)
	return sub
}

// formatLimit renders 0 as the unlimited sign and a positive value with a unit.
func formatLimit(v int64, unit string) string {
	if v <= 0 {
		return "∞"
	}
	return fmt.Sprintf("%d%s", v, unit)
}

// sendUserCard renders the user-management card with its action keyboard.
func (s *Service) sendUserCard(ctx context.Context, chatID int64, sub *Subscriber) {
	var b strings.Builder
	b.WriteString(i18n.T("👤 Управление пользователем\n\n"))
	b.WriteString(i18n.T("Имя: ") + sub.Username + "\n")
	b.WriteString(i18n.T("Статус: ") + sub.Status + "\n")
	b.WriteString(i18n.T("Подписка до: ") + sub.ExpireAt.Format("02.01.2006") + "\n")
	hwid := int64(0)
	if sub.HwidDeviceLimit != nil {
		hwid = int64(*sub.HwidDeviceLimit)
	}
	b.WriteString(i18n.T("Лимит устройств: ") + formatLimit(hwid, "") + "\n")
	b.WriteString(i18n.T("Лимит трафика: ") + formatLimit(sub.TrafficLimitBytes/bytesPerGB, " GB") + "\n")
	b.WriteString(i18n.T("Сброс трафика: ") + sub.TrafficLimitStrategy + "\n")
	squads := i18n.T("нет")
	if len(sub.SquadNames) > 0 {
		squads = strings.Join(sub.SquadNames, ", ")
	}
	b.WriteString(i18n.T("Сквады: ") + squads)

	statusLabel := i18n.T("⛔ Отключить подписку")
	if sub.Status != "ACTIVE" {
		statusLabel = i18n.T("✅ Включить подписку")
	}
	rows := [][]tg.InlineKeyboardButton{
		{
			{Text: i18n.T("+30д"), CallbackData: "adm:u:ext:30"},
			{Text: i18n.T("+90д"), CallbackData: "adm:u:ext:90"},
			{Text: i18n.T("−30д"), CallbackData: "adm:u:ext:-30"},
		},
		{{Text: i18n.T("⏳ Срок: указать дни"), CallbackData: "adm:u:extset"}},
		{{Text: i18n.T("📱 Лимит устройств"), CallbackData: "adm:u:hwid"}},
		{{Text: i18n.T("📊 Лимит трафика"), CallbackData: "adm:u:traffic"}},
		{{Text: i18n.T("🔄 Стратегия сброса"), CallbackData: "adm:u:reset"}},
		{{Text: statusLabel, CallbackData: "adm:u:status"}},
		{{Text: i18n.T("🛡 Сквады"), CallbackData: "adm:u:squads"}},
		{
			{Text: i18n.T("🔁 Обновить"), CallbackData: "adm:u:card"},
			{Text: i18n.T("← Меню"), CallbackData: "adm:menu"},
		},
	}
	_, _ = s.bot.SendPlainWithKeyboard(ctx, chatID, b.String(), &tg.InlineKeyboardMarkup{InlineKeyboard: rows})
}

// handleUserExpiryDelta applies a quick +/- days expiry change from an
// adm:u:ext:<days> button.
func (s *Service) handleUserExpiryDelta(ctx context.Context, chatID int64, data string) {
	days, err := strconv.Atoi(strings.TrimPrefix(data, "adm:u:ext:"))
	if err != nil {
		return
	}
	username, ok := s.userCtlTarget(chatID)
	if !ok {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Сессия управления пользователем истекла. Откройте её заново через меню."))
		return
	}
	sub, err := s.findManagedUser(ctx, username)
	if err != nil || sub == nil {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Не удалось обновить карточку пользователя. Попробуйте позже."))
		return
	}
	if _, err := s.applyUserExpiryDelta(ctx, sub.RemnawaveID, sub.ExpireAt, days); err != nil {
		s.reportUserCtlError(ctx, chatID, err)
		return
	}
	s.reloadUserCard(ctx, chatID)
}

// startUserInput begins a text-input step (HWID/traffic/expiry) for the current
// target.
func (s *Service) startUserInput(ctx context.Context, chatID int64, step adminInputStep, prompt string) {
	if _, ok := s.userCtlTarget(chatID); !ok {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Сессия управления пользователем истекла. Откройте её заново через меню."))
		return
	}
	s.mu.Lock()
	state := s.adminInput[chatID]
	state.step = step
	s.adminInput[chatID] = state
	s.mu.Unlock()
	_ = s.bot.SendPlain(ctx, chatID, prompt)
}

// consumeUserHwid applies a typed HWID device limit (0 = unlimited).
func (s *Service) consumeUserHwid(ctx context.Context, chatID int64, text string) bool {
	s.mu.Lock()
	delete(s.adminInput, chatID)
	s.mu.Unlock()
	n, err := strconv.Atoi(text)
	if err != nil || n < 0 {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Введите целое число ≥ 0 (0 — без ограничения)."))
		return true
	}
	username, ok := s.userCtlTarget(chatID)
	if !ok {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Сессия управления пользователем истекла. Откройте её заново через меню."))
		return true
	}
	sub, err := s.findManagedUser(ctx, username)
	if err != nil || sub == nil {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Не удалось обновить карточку пользователя. Попробуйте позже."))
		return true
	}
	if err := s.setUserHwidLimit(ctx, sub.RemnawaveID, n); err != nil {
		s.reportUserCtlError(ctx, chatID, err)
		return true
	}
	s.reloadUserCard(ctx, chatID)
	return true
}

// consumeUserTraffic applies a typed traffic limit in GB (0 = unlimited).
func (s *Service) consumeUserTraffic(ctx context.Context, chatID int64, text string) bool {
	s.mu.Lock()
	delete(s.adminInput, chatID)
	s.mu.Unlock()
	gb, err := strconv.ParseInt(text, 10, 64)
	if err != nil || gb < 0 {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Введите целое число ≥ 0 GB (0 — без ограничения)."))
		return true
	}
	username, ok := s.userCtlTarget(chatID)
	if !ok {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Сессия управления пользователем истекла. Откройте её заново через меню."))
		return true
	}
	sub, err := s.findManagedUser(ctx, username)
	if err != nil || sub == nil {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Не удалось обновить карточку пользователя. Попробуйте позже."))
		return true
	}
	if err := s.setUserTrafficLimitGB(ctx, sub.RemnawaveID, gb); err != nil {
		s.reportUserCtlError(ctx, chatID, err)
		return true
	}
	s.reloadUserCard(ctx, chatID)
	return true
}

// consumeUserExpiry applies a typed +/- days expiry change.
func (s *Service) consumeUserExpiry(ctx context.Context, chatID int64, text string) bool {
	s.mu.Lock()
	delete(s.adminInput, chatID)
	s.mu.Unlock()
	days, err := strconv.Atoi(strings.TrimPrefix(text, "+"))
	if err != nil {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Введите целое число дней (можно со знаком «−» для уменьшения)."))
		return true
	}
	username, ok := s.userCtlTarget(chatID)
	if !ok {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Сессия управления пользователем истекла. Откройте её заново через меню."))
		return true
	}
	sub, err := s.findManagedUser(ctx, username)
	if err != nil || sub == nil {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Не удалось обновить карточку пользователя. Попробуйте позже."))
		return true
	}
	if _, err := s.applyUserExpiryDelta(ctx, sub.RemnawaveID, sub.ExpireAt, days); err != nil {
		s.reportUserCtlError(ctx, chatID, err)
		return true
	}
	s.reloadUserCard(ctx, chatID)
	return true
}

// handleUserStatusToggle flips ACTIVE⇄DISABLED for the current target.
func (s *Service) handleUserStatusToggle(ctx context.Context, chatID int64) {
	username, ok := s.userCtlTarget(chatID)
	if !ok {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Сессия управления пользователем истекла. Откройте её заново через меню."))
		return
	}
	sub, err := s.findManagedUser(ctx, username)
	if err != nil || sub == nil {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Не удалось обновить карточку пользователя. Попробуйте позже."))
		return
	}
	if _, err := s.toggleUserStatus(ctx, sub.RemnawaveID, sub.Status); err != nil {
		s.reportUserCtlError(ctx, chatID, err)
		return
	}
	s.reloadUserCard(ctx, chatID)
}

// sendUserResetStrategies shows the per-user traffic-reset strategy picker.
func (s *Service) sendUserResetStrategies(ctx context.Context, chatID int64) {
	username, ok := s.userCtlTarget(chatID)
	if !ok {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Сессия управления пользователем истекла. Откройте её заново через меню."))
		return
	}
	sub, err := s.findManagedUser(ctx, username)
	if err != nil || sub == nil {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Не удалось обновить карточку пользователя. Попробуйте позже."))
		return
	}
	rows := make([][]tg.InlineKeyboardButton, 0, len(TrafficResetStrategies)+1)
	for _, st := range TrafficResetStrategies {
		text := st
		if st == sub.TrafficLimitStrategy {
			text = "✅ " + text
		}
		rows = append(rows, []tg.InlineKeyboardButton{{Text: text, CallbackData: "adm:u:reset:" + st}})
	}
	rows = append(rows, []tg.InlineKeyboardButton{{Text: i18n.T("← Назад"), CallbackData: "adm:u:card"}})
	_, _ = s.bot.SendPlainWithKeyboard(ctx, chatID,
		i18n.T("Выберите стратегию сброса трафика для пользователя:"),
		&tg.InlineKeyboardMarkup{InlineKeyboard: rows})
}

// handleUserResetPick applies a per-user reset strategy from adm:u:reset:<S>.
func (s *Service) handleUserResetPick(ctx context.Context, chatID int64, data string) {
	strategy := strings.TrimPrefix(data, "adm:u:reset:")
	username, ok := s.userCtlTarget(chatID)
	if !ok {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Сессия управления пользователем истекла. Откройте её заново через меню."))
		return
	}
	sub, err := s.findManagedUser(ctx, username)
	if err != nil || sub == nil {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Не удалось обновить карточку пользователя. Попробуйте позже."))
		return
	}
	if err := s.setUserResetStrategy(ctx, sub.RemnawaveID, strategy); err != nil {
		s.reportUserCtlError(ctx, chatID, err)
		return
	}
	s.reloadUserCard(ctx, chatID)
}

// sendUserSquads shows the per-user squad toggle list.
func (s *Service) sendUserSquads(ctx context.Context, chatID int64) {
	username, ok := s.userCtlTarget(chatID)
	if !ok {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Сессия управления пользователем истекла. Откройте её заново через меню."))
		return
	}
	sub, err := s.findManagedUser(ctx, username)
	if err != nil || sub == nil {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Не удалось обновить карточку пользователя. Попробуйте позже."))
		return
	}
	if s.squads == nil {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Список сквадов недоступен."))
		return
	}
	squads, err := s.squads.GetInternalSquads(ctx)
	if err != nil {
		s.logger.Error("admin: list squads failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Ошибка получения сквадов из панели. Попробуйте позже."))
		return
	}
	cur := make(map[string]bool, len(sub.SquadUUIDs))
	for _, u := range sub.SquadUUIDs {
		cur[u] = true
	}
	rows := make([][]tg.InlineKeyboardButton, 0, len(squads)+1)
	for _, sq := range squads {
		mark := "⬜ "
		if cur[sq.UUID] {
			mark = "✅ "
		}
		rows = append(rows, []tg.InlineKeyboardButton{{Text: mark + sq.Name, CallbackData: "adm:u:sq:" + sq.UUID}})
	}
	rows = append(rows, []tg.InlineKeyboardButton{{Text: i18n.T("← Назад"), CallbackData: "adm:u:card"}})
	text := i18n.T("Сквады пользователя (нажмите, чтобы добавить или убрать):")
	if len(squads) == 0 {
		text = i18n.T("В панели нет внутренних сквадов.")
	}
	_, _ = s.bot.SendPlainWithKeyboard(ctx, chatID, text, &tg.InlineKeyboardMarkup{InlineKeyboard: rows})
}

// handleUserSquadToggle adds/removes one squad for the current target.
func (s *Service) handleUserSquadToggle(ctx context.Context, chatID int64, data string) {
	squadUUID := strings.TrimPrefix(data, "adm:u:sq:")
	username, ok := s.userCtlTarget(chatID)
	if !ok {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Сессия управления пользователем истекла. Откройте её заново через меню."))
		return
	}
	sub, err := s.findManagedUser(ctx, username)
	if err != nil || sub == nil {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Не удалось обновить карточку пользователя. Попробуйте позже."))
		return
	}
	if err := s.toggleUserSquad(ctx, sub.RemnawaveID, sub.SquadUUIDs, squadUUID); err != nil {
		s.reportUserCtlError(ctx, chatID, err)
		return
	}
	s.sendUserSquads(ctx, chatID)
}

// reportUserCtlError maps a "Manage user" mutation error to an admin reply.
func (s *Service) reportUserCtlError(ctx context.Context, chatID int64, err error) {
	switch {
	case err == ErrBadInput:
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Некорректное значение."))
	case err == ErrPanelUnavailable:
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Панель недоступна, попробуйте позже."))
	default:
		s.logger.Error("admin: manage user mutation failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Ошибка, попробуйте позже."))
	}
}

// sendDefaultTrafficResetPicker shows the global new-user reset-strategy picker.
func (s *Service) sendDefaultTrafficResetPicker(ctx context.Context, chatID int64) {
	cur := s.getDefaultTrafficReset()
	rows := make([][]tg.InlineKeyboardButton, 0, len(TrafficResetStrategies)+1)
	for _, st := range TrafficResetStrategies {
		text := st
		if st == cur {
			text = "✅ " + text
		}
		rows = append(rows, []tg.InlineKeyboardButton{{Text: text, CallbackData: "adm:treset:" + st}})
	}
	rows = append(rows, []tg.InlineKeyboardButton{{Text: i18n.T("← Меню"), CallbackData: "adm:menu"}})
	_, _ = s.bot.SendPlainWithKeyboard(ctx, chatID,
		i18n.T("Стратегия сброса трафика для новых пользователей:"),
		&tg.InlineKeyboardMarkup{InlineKeyboard: rows})
}

// handleDefaultTrafficResetPick persists the global new-user reset strategy.
func (s *Service) handleDefaultTrafficResetPick(ctx context.Context, chatID int64, data string) {
	strategy := strings.TrimPrefix(data, "adm:treset:")
	if err := s.setDefaultTrafficReset(ctx, strategy); err != nil {
		if err == ErrBadInput {
			_ = s.bot.SendPlain(ctx, chatID, i18n.T("Неизвестная стратегия."))
		} else {
			s.logger.Error("admin: save default traffic reset failed", "err", err.Error())
			_ = s.bot.SendPlain(ctx, chatID, i18n.T("Ошибка сохранения настройки."))
		}
		return
	}
	s.sendDefaultTrafficResetPicker(ctx, chatID)
}
