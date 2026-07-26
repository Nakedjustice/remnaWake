package payments

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Nakedjustice/remnaWake/internal/i18n"
	tg "github.com/Nakedjustice/remnaWake/internal/telegram"
)

// Errors raised by the device-slot flows. Each one is mapped in both consumers:
// the HTTP status switch in internal/webapp/server.go and deviceErrorText below.
var (
	// ErrDevicesUnavailable: no device manager is wired in, so the panel's HWID
	// endpoints cannot be reached at all.
	ErrDevicesUnavailable = errors.New("device management unavailable")
	// ErrDeviceNotFound: the hwid is not registered against the caller's
	// profile. A device that exists on somebody else's profile is reported
	// identically, so the revoke path cannot be used to probe for real hwids.
	ErrDeviceNotFound = errors.New("device not found")
)

// Device is one device occupying a slot of a profile's HWID limit, kept
// payments-local so this package stays decoupled from the remnawave package;
// main.go adapts the concrete client to DeviceManager. Every descriptive field
// is self-reported by the client app and may be empty.
type Device struct {
	HWID        string
	Platform    string
	OSVersion   string
	DeviceModel string
	CreatedAt   time.Time
}

// DeviceManager lists and frees the HWID slots of a panel profile. Wired once
// at startup via SetDeviceManager; nil = device management unavailable.
type DeviceManager interface {
	ListDevices(ctx context.Context, userUUID string) ([]Device, error)
	// DeleteDevice frees one slot. It reports ErrDeviceNotFound when the panel
	// no longer knows the device, so a racing second revoke is distinguishable
	// from an outage.
	DeleteDevice(ctx context.Context, userUUID, hwid string) error
}

// WebDevice is one registered device as rendered by the bot and the Mini App.
// Platform, OS version and model are panel data, so they are passed through
// verbatim and never translated; only CreatedAt is reformatted (DD.MM.YYYY).
type WebDevice struct {
	HWID        string `json:"hwid"`
	Platform    string `json:"platform,omitempty"`
	OSVersion   string `json:"os_version,omitempty"`
	DeviceModel string `json:"device_model,omitempty"`
	CreatedAt   string `json:"created_at,omitempty"`
}

// WebDeviceProfile is one linked profile's slot usage. A Limit of 0 is
// Remnawave's "no device limit", reported as Unlimited so the surfaces render
// ∞ instead of a nonsensical "n of 0"; Full then never becomes true.
type WebDeviceProfile struct {
	RemnawaveID int64       `json:"remnawave_id"`
	UUID        string      `json:"uuid,omitempty"`
	Username    string      `json:"username"`
	Limit       int         `json:"limit"`
	Used        int         `json:"used"`
	Unlimited   bool        `json:"unlimited,omitempty"`
	Full        bool        `json:"full,omitempty"`
	Devices     []WebDevice `json:"devices,omitempty"`
}

// WebDevices is the device screen: one section per linked profile. A Telegram
// account may own several panel profiles, so nothing is ever silently collapsed
// onto the first one.
type WebDevices struct {
	Profiles []WebDeviceProfile `json:"profiles"`
}

// deviceSlot pairs a listed device with the profile that holds it, so a short
// index in the callback payload can be resolved back to both.
type deviceSlot struct {
	uuid  string
	hwid  string
	label string
}

// deviceSlotState is one user's most recent listing. hwids are long, arbitrary
// and would blow Telegram's 64-byte callback budget, so the buttons carry the
// index into slots instead and this map holds the real values.
type deviceSlotState struct {
	slots     []deviceSlot
	createdAt time.Time
}

const deviceSlotTTL = 10 * time.Minute

// SetDeviceManager wires the panel's HWID endpoints. Called once at startup;
// until then the device screen reports ErrDevicesUnavailable.
func (s *Service) SetDeviceManager(dm DeviceManager) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.devices = dm
}

func (s *Service) getDeviceManager() DeviceManager {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.devices
}

// putDeviceSlots stores the slot map behind telegramID's newest listing, first
// evicting the maps of users who opened a listing and walked away.
func (s *Service) putDeviceSlots(telegramID int64, slots []deviceSlot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for id, st := range s.deviceSlots {
		if now.Sub(st.createdAt) > deviceSlotTTL {
			delete(s.deviceSlots, id)
		}
	}
	s.deviceSlots[telegramID] = &deviceSlotState{slots: slots, createdAt: now}
}

// getDeviceSlot resolves one index of telegramID's own listing. A stale or
// foreign index simply misses: the map is keyed by the presser, so a forwarded
// keyboard resolves to nothing at all.
func (s *Service) getDeviceSlot(telegramID int64, idx int) (deviceSlot, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.deviceSlots[telegramID]
	if st == nil {
		return deviceSlot{}, false
	}
	if s.now().Sub(st.createdAt) > deviceSlotTTL {
		delete(s.deviceSlots, telegramID)
		return deviceSlot{}, false
	}
	if idx < 0 || idx >= len(st.slots) {
		return deviceSlot{}, false
	}
	return st.slots[idx], true
}

// DeviceOverview is the Mini App twin of the dev:list bot screen.
func (s *Service) DeviceOverview(ctx context.Context, telegramID int64) (*WebDevices, error) {
	return s.deviceOverview(ctx, telegramID)
}

// RevokeDevice frees one slot from the Mini App. remnawaveID is caller-supplied
// and therefore only ever matched against the profiles actually linked to
// telegramID.
func (s *Service) RevokeDevice(ctx context.Context, telegramID, remnawaveID int64, hwid string) (*WebDevices, error) {
	return s.revokeDevice(ctx, telegramID, func(sub *Subscriber) bool { return sub.RemnawaveID == remnawaveID }, hwid)
}

// deviceOverview is the shared core behind both surfaces: it resolves every
// profile linked to telegramID and reads each one's occupied slots.
func (s *Service) deviceOverview(ctx context.Context, telegramID int64) (*WebDevices, error) {
	dm := s.getDeviceManager()
	if dm == nil {
		return nil, ErrDevicesUnavailable
	}
	subs, err := s.finder.FindByTelegramID(ctx, telegramID)
	if err != nil {
		return nil, fmt.Errorf("%w: find by telegram id: %v", ErrPanelUnavailable, err)
	}
	if len(subs) == 0 {
		return nil, ErrNotLinked
	}

	out := &WebDevices{Profiles: make([]WebDeviceProfile, 0, len(subs))}
	for i := range subs {
		sub := &subs[i]
		devices, err := dm.ListDevices(ctx, sub.UUID)
		if err != nil {
			// One unreadable profile poisons the whole screen: a partial listing
			// understates the occupied slots and would invite the user to revoke
			// a device that was not the problem.
			s.logger.Error("list devices failed", "uuid", sub.UUID, "err", err.Error())
			return nil, fmt.Errorf("%w: list devices: %v", ErrPanelUnavailable, err)
		}
		p := WebDeviceProfile{
			RemnawaveID: sub.RemnawaveID,
			UUID:        sub.UUID,
			Username:    sub.Username,
			Used:        len(devices),
		}
		if sub.HwidDeviceLimit != nil {
			p.Limit = *sub.HwidDeviceLimit
		}
		p.Unlimited = p.Limit <= 0
		p.Full = !p.Unlimited && p.Used >= p.Limit
		for _, d := range devices {
			wd := WebDevice{HWID: d.HWID, Platform: d.Platform, OSVersion: d.OSVersion, DeviceModel: d.DeviceModel}
			if !d.CreatedAt.IsZero() {
				wd.CreatedAt = d.CreatedAt.Format("02.01.2006")
			}
			p.Devices = append(p.Devices, wd)
		}
		out.Profiles = append(out.Profiles, p)
	}
	return out, nil
}

// revokeDevice is the shared core behind both revoke paths. match picks the
// target out of the profiles genuinely linked to telegramID, so neither a
// callback payload nor a Mini App body can name a profile the caller does not
// own; the hwid is then re-checked against that profile's live slots before
// anything is deleted. Returns the refreshed overview.
func (s *Service) revokeDevice(ctx context.Context, telegramID int64, match func(*Subscriber) bool, hwid string) (*WebDevices, error) {
	dm := s.getDeviceManager()
	if dm == nil {
		return nil, ErrDevicesUnavailable
	}
	hwid = strings.TrimSpace(hwid)
	if hwid == "" {
		return nil, ErrBadInput
	}
	subs, err := s.finder.FindByTelegramID(ctx, telegramID)
	if err != nil {
		return nil, fmt.Errorf("%w: find by telegram id: %v", ErrPanelUnavailable, err)
	}
	if len(subs) == 0 {
		return nil, ErrNotLinked
	}
	var sub *Subscriber
	for i := range subs {
		if match(&subs[i]) {
			sub = &subs[i]
			break
		}
	}
	if sub == nil {
		return nil, ErrProfileUnknown
	}

	devices, err := dm.ListDevices(ctx, sub.UUID)
	if err != nil {
		s.logger.Error("list devices failed", "uuid", sub.UUID, "err", err.Error())
		return nil, fmt.Errorf("%w: list devices: %v", ErrPanelUnavailable, err)
	}
	held := false
	for _, d := range devices {
		if d.HWID == hwid {
			held = true
			break
		}
	}
	if !held {
		return nil, ErrDeviceNotFound
	}

	if s.dryRun {
		s.logger.Info("dry-run: would revoke hwid device", "uuid", sub.UUID, "hwid", hwid)
	} else if err := dm.DeleteDevice(ctx, sub.UUID, hwid); errors.Is(err, ErrDeviceNotFound) {
		return nil, ErrDeviceNotFound
	} else if err != nil {
		s.logger.Error("revoke device failed", "uuid", sub.UUID, "err", err.Error())
		return nil, fmt.Errorf("%w: delete device: %v", ErrPanelUnavailable, err)
	}
	return s.deviceOverview(ctx, telegramID)
}

// SendDevices renders the device screen for the /devices command. Returns true
// (handled) so the dispatcher stops there.
func (s *Service) SendDevices(ctx context.Context, chatID int64) bool {
	if msg := s.sendDeviceList(ctx, chatID); msg != "" {
		_ = s.bot.SendPlain(ctx, chatID, msg)
	}
	return true
}

// handleDeviceCallback dispatches the dev:* inline buttons.
func (s *Service) handleDeviceCallback(ctx context.Context, cb *tg.CallbackQuery) bool {
	switch {
	case cb.Data == "dev:list":
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, s.sendDeviceList(ctx, cb.From.ID))
	case cb.Data == "dev:cancel":
		if cb.Message != nil {
			_ = s.bot.EditMessageReplyMarkup(ctx, cb.Message.Chat.ID, cb.Message.MessageID, nil)
		}
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Отменено."))
	case strings.HasPrefix(cb.Data, "dev:rev:"):
		s.handleDeviceRevoke(ctx, cb)
	default:
		return false
	}
	return true
}

// handleDeviceRevoke serves both dev:rev:<idx> (ask) and dev:rev:<idx>:ok
// (act). Revoking is irreversible from the user's side, so the plain form only
// ever renders the confirmation.
func (s *Service) handleDeviceRevoke(ctx context.Context, cb *tg.CallbackQuery) {
	raw := strings.TrimPrefix(cb.Data, "dev:rev:")
	confirmed := false
	if rest, ok := strings.CutSuffix(raw, ":ok"); ok {
		raw, confirmed = rest, true
	}
	idx, err := strconv.Atoi(raw)
	if err != nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Не удалось распознать устройство."))
		return
	}
	// The index is only meaningful against the presser's own listing.
	slot, ok := s.getDeviceSlot(cb.From.ID, idx)
	if !ok {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Список устройств устарел. Откройте его заново."))
		return
	}
	if !confirmed {
		s.promptDeviceRevokeConfirm(ctx, cb, idx, slot)
		return
	}
	if _, err := s.revokeDevice(ctx, cb.From.ID, func(sub *Subscriber) bool { return sub.UUID == slot.uuid }, slot.hwid); err != nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, deviceErrorText(err))
		return
	}
	if cb.Message != nil {
		_ = s.bot.EditMessageReplyMarkup(ctx, cb.Message.Chat.ID, cb.Message.MessageID, nil)
	}
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("✅ Устройство отвязано."))
	_ = s.sendDeviceList(ctx, cb.From.ID)
}

// promptDeviceRevokeConfirm asks before the irreversible step. Like the listing
// it answers into cb.From.ID rather than the originating chat: the slot map is
// keyed by the presser, so a keyboard that travelled elsewhere must never spill
// somebody's device names into that other chat.
func (s *Service) promptDeviceRevokeConfirm(ctx context.Context, cb *tg.CallbackQuery, idx int, slot deviceSlot) {
	kb := &tg.InlineKeyboardMarkup{InlineKeyboard: [][]tg.InlineKeyboardButton{{
		{Text: i18n.T("✅ Да, отвязать"), CallbackData: fmt.Sprintf("dev:rev:%d:ok", idx)},
		{Text: i18n.T("Отмена"), CallbackData: "dev:cancel"},
	}}}
	_, _ = s.bot.SendPlainWithKeyboard(ctx, cb.From.ID, fmt.Sprintf(
		i18n.T("Отвязать устройство «%s»?\n\nСлот освободится сразу. Чтобы снова пользоваться подпиской с этого устройства, придётся подключить его заново."),
		slot.label), kb)
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "")
}

// sendDeviceList renders the device screen in chatID and remembers the index →
// hwid map it just handed out. Returns the callback answer to show: empty on
// success, the mapped failure text otherwise (nothing is sent in that case, so
// the toast carries the whole explanation).
func (s *Service) sendDeviceList(ctx context.Context, chatID int64) string {
	overview, err := s.deviceOverview(ctx, chatID)
	if err != nil {
		return deviceErrorText(err)
	}

	multi := len(overview.Profiles) > 1
	var b strings.Builder
	b.WriteString(i18n.T("📱 Мои устройства\n"))
	var slots []deviceSlot
	var rows [][]tg.InlineKeyboardButton
	for i := range overview.Profiles {
		p := &overview.Profiles[i]
		b.WriteString(i18n.T("\n📡 Профиль: ") + p.Username + "\n")
		if p.Unlimited {
			b.WriteString(fmt.Sprintf(i18n.T("Устройств: %d (лимит не задан)"), p.Used) + "\n")
		} else {
			b.WriteString(fmt.Sprintf(i18n.T("Занято слотов: %d из %d"), p.Used, p.Limit) + "\n")
		}
		if p.Full {
			b.WriteString(i18n.T("⚠️ Свободных слотов нет. Отвяжите ненужное устройство, чтобы подключить новое.\n"))
		}
		if len(p.Devices) == 0 {
			b.WriteString(i18n.T("Пока нет подключённых устройств.\n"))
			continue
		}
		for j := range p.Devices {
			d := &p.Devices[j]
			label := deviceLabel(d)
			idx := len(slots)
			slots = append(slots, deviceSlot{uuid: p.UUID, hwid: d.HWID, label: label})
			line := fmt.Sprintf("%d. %s", idx+1, label)
			if d.CreatedAt != "" {
				line += fmt.Sprintf(i18n.T(" — подключено %s"), d.CreatedAt)
			}
			b.WriteString(line + "\n")
			text := fmt.Sprintf(i18n.T("❌ Отвязать %s"), label)
			if multi {
				text = fmt.Sprintf(i18n.T("❌ %s · %s"), p.Username, label)
			}
			rows = append(rows, []tg.InlineKeyboardButton{{
				Text: trimLabel(text, 48), CallbackData: fmt.Sprintf("dev:rev:%d", idx),
			}})
		}
	}
	s.putDeviceSlots(chatID, slots)
	rows = append(rows, []tg.InlineKeyboardButton{{Text: i18n.T("🔄 Обновить"), CallbackData: "dev:list"}})

	_, _ = s.bot.SendPlainWithKeyboard(ctx, chatID, strings.TrimRight(b.String(), "\n"),
		&tg.InlineKeyboardMarkup{InlineKeyboard: rows})
	return ""
}

// deviceErrorText is the bot twin of the webapp status switch: every sentinel
// the device flows raise is answered here too. A profile the caller does not own
// and a device that is simply gone deliberately share one answer.
func deviceErrorText(err error) string {
	switch {
	case errors.Is(err, ErrDevicesUnavailable):
		return i18n.T("Управление устройствами сейчас недоступно.")
	case errors.Is(err, ErrNotLinked):
		return i18n.T("Ваш Telegram пока не привязан к профилю подписки.")
	case errors.Is(err, ErrProfileUnknown), errors.Is(err, ErrDeviceNotFound):
		return i18n.T("Устройство не найдено. Обновите список.")
	case errors.Is(err, ErrPanelUnavailable):
		return i18n.T("Панель недоступна, попробуйте позже.")
	default:
		return i18n.T("Ошибка, попробуйте позже.")
	}
}

// deviceLabel renders the panel's self-reported device metadata as-is — it is
// client-supplied text, not something to translate — and falls back to a
// shortened hwid when the app reported nothing usable.
func deviceLabel(d *WebDevice) string {
	parts := make([]string, 0, 2)
	if model := strings.TrimSpace(d.DeviceModel); model != "" {
		parts = append(parts, model)
	}
	if platform := strings.TrimSpace(d.Platform + " " + d.OSVersion); platform != "" {
		parts = append(parts, platform)
	}
	if len(parts) == 0 {
		return shortHWID(d.HWID)
	}
	return strings.Join(parts, " · ")
}

// shortHWID trims an opaque hardware id so it can identify a row without
// swamping a button label.
func shortHWID(hwid string) string {
	r := []rune(hwid)
	if len(r) <= 14 {
		return hwid
	}
	return string(r[:6]) + "…" + string(r[len(r)-4:])
}

// trimLabel keeps a button caption within Telegram's practical width without
// cutting a multi-byte rune in half.
func trimLabel(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}
