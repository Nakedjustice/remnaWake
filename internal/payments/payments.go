package payments

import (
	"context"
	"fmt"
	"log/slog"
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
// internal squads (empty = no squads).
type Creator interface {
	CreateUser(ctx context.Context, username string, expireAt time.Time, squadUUIDs []string) (*CreatedUser, error)
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
// "platega" is the automatic Platega gateway.
const (
	ProviderP2P     = "p2p"
	ProviderPlatega = "platega"
)

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
)

type adminInputState struct {
	step adminInputStep

	pendingMonths int
	// pendingBroadcast holds the broadcast text between the preview message and
	// the admin pressing the confirm button (step is back to adminInputNone by
	// then, so regular chat keeps working while the confirmation is pending).
	pendingBroadcast string
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
	store     *store.Store
	bot       BotSender
	extender  Extender
	creator   Creator
	registrar Registrar
	adminIDs  []int64
	currency  string
	dryRun    bool
	logger    *slog.Logger
	now       func() time.Time

	finder    Finder
	squads    SquadLister
	mu        sync.Mutex
	invites   map[int64]*inviteState
	registers map[int64]*registerState
	giftCodes map[int64]*giftCodeState
	redeems   map[int64]*redeemState
	payPhotos map[int64]*payPhotoState

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

	// paymentProvider mirrors the persisted active-provider setting ("p2p" or
	// "platega"). Switching to "platega" is only honored when platega != nil.
	paymentProvider string // protected by mu

	// platega and its parameters are wired once at startup via SetPlatega when
	// Platega credentials are configured; nil = Platega unavailable.
	platega          PlategaGateway // protected by mu
	plategaMethod    int            // protected by mu
	plategaCurrency  string         // protected by mu
	plategaReturnURL string         // protected by mu

	// resolvedSquadUUID caches a successful by-name fallback lookup of the
	// default squad, so user creation doesn't hit the panel's squad listing
	// every time while no squad is explicitly selected. Protected by mu;
	// cleared when an admin selects a squad.
	resolvedSquadUUID string
}

// requisitesKey is the settings-table key under which payment requisites text
// is persisted.
const requisitesKey = "payment_requisites"

// requireScreenshotKey is the settings-table key for the "payment screenshot
// required" toggle ("1" = on, anything else / absent = off).
const requireScreenshotKey = "require_payment_screenshot"

// paymentProviderKey is the settings-table key for the active payment provider
// ("platega"; anything else / absent = "p2p").
const paymentProviderKey = "payment_provider"

// defaultSquadUUIDKey / defaultSquadNameKey are the settings-table keys for
// the admin-selected internal squad assigned to newly created users. The name
// is presentational only; the UUID is what is sent to the panel.
const (
	defaultSquadUUIDKey = "default_squad_uuid"
	defaultSquadNameKey = "default_squad_name"
)

func New(st *store.Store, bot BotSender, ext Extender, creator Creator, finder Finder, registrar Registrar, squads SquadLister, adminIDs []int64, currency string, dryRun bool, logger *slog.Logger) *Service {
	s := &Service{
		store:      st,
		bot:        bot,
		extender:   ext,
		creator:    creator,
		registrar:  registrar,
		finder:     finder,
		squads:     squads,
		adminIDs:   adminIDs,
		currency:   currency,
		dryRun:     dryRun,
		logger:     logger,
		now:        time.Now,
		invites:    make(map[int64]*inviteState),
		registers:  make(map[int64]*registerState),
		giftCodes:  make(map[int64]*giftCodeState),
		redeems:    make(map[int64]*redeemState),
		payPhotos:  make(map[int64]*payPhotoState),
		adminInput: make(map[int64]adminInputState),
		payMsgs:    make(map[int64]adminMsgEntry),
		inviteMsgs: make(map[int64]adminMsgEntry),
		giftMsgs:   make(map[int64]adminMsgEntry),
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
	if value, found, err := st.GetSetting(context.Background(), paymentProviderKey); err != nil {
		logger.Error("load payment provider setting failed", "err", err.Error())
	} else if found && value == ProviderPlatega {
		s.paymentProvider = ProviderPlatega
	}
	return s
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

// plategaConfigured reports whether a Platega gateway is wired in.
func (s *Service) plategaConfigured() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.platega != nil
}

// activeProvider returns the effective payment provider: "platega" only when it
// is both selected and configured, otherwise "p2p". This guarantees behaviour
// is identical to the legacy flow whenever Platega is not wired in.
func (s *Service) activeProvider() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.paymentProvider == ProviderPlatega && s.platega != nil {
		return ProviderPlatega
	}
	return ProviderP2P
}

// getPaymentProvider returns the raw selected provider setting (independent of
// whether Platega is configured), used to render the admin toggle state.
func (s *Service) getPaymentProvider() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.paymentProvider == ProviderPlatega {
		return ProviderPlatega
	}
	return ProviderP2P
}

// setPaymentProvider persists the active-provider toggle and refreshes the
// in-memory cache. Selecting "platega" is rejected when it is not configured.
func (s *Service) setPaymentProvider(ctx context.Context, name string) error {
	if name != ProviderP2P && name != ProviderPlatega {
		return ErrBadInput
	}
	if name == ProviderPlatega && !s.plategaConfigured() {
		return ErrBadInput
	}
	if err := s.store.UpsertSetting(ctx, paymentProviderKey, name); err != nil {
		return err
	}
	s.mu.Lock()
	s.paymentProvider = name
	s.mu.Unlock()
	return nil
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
