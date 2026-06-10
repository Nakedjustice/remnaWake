package payments

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Nakedjustice/remnaWake/internal/store"
	tg "github.com/Nakedjustice/remnaWake/internal/telegram"
)

// BotSender is the subset of *telegram.Bot that payments needs.
type BotSender interface {
	SendPlain(ctx context.Context, chatID int64, text string) error
	SendPlainWithKeyboard(ctx context.Context, chatID int64, text string, kb *tg.InlineKeyboardMarkup) (int64, error)
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

// Creator creates a new user in the remote panel.
type Creator interface {
	CreateUser(ctx context.Context, username string, expireAt time.Time) (*CreatedUser, error)
}

// Registrar links an existing panel user to a Telegram ID.
type Registrar interface {
	SetTelegramID(ctx context.Context, uuid string, telegramID int64) error
}

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

// Finder resolves a target subscriber by Telegram ID (may match several) or by
// username (at most one).
type Finder interface {
	FindByTelegramID(ctx context.Context, telegramID int64) ([]Subscriber, error)
	FindByUsername(ctx context.Context, username string) (*Subscriber, error)
}

type adminInputStep int

const (
	adminInputNone adminInputStep = iota
	adminInputRequisites
	adminInputTariffMonths
	adminInputTariffPrice
)

type adminInputState struct {
	step          adminInputStep
	pendingMonths int
}

type adminMsgRef struct {
	chatID    int64
	messageID int64
}

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
	mu        sync.Mutex
	invites   map[int64]*inviteState
	registers map[int64]*registerState
	giftCodes map[int64]*giftCodeState
	redeems   map[int64]*redeemState

	botUsername string // protected by mu; empty = unknown, fall back to raw code
	webAppURL   string // protected by mu; empty = mini app disabled

	adminInput map[int64]adminInputState // protected by mu
	// payMsgs/inviteMsgs map a request ID to the admin message copies of its
	// notification, so confirming/rejecting clears the button for every admin.
	// Entries are deleted on resolve; abandoned (never-actioned) requests leak
	// slowly until restart. TODO: add TTL-based eviction if this grows.
	payMsgs    map[int64][]adminMsgRef // protected by mu
	inviteMsgs map[int64][]adminMsgRef // protected by mu
	giftMsgs   map[int64][]adminMsgRef // protected by mu
	requisites string                  // protected by mu; empty = not set
}

// requisitesKey is the settings-table key under which payment requisites text
// is persisted.
const requisitesKey = "payment_requisites"

func New(st *store.Store, bot BotSender, ext Extender, creator Creator, finder Finder, registrar Registrar, adminIDs []int64, currency string, dryRun bool, logger *slog.Logger) *Service {
	s := &Service{
		store:      st,
		bot:        bot,
		extender:   ext,
		creator:    creator,
		registrar:  registrar,
		finder:     finder,
		adminIDs:   adminIDs,
		currency:   currency,
		dryRun:     dryRun,
		logger:     logger,
		now:        time.Now,
		invites:    make(map[int64]*inviteState),
		registers:  make(map[int64]*registerState),
		giftCodes:  make(map[int64]*giftCodeState),
		redeems:    make(map[int64]*redeemState),
		adminInput: make(map[int64]adminInputState),
		payMsgs:    make(map[int64][]adminMsgRef),
		inviteMsgs: make(map[int64][]adminMsgRef),
		giftMsgs:   make(map[int64][]adminMsgRef),
	}
	// Load persisted payment requisites into the in-memory cache so the user
	// flow never needs a DB read on each button tap.
	if value, found, err := st.GetSetting(context.Background(), requisitesKey); err != nil {
		logger.Error("load requisites failed", "err", err.Error())
	} else if found {
		s.requisites = value
	}
	return s
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
			{{Text: "Я оплатил", CallbackData: fmt.Sprintf("pay:%d", userID)}},
		},
	}
}

// RememberUser persists the user snapshot captured when a notification is sent.
func (s *Service) RememberUser(ctx context.Context, u store.NotifiedUser) error {
	return s.store.UpsertNotifiedUser(ctx, u)
}

func (s *Service) priceLabel(price int) string {
	return fmt.Sprintf("%d%s", price, s.currency)
}
