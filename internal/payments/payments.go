package payments

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/Nakedjustice/remnaWake/internal/i18n"
	"github.com/Nakedjustice/remnaWake/internal/store"
	tg "github.com/Nakedjustice/remnaWake/internal/telegram"
)

// BotSender is the subset of *telegram.Bot that payments needs.
type BotSender interface {
	SendPlain(ctx context.Context, chatID int64, text string) error
	SendPlainWithKeyboard(ctx context.Context, chatID int64, text string, kb *tg.InlineKeyboardMarkup) (int64, error)
	SendPhoto(ctx context.Context, chatID int64, fileID, caption string, kb *tg.InlineKeyboardMarkup) (int64, error)
	SendDocument(ctx context.Context, chatID int64, fileID, caption string, kb *tg.InlineKeyboardMarkup) (int64, error)
	AnswerCallbackQuery(ctx context.Context, id, text string) error
	EditMessageReplyMarkup(ctx context.Context, chatID, messageID int64, kb *tg.InlineKeyboardMarkup) error
	EditMessageText(ctx context.Context, chatID, messageID int64, text string, kb *tg.InlineKeyboardMarkup) error
	SendInvoice(ctx context.Context, chatID int64, title, description, payload string, prices []tg.LabeledPrice) (int64, error)
	CreateInvoiceLink(ctx context.Context, title, description, payload string, prices []tg.LabeledPrice) (string, error)
	AnswerPreCheckoutQuery(ctx context.Context, queryID string, ok bool, errorMessage string) error
}

// Extender is the subset of *remnawave.Client that payments needs.
type Extender interface {
	ExtendSubscriptionByUUID(ctx context.Context, uuid string, newExpireAt time.Time) error
}

// CreatedUser is the minimal result returned after a new user is created.
type CreatedUser struct {
	UUID            string
	Username        string
	SubscriptionURL string
}

// Creator creates a new user in the remote panel, assigned to the given
// internal squads (empty = no squads) and the given traffic-reset strategy
// (empty = NO_RESET).
type Creator interface {
	CreateUser(ctx context.Context, username string, expireAt time.Time, squadUUIDs []string, trafficLimitStrategy string) (*CreatedUser, error)
}

// UserPatch carries the manageable fields of an existing user. It mirrors
// remnawave.UserPatch but is kept payments-local so this package stays
// decoupled from the remnawave package; main.go adapts between the two. A nil
// pointer means "leave unchanged"; a set pointer is applied even when zero
// (0 = unlimited for HWID/traffic).
type UserPatch struct {
	ExpireAt             *time.Time
	HwidDeviceLimit      *int
	TrafficLimitBytes    *int64
	TrafficLimitStrategy *string
	Status               *string // ACTIVE | DISABLED
	ActiveInternalSquads *[]string
}

// UserUpdater patches the manageable fields of an existing panel user.
type UserUpdater interface {
	UpdateUser(ctx context.Context, uuid string, patch UserPatch) error
}

// TrafficResetStrategies is the canonical set of valid traffic-reset strategies.
var TrafficResetStrategies = []string{"NO_RESET", "DAY", "WEEK", "MONTH"}

// validTrafficResetStrategy reports whether s is a recognised reset strategy.
func validTrafficResetStrategy(s string) bool {
	for _, v := range TrafficResetStrategies {
		if v == s {
			return true
		}
	}
	return false
}

// InternalSquad is the minimal squad view the admin pickers need, kept
// payments-local so this package stays decoupled from the remnawave package.
type InternalSquad struct {
	UUID string
	Name string
}

// SquadLister fetches the panel's internal squads.
type SquadLister interface {
	GetInternalSquads(ctx context.Context) ([]InternalSquad, error)
}

// Registrar links an existing panel user to a Telegram ID.
type Registrar interface {
	SetTelegramID(ctx context.Context, uuid string, telegramID int64) error
}

// PlategaGateway is the subset of *platega.Client the payment flow needs. Kept
// payments-local (primitive returns) so this package stays decoupled from the
// platega package; main.go adapts the concrete client to it.
type PlategaGateway interface {
	// CreateTransaction opens a payment and returns its id and redirect URL.
	CreateTransaction(ctx context.Context, method int, amount float64, currency, desc, returnURL, payload string) (id, redirect string, err error)
	// GetTransaction returns the current status of a transaction ("CONFIRMED",
	// "PENDING", ...), used to verify webhook callbacks.
	GetTransaction(ctx context.Context, id string) (status string, err error)
}

// Payment providers. "p2p" is the manual admin-confirmed flow (default);
// "platega" is the automatic Platega gateway; "telegram_stars" is Telegram's
// native in-app Stars (XTR) currency. Several providers may be enabled at once;
// when more than one is enabled the user picks at pay time.
const (
	ProviderP2P           = "p2p"
	ProviderPlatega       = "platega"
	ProviderTelegramStars = "telegram_stars"
)

// allProviders is the canonical iteration/display order of payment providers.
var allProviders = []string{ProviderP2P, ProviderPlatega, ProviderTelegramStars}

// Subscriber is the minimal user view the gift flow needs, kept payments-local
// so this package stays decoupled from the remnawave package.
type Subscriber struct {
	RemnawaveID     int64
	UUID            string
	Username        string
	TelegramID      int64
	ExpireAt        time.Time
	Status          string // panel status: ACTIVE / EXPIRED / DISABLED / LIMITED
	SubscriptionURL string

	// Manageable detail, populated by the single-user finders (FindByUsername /
	// FindByShortUUID) for the admin "Manage user" flow.
	HwidDeviceLimit      *int
	TrafficLimitBytes    int64
	UsedTrafficBytes     int64
	TrafficLimitStrategy string
	SquadUUIDs           []string
	SquadNames           []string
}

// Finder resolves a target subscriber by Telegram ID (may match several), by
// username or by subscription-link short UUID (at most one each), and can list
// every subscriber for broadcasts.
type Finder interface {
	FindByTelegramID(ctx context.Context, telegramID int64) ([]Subscriber, error)
	FindByUsername(ctx context.Context, username string) (*Subscriber, error)
	FindByShortUUID(ctx context.Context, shortUUID string) (*Subscriber, error)
	ListAll(ctx context.Context) ([]Subscriber, error)
}

type adminInputStep int

const (
	adminInputNone adminInputStep = iota
	adminInputRequisites
	adminInputTariffMonths
	adminInputTariffPrice
	adminInputBroadcast
	adminInputUserLookup
	adminInputUserHwid
	adminInputUserTraffic
	adminInputUserExpiry
	adminInputSupportReply
)

type adminInputState struct {
	step adminInputStep

	pendingMonths int
	// pendingBroadcast holds the broadcast text between the preview message and
	// the admin pressing the confirm button (step is back to adminInputNone by
	// then, so regular chat keeps working while the confirmation is pending).
	pendingBroadcast string
	// supportTarget is the user Telegram ID an admin is replying to while step
	// is adminInputSupportReply (set when the admin taps the reply button on a
	// support notification).
	supportTarget int64
}

type adminMsgRef struct {
	chatID    int64
	messageID int64
}

// adminMsgEntry holds every admin's copy of one request notification plus when
// it was recorded, so abandoned (never-actioned) entries can be evicted.
type adminMsgEntry struct {
	refs      []adminMsgRef
	createdAt time.Time
}

// adminMsgTTL is how long admin message refs are kept for a request nobody
// actions; after that the stale confirm buttons just stop being clearable.
const adminMsgTTL = 7 * 24 * time.Hour

// giftCodeState tracks a /gift purchase conversation: the buyer has been
// prompted with the tariff keyboard and we are awaiting their pick.
type giftCodeState struct {
	buyerName string
	buyerTGID int64
	createdAt time.Time
}

// redeemState tracks a gift-code redemption conversation after the recipient
// opened a deep link: either awaiting a profile choice (candidates) or a
// desired username for a new profile (awaitingUsername).
type redeemState struct {
	giftID           int64
	code             string
	months           int
	candidates       []Subscriber
	awaitingUsername bool
	createdAt        time.Time
}

const giftCodeTTL = 10 * time.Minute

// userCtlState remembers which panel user an admin is editing across the inline
// "Manage user" buttons, since UUIDs don't fit cleanly in repeated callback
// data. Evicted after userCtlTTL.
type userCtlState struct {
	uuid      string
	username  string
	createdAt time.Time
}

const userCtlTTL = 10 * time.Minute

// payPhotoState tracks a renewal conversation that is waiting for the user to
// attach a payment screenshot (the screenshot requirement is on): the tariff
// is already picked, the request is created only once the photo arrives.
type payPhotoState struct {
	userID    int64 // remnawave user ID whose subscription is being renewed
	months    int
	price     int
	createdAt time.Time
}

const payPhotoTTL = 10 * time.Minute

type Service struct {
	store       *store.Store
	bot         BotSender
	extender    Extender
	creator     Creator
	registrar   Registrar
	userUpdater UserUpdater
	adminIDs    []int64
	currency    string
	dryRun      bool
	logger      *slog.Logger
	now         func() time.Time

	finder    Finder
	squads    SquadLister
	mu        sync.Mutex
	invites   map[int64]*inviteState
	registers map[int64]*registerState
	giftCodes map[int64]*giftCodeState
	redeems   map[int64]*redeemState
	payPhotos map[int64]*payPhotoState
	userCtl   map[int64]*userCtlState // protected by mu; admin "Manage user" target

	// supportSessions tracks users who are in /support typing mode in the bot:
	// while set, their plain text is routed to the support conversation. The
	// flag is cleared by /cancel or by closing the chat. Persisting the
	// conversation lives in the store; this is only the bot-chat input mode.
	supportSessions map[int64]bool // protected by mu

	botUsername string // protected by mu; empty = unknown, fall back to raw code
	webAppURL   string // protected by mu; empty = mini app disabled

	adminInput map[int64]adminInputState // protected by mu
	// payMsgs/inviteMsgs/giftMsgs map a request ID to the admin message copies
	// of its notification, so confirming/rejecting clears the button for every
	// admin. Entries are deleted on resolve; abandoned requests are evicted
	// after adminMsgTTL by putAdminMsgs.
	payMsgs    map[int64]adminMsgEntry // protected by mu
	inviteMsgs map[int64]adminMsgEntry // protected by mu
	giftMsgs   map[int64]adminMsgEntry // protected by mu
	requisites string                  // protected by mu; empty = not set

	// requireScreenshot mirrors the persisted setting: when true the user must
	// attach a payment screenshot before the request reaches the admins.
	requireScreenshot bool // protected by mu

	// enabledProvidersSet mirrors the persisted set of enabled payment
	// providers. A provider only takes effect when it is also configured
	// (see providerAvailable); an empty effective set falls back to p2p.
	enabledProvidersSet map[string]bool // protected by mu

	// platega and its parameters are wired once at startup via SetPlatega when
	// Platega credentials are configured; nil = Platega unavailable.
	platega          PlategaGateway // protected by mu
	plategaMethod    int            // protected by mu
	plategaCurrency  string         // protected by mu
	plategaReturnURL string         // protected by mu

	// starsEnabled / starsRate are wired once at startup via SetTelegramStars
	// when Telegram Stars is configured. starsRate is how many price units equal
	// one Star; starsEnabled false = Stars unavailable.
	starsEnabled bool // protected by mu
	starsRate    int  // protected by mu

	// resolvedSquadUUID caches a successful by-name fallback lookup of the
	// default squad, so user creation doesn't hit the panel's squad listing
	// every time while no squad is explicitly selected. Protected by mu;
	// cleared when an admin selects a squad.
	resolvedSquadUUID string

	// defaultTrafficReset mirrors the persisted traffic-reset strategy applied to
	// newly created users (absent = NO_RESET). Protected by mu.
	defaultTrafficReset string

	// updateTrigger applies an available bot update when an admin taps "Install
	// now" (typically a Watchtower HTTP-API call). nil = no one-tap install;
	// the button then shows manual instructions. Wired once at startup.
	updateTrigger func(context.Context) error
}

// requisitesKey is the settings-table key under which payment requisites text
// is persisted.
const requisitesKey = "payment_requisites"

// requireScreenshotKey is the settings-table key for the "payment screenshot
// required" toggle ("1" = on, anything else / absent = off).
const requireScreenshotKey = "require_payment_screenshot"

// paymentProviderKey is the legacy settings-table key for the single active
// payment provider ("platega"; anything else / absent = "p2p"). Read once at
// startup to migrate to enabledProvidersKey; no longer written.
const paymentProviderKey = "payment_provider"

// enabledProvidersKey is the settings-table key holding the comma-separated set
// of enabled payment providers (e.g. "p2p,telegram_stars").
const enabledProvidersKey = "enabled_payment_providers"

// defaultSquadUUIDKey / defaultSquadNameKey are the settings-table keys for
// the admin-selected internal squad assigned to newly created users. The name
// is presentational only; the UUID is what is sent to the panel.
const (
	defaultSquadUUIDKey = "default_squad_uuid"
	defaultSquadNameKey = "default_squad_name"
)

// defaultTrafficResetKey is the settings-table key for the traffic-reset
// strategy applied to newly created users (absent = NO_RESET).
const defaultTrafficResetKey = "default_traffic_reset_strategy"

func New(st *store.Store, bot BotSender, ext Extender, creator Creator, updater UserUpdater, finder Finder, registrar Registrar, squads SquadLister, adminIDs []int64, currency string, dryRun bool, logger *slog.Logger) *Service {
	s := &Service{
		store:           st,
		bot:             bot,
		extender:        ext,
		creator:         creator,
		userUpdater:     updater,
		registrar:       registrar,
		finder:          finder,
		squads:          squads,
		adminIDs:        adminIDs,
		currency:        currency,
		dryRun:          dryRun,
		logger:          logger,
		now:             time.Now,
		invites:         make(map[int64]*inviteState),
		registers:       make(map[int64]*registerState),
		giftCodes:       make(map[int64]*giftCodeState),
		redeems:         make(map[int64]*redeemState),
		payPhotos:       make(map[int64]*payPhotoState),
		userCtl:         make(map[int64]*userCtlState),
		supportSessions: make(map[int64]bool),
		adminInput:      make(map[int64]adminInputState),
		payMsgs:         make(map[int64]adminMsgEntry),
		inviteMsgs:      make(map[int64]adminMsgEntry),
		giftMsgs:        make(map[int64]adminMsgEntry),
	}
	// Load persisted payment requisites into the in-memory cache so the user
	// flow never needs a DB read on each button tap.
	if value, found, err := st.GetSetting(context.Background(), requisitesKey); err != nil {
		logger.Error("load requisites failed", "err", err.Error())
	} else if found {
		s.requisites = value
	}
	if value, found, err := st.GetSetting(context.Background(), requireScreenshotKey); err != nil {
		logger.Error("load screenshot setting failed", "err", err.Error())
	} else if found {
		s.requireScreenshot = value == "1"
	}
	s.enabledProvidersSet = s.loadEnabledProviders(context.Background())
	if value, found, err := st.GetSetting(context.Background(), defaultTrafficResetKey); err != nil {
		logger.Error("load default traffic reset setting failed", "err", err.Error())
	} else if found && validTrafficResetStrategy(value) {
		s.defaultTrafficReset = value
	}
	return s
}

// getDefaultTrafficReset returns the configured traffic-reset strategy for new
// users, defaulting to NO_RESET when none is set.
func (s *Service) getDefaultTrafficReset() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.defaultTrafficReset == "" {
		return "NO_RESET"
	}
	return s.defaultTrafficReset
}

// setDefaultTrafficReset validates and persists the traffic-reset strategy for
// newly created users, refreshing the in-memory cache.
func (s *Service) setDefaultTrafficReset(ctx context.Context, strategy string) error {
	if !validTrafficResetStrategy(strategy) {
		return ErrBadInput
	}
	if err := s.store.UpsertSetting(ctx, defaultTrafficResetKey, strategy); err != nil {
		return err
	}
	s.mu.Lock()
	s.defaultTrafficReset = strategy
	s.mu.Unlock()
	return nil
}

// loadEnabledProviders reads the persisted enabled-providers set, migrating from
// the legacy single-provider key when the new key is absent. Defaults to p2p.
func (s *Service) loadEnabledProviders(ctx context.Context) map[string]bool {
	if value, found, err := s.store.GetSetting(ctx, enabledProvidersKey); err != nil {
		s.logger.Error("load enabled providers setting failed", "err", err.Error())
	} else if found {
		return parseProviderSet(value)
	}
	// Migrate from the legacy single-provider setting.
	if value, found, err := s.store.GetSetting(ctx, paymentProviderKey); err != nil {
		s.logger.Error("load payment provider setting failed", "err", err.Error())
	} else if found && value == ProviderPlatega {
		return map[string]bool{ProviderPlatega: true}
	}
	return map[string]bool{ProviderP2P: true}
}

// parseProviderSet parses a comma-separated provider list into a set, keeping
// only recognised provider names.
func parseProviderSet(value string) map[string]bool {
	set := make(map[string]bool)
	for _, name := range strings.Split(value, ",") {
		name = strings.TrimSpace(name)
		switch name {
		case ProviderP2P, ProviderPlatega, ProviderTelegramStars:
			set[name] = true
		}
	}
	if len(set) == 0 {
		set[ProviderP2P] = true
	}
	return set
}

// SetPlatega wires the Platega gateway and its parameters. Called once at
// startup when Platega credentials are configured; until then Platega is
// unavailable and the active provider stays p2p regardless of the setting.
func (s *Service) SetPlatega(gw PlategaGateway, method int, currency, returnURL string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.platega = gw
	s.plategaMethod = method
	s.plategaCurrency = currency
	s.plategaReturnURL = returnURL
}

// SetTelegramStars enables the native Telegram Stars provider with the given
// price-to-Stars conversion rate. Called once at startup when Stars is
// configured; until then Stars is unavailable regardless of the enabled set.
func (s *Service) SetTelegramStars(rate int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.starsEnabled = true
	s.starsRate = rate
}

// plategaConfigured reports whether a Platega gateway is wired in.
func (s *Service) plategaConfigured() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.platega != nil
}

// starsConfigured reports whether the Telegram Stars provider is wired in.
func (s *Service) starsConfigured() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.starsEnabled
}

// providerAvailable reports whether a provider is configured and may be offered.
// p2p is always available; platega and telegram_stars require wiring at startup.
func (s *Service) providerAvailable(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch name {
	case ProviderP2P:
		return true
	case ProviderPlatega:
		return s.platega != nil
	case ProviderTelegramStars:
		return s.starsEnabled
	default:
		return false
	}
}

// enabledProviders returns the effective list of enabled providers (those that
// are both selected and configured), in canonical order. Falls back to ["p2p"]
// when nothing is effectively enabled.
func (s *Service) enabledProviders() []string {
	s.mu.Lock()
	set := s.enabledProvidersSet
	plategaOK := s.platega != nil
	starsOK := s.starsEnabled
	s.mu.Unlock()

	available := func(name string) bool {
		switch name {
		case ProviderP2P:
			return true
		case ProviderPlatega:
			return plategaOK
		case ProviderTelegramStars:
			return starsOK
		default:
			return false
		}
	}

	out := make([]string, 0, len(allProviders))
	for _, name := range allProviders {
		if set[name] && available(name) {
			out = append(out, name)
		}
	}
	if len(out) == 0 {
		return []string{ProviderP2P}
	}
	return out
}

// isProviderEnabled reports whether a provider is in the persisted enabled set
// (independent of configuration), used to render the admin picker state.
func (s *Service) isProviderEnabled(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.enabledProvidersSet[name]
}

// setProviderEnabled adds or removes a provider from the enabled set and
// persists it. Enabling an unconfigured provider, or disabling the last
// effectively-enabled one, is rejected with ErrBadInput.
func (s *Service) setProviderEnabled(ctx context.Context, name string, on bool) error {
	switch name {
	case ProviderP2P, ProviderPlatega, ProviderTelegramStars:
	default:
		return ErrBadInput
	}
	if on && !s.providerAvailable(name) {
		return ErrBadInput
	}
	if !on {
		// Refuse to disable the last effectively-enabled provider.
		remaining := s.enabledProviders()
		if len(remaining) == 1 && remaining[0] == name {
			return ErrBadInput
		}
	}

	s.mu.Lock()
	if s.enabledProvidersSet == nil {
		s.enabledProvidersSet = make(map[string]bool)
	}
	if on {
		s.enabledProvidersSet[name] = true
	} else {
		delete(s.enabledProvidersSet, name)
	}
	serialized := serializeProviderSet(s.enabledProvidersSet)
	s.mu.Unlock()

	if err := s.store.UpsertSetting(ctx, enabledProvidersKey, serialized); err != nil {
		return err
	}
	return nil
}

// serializeProviderSet renders the enabled-providers set as a comma-separated
// list in canonical order.
func serializeProviderSet(set map[string]bool) string {
	ordered := make([]string, 0, len(set))
	for _, name := range allProviders {
		if set[name] {
			ordered = append(ordered, name)
		}
	}
	return strings.Join(ordered, ",")
}

// SetBotUsername stores the bot's own username (from getMe) used to build
// t.me deep links for gift codes. Safe to leave unset: flows fall back to
// sending the raw code with manual instructions.
func (s *Service) SetBotUsername(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.botUsername = name
}

// SetWebAppURL stores the public Mini App URL; when set, the cabinet keyboard
// gains a button that opens the mini app.
func (s *Service) SetWebAppURL(url string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.webAppURL = url
}

func (s *Service) getWebAppURL() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.webAppURL
}

func (s *Service) getBotUsername() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.botUsername
}

func (s *Service) isAdmin(id int64) bool {
	for _, a := range s.adminIDs {
		if a == id {
			return true
		}
	}
	return false
}

func (s *Service) isEnabled() bool {
	return len(s.adminIDs) > 0
}

// PaymentButton returns the single «Я оплатил» keyboard, or nil when the
// payment flow is disabled (no admin configured).
func (s *Service) PaymentButton(userID int64) *tg.InlineKeyboardMarkup {
	if !s.isEnabled() {
		return nil
	}
	return &tg.InlineKeyboardMarkup{
		InlineKeyboard: [][]tg.InlineKeyboardButton{
			{{Text: i18n.T("Я оплатил"), CallbackData: fmt.Sprintf("pay:%d", userID)}},
		},
	}
}

// RememberUser persists the user snapshot captured when a notification is sent.
func (s *Service) RememberUser(ctx context.Context, u store.NotifiedUser) error {
	return s.store.UpsertNotifiedUser(ctx, u)
}

// putAdminMsgs stores the admin message refs for id in m, first evicting
// entries older than adminMsgTTL.
func (s *Service) putAdminMsgs(m map[int64]adminMsgEntry, id int64, refs []adminMsgRef) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for k, e := range m {
		if now.Sub(e.createdAt) > adminMsgTTL {
			delete(m, k)
		}
	}
	m[id] = adminMsgEntry{refs: refs, createdAt: now}
}

// takeAdminMsgs removes and returns the admin message refs stored for id.
func (s *Service) takeAdminMsgs(m map[int64]adminMsgEntry, id int64) []adminMsgRef {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := m[id]
	delete(m, id)
	return e.refs
}

func (s *Service) priceLabel(price int) string {
	return fmt.Sprintf("%d%s", price, s.currency)
}
