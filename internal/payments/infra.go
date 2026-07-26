package payments

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Nakedjustice/remnaWake/internal/i18n"
	"github.com/Nakedjustice/remnaWake/internal/store"
	tg "github.com/Nakedjustice/remnaWake/internal/telegram"
)

// RateFetcher fetches base-per-symbol FX rates from a live source. Optional:
// when unset, infrastructure-cost conversion relies on admin-entered manual
// rates only.
type RateFetcher interface {
	FetchRates(ctx context.Context, base string, symbols []string) (map[string]float64, error)
}

// infraBaseCurrencyKey is the settings-table key holding the ISO 4217 code that
// infrastructure costs are converted into (defaults to the service currency).
const infraBaseCurrencyKey = "infra_base_currency"

// infraReminderLeadDays is how many days before a server's payment due date the
// first reminder is sent; a second reminder follows once it is overdue.
const infraReminderLeadDays = 3

// infraMaxPeriodMonths bounds a server's billing period. No hoster bills more
// than five years ahead, so a larger value is a data-entry slip (a price typed
// into the period box) that would otherwise push the due date decades out on
// the next "paid" click.
const infraMaxPeriodMonths = 60

// infraMaxDueYears bounds how far ahead a due date may be set by hand, so a
// mistyped year in the form is rejected instead of stored.
const infraMaxDueYears = 5

// currencySymbols maps ISO 4217 codes to display symbols used in original
// per-server cost labels; unknown codes fall back to the code itself.
var currencySymbols = map[string]string{
	"USD": "$", "EUR": "€", "RUB": "₽", "KZT": "₸", "UAH": "₴", "GBP": "£",
	"JPY": "¥", "CNY": "¥", "TRY": "₺", "PLN": "zł", "BRL": "R$", "INR": "₹",
}

// symbolToISO maps the configured service-currency symbol to an ISO code so FX
// auto-fetch has a base to work with when no explicit base is set.
var symbolToISO = map[string]string{
	"₽": "RUB", "$": "USD", "€": "EUR", "₸": "KZT", "₴": "UAH",
	"£": "GBP", "₺": "TRY", "₹": "INR", "zł": "PLN",
}

// SetForex wires the optional live FX-rate fetcher. Called once at startup;
// until then infrastructure-cost conversion uses admin-entered manual rates.
func (s *Service) SetForex(f RateFetcher) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.forex = f
}

func (s *Service) getForex() RateFetcher {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.forex
}

// baseCurrencyISO returns the ISO code infrastructure costs convert into: the
// persisted override if set, else a code derived from the service currency
// symbol, else the service currency itself when it already looks like a code.
// Empty means no base is known (auto-fetch disabled).
func (s *Service) baseCurrencyISO(ctx context.Context) string {
	if v, found, _ := s.store.GetSetting(ctx, infraBaseCurrencyKey); found {
		if c := strings.ToUpper(strings.TrimSpace(v)); c != "" {
			return c
		}
	}
	if iso, ok := symbolToISO[strings.TrimSpace(s.currency)]; ok {
		return iso
	}
	if c := strings.ToUpper(strings.TrimSpace(s.currency)); len(c) == 3 {
		return c
	}
	return ""
}

func infraCurrencySymbol(code string) string {
	if sym, ok := currencySymbols[strings.ToUpper(code)]; ok {
		return sym
	}
	return strings.ToUpper(code)
}

// infraPriceLabel renders an amount in its own currency, e.g. "$30".
func infraPriceLabel(price int, currency string) string {
	return infraCurrencySymbol(currency) + strconv.Itoa(price)
}

func validCurrencyCode(c string) bool {
	if len(c) != 3 {
		return false
	}
	for _, r := range c {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

// effectiveRate returns base-per-unit for currency, preferring a fetched auto
// rate over the manual fallback. ok is false when no rate is available.
func (s *Service) effectiveRate(ctx context.Context, base, currency string) (float64, bool) {
	base = strings.ToUpper(base)
	currency = strings.ToUpper(currency)
	if base == "" {
		return 0, false
	}
	if currency == base {
		return 1, true
	}
	r, err := s.store.GetFxRate(ctx, currency)
	if err != nil || r == nil {
		return 0, false
	}
	if r.HasAuto && r.AutoRate > 0 {
		return r.AutoRate, true
	}
	if r.HasManual && r.ManualRate > 0 {
		return r.ManualRate, true
	}
	return 0, false
}

// serverMonthlyBase converts a server's normalized monthly cost into the base
// currency. ok is false when the period is invalid or no rate is available.
func (s *Service) serverMonthlyBase(ctx context.Context, base string, srv *store.InfraServer) (float64, bool) {
	if srv.PeriodMonths <= 0 {
		return 0, false
	}
	rate, ok := s.effectiveRate(ctx, base, srv.Currency)
	if !ok {
		return 0, false
	}
	return float64(srv.Price) / float64(srv.PeriodMonths) * rate, true
}

// baseMonthlyLabel formats a converted monthly amount, e.g. "≈ 300₽/мес".
func (s *Service) baseMonthlyLabel(amount float64) string {
	return "≈ " + s.priceLabel(int(math.Round(amount))) + i18n.T("/мес")
}

func infraRAMLabel(mb int) string {
	if mb <= 0 {
		return "—"
	}
	if mb%1024 == 0 {
		return fmt.Sprintf("%d %s", mb/1024, i18n.T("ГБ"))
	}
	return fmt.Sprintf("%d %s", mb, i18n.T("МБ"))
}

// infraDueStatus classifies a due date relative to now: overdue, soon (within
// the reminder lead window), or ok.
func infraDueStatus(due, now time.Time) string {
	switch {
	case !due.After(now):
		return "overdue"
	case due.Sub(now) <= time.Duration(infraReminderLeadDays)*24*time.Hour:
		return "soon"
	default:
		return "ok"
	}
}

// WebInfraServer is one infrastructure server as shown in the mini app: the
// original cost in its own currency plus the estimated monthly cost converted
// into the service currency.
type WebInfraServer struct {
	ID               int64   `json:"id"`
	Name             string  `json:"name"`
	Hoster           string  `json:"hoster,omitempty"`
	Country          string  `json:"country,omitempty"`
	CPUCores         int     `json:"cpu_cores"`
	RAMMB            int     `json:"ram_mb"`
	RAMLabel         string  `json:"ram_label"`
	Price            int     `json:"price"`
	Currency         string  `json:"currency"`
	PriceLabel       string  `json:"price_label"` // e.g. "$30"
	PeriodMonths     int     `json:"period_months"`
	PeriodCostLabel  string  `json:"period_cost_label"`  // e.g. "$30 / 3 мес."
	MonthlyBaseLabel string  `json:"monthly_base_label"` // e.g. "≈ 300₽/мес" or "≈ — (нет курса)"
	MonthlyBase      float64 `json:"monthly_base"`       // converted monthly cost (0 when no rate)
	Converted        bool    `json:"converted"`
	NextDue          string  `json:"next_due,omitempty"` // DD.MM.YYYY
	DueStatus        string  `json:"due_status"`         // ok|soon|overdue|none
	Notes            string  `json:"notes,omitempty"`
}

func (s *Service) toWebInfraServer(ctx context.Context, base string, srv *store.InfraServer) WebInfraServer {
	w := WebInfraServer{
		ID: srv.ID, Name: srv.Name, Hoster: srv.Hoster, Country: srv.Country,
		CPUCores: srv.CPUCores, RAMMB: srv.RAMMB, RAMLabel: infraRAMLabel(srv.RAMMB),
		Price: srv.Price, Currency: srv.Currency, PriceLabel: infraPriceLabel(srv.Price, srv.Currency),
		PeriodMonths: srv.PeriodMonths, Notes: srv.Notes, DueStatus: "none",
	}
	w.PeriodCostLabel = fmt.Sprintf(i18n.T("%s / %d мес."), w.PriceLabel, srv.PeriodMonths)
	if monthly, ok := s.serverMonthlyBase(ctx, base, srv); ok {
		w.MonthlyBaseLabel = s.baseMonthlyLabel(monthly)
		w.MonthlyBase = monthly
		w.Converted = true
	} else {
		w.MonthlyBaseLabel = i18n.T("≈ — (нет курса)")
	}
	if !srv.NextDueAt.IsZero() {
		w.NextDue = srv.NextDueAt.Format("02.01.2006")
		w.DueStatus = infraDueStatus(srv.NextDueAt, s.now())
	}
	return w
}

// AdminListInfraServers returns the infrastructure servers for the mini app
// admin panel, each with its original and converted cost labels.
func (s *Service) AdminListInfraServers(ctx context.Context, telegramID int64) ([]WebInfraServer, error) {
	if err := s.adminGuard(telegramID); err != nil {
		return nil, err
	}
	servers, err := s.store.ListInfraServers(ctx)
	if err != nil {
		return nil, fmt.Errorf("list infra servers: %w", err)
	}
	base := s.baseCurrencyISO(ctx)
	out := make([]WebInfraServer, 0, len(servers))
	for i := range servers {
		out = append(out, s.toWebInfraServer(ctx, base, &servers[i]))
	}
	return out, nil
}

// WebInfraServerInput carries an add/edit from the mini app server form. ID == 0
// creates a new server; NextDue is "YYYY-MM-DD" (HTML date input), "DD.MM.YYYY",
// or empty.
type WebInfraServerInput struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Hoster       string `json:"hoster"`
	Country      string `json:"country"`
	CPUCores     int    `json:"cpu_cores"`
	RAMMB        int    `json:"ram_mb"`
	Price        int    `json:"price"`
	Currency     string `json:"currency"`
	PeriodMonths int    `json:"period_months"`
	NextDue      string `json:"next_due"`
	Notes        string `json:"notes"`
}

func parseInfraDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, nil
	}
	for _, layout := range []string{"2006-01-02", "02.01.2006"} {
		if t, err := time.ParseInLocation(layout, s, time.UTC); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("bad date %q", s)
}

// AdminSaveInfraServer creates or updates an infrastructure server from the mini
// app admin panel.
func (s *Service) AdminSaveInfraServer(ctx context.Context, telegramID int64, in WebInfraServerInput) error {
	if err := s.adminGuard(telegramID); err != nil {
		return err
	}
	in.Name = strings.TrimSpace(in.Name)
	in.Currency = strings.ToUpper(strings.TrimSpace(in.Currency))
	if in.Name == "" || in.Price < 0 || !validCurrencyCode(in.Currency) {
		return ErrBadInput
	}
	if in.PeriodMonths < 1 || in.PeriodMonths > infraMaxPeriodMonths {
		return ErrBadInput
	}
	if in.CPUCores < 0 || in.RAMMB < 0 {
		return ErrBadInput
	}
	due, err := parseInfraDate(in.NextDue)
	if err != nil {
		return ErrBadInput
	}
	if !due.IsZero() && due.After(s.now().AddDate(infraMaxDueYears, 0, 0)) {
		return ErrBadInput
	}
	srv := store.InfraServer{
		ID: in.ID, Name: in.Name, Hoster: strings.TrimSpace(in.Hoster),
		Country: strings.TrimSpace(in.Country), CPUCores: in.CPUCores, RAMMB: in.RAMMB,
		Price: in.Price, Currency: in.Currency, PeriodMonths: in.PeriodMonths,
		NextDueAt: due, Notes: strings.TrimSpace(in.Notes),
	}
	if in.ID == 0 {
		if _, err := s.store.CreateInfraServer(ctx, srv); err != nil {
			return fmt.Errorf("create infra server: %w", err)
		}
		return nil
	}
	updated, err := s.store.UpdateInfraServer(ctx, srv)
	if err != nil {
		return fmt.Errorf("update infra server: %w", err)
	}
	if !updated {
		return ErrInfraServerUnknown
	}
	return nil
}

// AdminDeleteInfraServer removes a server from the mini app admin panel.
func (s *Service) AdminDeleteInfraServer(ctx context.Context, telegramID, id int64) error {
	if err := s.adminGuard(telegramID); err != nil {
		return err
	}
	deleted, err := s.store.DeleteInfraServer(ctx, id)
	if err != nil {
		return fmt.Errorf("delete infra server: %w", err)
	}
	if !deleted {
		return ErrInfraServerUnknown
	}
	return nil
}

// AdminMarkInfraServerPaid moves a server's payment due date to the next date
// still ahead of today and re-arms its reminders. Used by the panel button and
// the reminder message.
//
// The step is one billing period from the current due date (or from today when
// unset), repeated when several periods went unpaid so a single click always
// lands on a future date. Clicking again while the server is already paid past
// the current period does nothing: the click that pays an invoice must not
// stack another period on top, or a double tap walks the date years out.
func (s *Service) AdminMarkInfraServerPaid(ctx context.Context, telegramID, id int64) error {
	if err := s.adminGuard(telegramID); err != nil {
		return err
	}
	srv, err := s.store.GetInfraServer(ctx, id)
	if err != nil {
		return fmt.Errorf("get infra server: %w", err)
	}
	if srv == nil {
		return ErrInfraServerUnknown
	}
	period := srv.PeriodMonths
	if period < 1 {
		period = 1
	}
	if period > infraMaxPeriodMonths {
		period = infraMaxPeriodMonths
	}
	now := s.now()
	if !srv.NextDueAt.IsZero() && !srv.NextDueAt.Before(now.AddDate(0, period, 0)) {
		return nil // already covered for a full period ahead
	}
	from := srv.NextDueAt
	if from.IsZero() {
		from = now
	}
	next := from.AddDate(0, period, 0)
	for !next.After(now) {
		next = next.AddDate(0, period, 0)
	}
	srv.NextDueAt = next
	if _, err := s.store.UpdateInfraServer(ctx, *srv); err != nil {
		return fmt.Errorf("update infra server: %w", err)
	}
	return nil
}

// WebFxRate is one currency's conversion rate as shown in the mini app FX card.
type WebFxRate struct {
	Currency   string  `json:"currency"`
	HasAuto    bool    `json:"has_auto"`
	AutoRate   float64 `json:"auto_rate,omitempty"`
	AutoDate   string  `json:"auto_date,omitempty"` // DD.MM.YYYY
	HasManual  bool    `json:"has_manual"`
	ManualRate float64 `json:"manual_rate,omitempty"`
	InUse      bool    `json:"in_use"`
}

// WebFxRates is the /api/admin/fx payload: the base currency plus the rates for
// every currency in use or with a stored rate.
type WebFxRates struct {
	BaseCurrency  string      `json:"base_currency"`
	BaseSymbol    string      `json:"base_symbol"`
	AutoAvailable bool        `json:"auto_available"`
	Rates         []WebFxRate `json:"rates"`
	Unconverted   int         `json:"unconverted"`
}

// AdminListFxRates returns the FX overview for the mini app: base currency,
// per-currency auto/manual rates, and how many servers cannot be converted.
func (s *Service) AdminListFxRates(ctx context.Context, telegramID int64) (*WebFxRates, error) {
	if err := s.adminGuard(telegramID); err != nil {
		return nil, err
	}
	base := s.baseCurrencyISO(ctx)
	out := &WebFxRates{BaseCurrency: base, BaseSymbol: s.currency, AutoAvailable: s.getForex() != nil}

	servers, err := s.store.ListInfraServers(ctx)
	if err != nil {
		return nil, fmt.Errorf("list infra servers: %w", err)
	}
	inUse := map[string]bool{}
	for i := range servers {
		if c := strings.ToUpper(servers[i].Currency); c != "" && c != base {
			inUse[c] = true
		}
		if _, ok := s.serverMonthlyBase(ctx, base, &servers[i]); !ok {
			out.Unconverted++
		}
	}

	stored, err := s.store.ListFxRates(ctx)
	if err != nil {
		return nil, fmt.Errorf("list fx rates: %w", err)
	}
	byCode := map[string]store.FxRate{}
	codes := map[string]bool{}
	for _, r := range stored {
		c := strings.ToUpper(r.Currency)
		if c == base {
			continue
		}
		byCode[c] = r
		codes[c] = true
	}
	for c := range inUse {
		codes[c] = true
	}
	sorted := make([]string, 0, len(codes))
	for c := range codes {
		sorted = append(sorted, c)
	}
	sort.Strings(sorted)
	for _, c := range sorted {
		r := byCode[c]
		wr := WebFxRate{Currency: c, InUse: inUse[c]}
		if r.HasAuto && r.AutoRate > 0 {
			wr.HasAuto = true
			wr.AutoRate = r.AutoRate
			if !r.AutoUpdatedAt.IsZero() {
				wr.AutoDate = r.AutoUpdatedAt.Format("02.01.2006")
			}
		}
		if r.HasManual && r.ManualRate > 0 {
			wr.HasManual = true
			wr.ManualRate = r.ManualRate
		}
		out.Rates = append(out.Rates, wr)
	}
	return out, nil
}

// AdminSetManualFxRate stores an admin-entered fallback rate (base units per 1
// unit of currency) for the mini app FX card.
func (s *Service) AdminSetManualFxRate(ctx context.Context, telegramID int64, currency string, rate float64) error {
	if err := s.adminGuard(telegramID); err != nil {
		return err
	}
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if !validCurrencyCode(currency) || rate <= 0 {
		return ErrBadInput
	}
	if err := s.store.UpsertManualFxRate(ctx, currency, rate); err != nil {
		return fmt.Errorf("set manual fx rate: %w", err)
	}
	return nil
}

// AdminSetBaseCurrency persists the ISO code infrastructure costs convert into
// and re-fetches auto rates against it (best-effort).
func (s *Service) AdminSetBaseCurrency(ctx context.Context, telegramID int64, iso string) error {
	if err := s.adminGuard(telegramID); err != nil {
		return err
	}
	iso = strings.ToUpper(strings.TrimSpace(iso))
	if !validCurrencyCode(iso) {
		return ErrBadInput
	}
	if err := s.store.UpsertSetting(ctx, infraBaseCurrencyKey, iso); err != nil {
		return fmt.Errorf("set base currency: %w", err)
	}
	_ = s.RefreshInfraFxRates(ctx)
	return nil
}

// AdminRefreshFxRates fetches fresh auto rates on demand from the mini app.
func (s *Service) AdminRefreshFxRates(ctx context.Context, telegramID int64) error {
	if err := s.adminGuard(telegramID); err != nil {
		return err
	}
	return s.RefreshInfraFxRates(ctx)
}

// RefreshInfraFxRates fetches base-per-symbol rates for every currency in use
// and caches them. Best-effort: a nil fetcher, unknown base, or fetch error
// leaves the existing (auto or manual) rates in place. Safe to call from the
// daily scheduler.
func (s *Service) RefreshInfraFxRates(ctx context.Context) error {
	fetcher := s.getForex()
	if fetcher == nil {
		return nil
	}
	base := s.baseCurrencyISO(ctx)
	if base == "" {
		return nil
	}
	servers, err := s.store.ListInfraServers(ctx)
	if err != nil {
		return fmt.Errorf("list infra servers: %w", err)
	}
	symset := map[string]bool{}
	for i := range servers {
		if c := strings.ToUpper(strings.TrimSpace(servers[i].Currency)); c != "" && c != base {
			symset[c] = true
		}
	}
	// Also refresh currencies that already have a stored rate but no current
	// server (e.g. an admin-entered manual rate, or a since-removed server):
	// they still appear as pairs in the FX card, so the "Refresh rates" button
	// must update them too. This mirrors AdminListFxRates's currency set.
	stored, err := s.store.ListFxRates(ctx)
	if err != nil {
		return fmt.Errorf("list fx rates: %w", err)
	}
	for i := range stored {
		if c := strings.ToUpper(strings.TrimSpace(stored[i].Currency)); c != "" && c != base {
			symset[c] = true
		}
	}
	if len(symset) == 0 {
		return nil
	}
	symbols := make([]string, 0, len(symset))
	for c := range symset {
		symbols = append(symbols, c)
	}
	rates, err := fetcher.FetchRates(ctx, base, symbols)
	if err != nil {
		s.logger.Warn("infra fx refresh failed", "err", err.Error())
		return err
	}
	now := s.now()
	for code, rate := range rates {
		if rate <= 0 {
			continue
		}
		if err := s.store.UpsertAutoFxRate(ctx, strings.ToUpper(code), rate, now); err != nil {
			s.logger.Error("infra fx upsert failed", "currency", code, "err", err.Error())
		}
	}
	return nil
}

// RunInfraPaymentReminders notifies all admins about servers whose payment is
// due soon or overdue, advancing each server's reminder stage so the same
// nudge is not repeated. Intended for the daily scheduler.
func (s *Service) RunInfraPaymentReminders(ctx context.Context) {
	if !s.isEnabled() {
		return
	}
	servers, err := s.store.ListInfraServers(ctx)
	if err != nil {
		s.logger.Error("infra reminders: list failed", "err", err.Error())
		return
	}
	now := s.now()
	lead := time.Duration(infraReminderLeadDays) * 24 * time.Hour
	for i := range servers {
		srv := &servers[i]
		if srv.NextDueAt.IsZero() {
			continue
		}
		var stage int
		switch {
		case !srv.NextDueAt.After(now) && srv.RemindedStage < 2:
			stage = 2
		case srv.NextDueAt.Sub(now) <= lead && srv.RemindedStage < 1:
			stage = 1
		default:
			continue
		}
		s.sendInfraReminder(ctx, srv, stage)
		if _, err := s.store.SetInfraServerRemindedStage(ctx, srv.ID, stage); err != nil {
			s.logger.Error("infra reminders: mark failed", "id", srv.ID, "err", err.Error())
		}
	}
}

func (s *Service) sendInfraReminder(ctx context.Context, srv *store.InfraServer, stage int) {
	due := srv.NextDueAt.Format("02.01.2006")
	price := infraPriceLabel(srv.Price, srv.Currency)
	var text string
	if stage >= 2 {
		text = fmt.Sprintf(i18n.T("⚠️ Просрочена оплата сервера «%s».\nХостер: %s\nСумма: %s\nСрок оплаты был: %s"),
			srv.Name, srv.Hoster, price, due)
	} else {
		text = fmt.Sprintf(i18n.T("🔔 Скоро оплата сервера «%s».\nХостер: %s\nСумма: %s\nОплатить до: %s"),
			srv.Name, srv.Hoster, price, due)
	}
	kb := &tg.InlineKeyboardMarkup{
		InlineKeyboard: [][]tg.InlineKeyboardButton{
			{{Text: i18n.T("✅ Отметить оплаченным"), CallbackData: fmt.Sprintf("adm:infrapaid:%d", srv.ID)}},
		},
	}
	for _, adminID := range s.adminIDs {
		_, _ = s.bot.SendPlainWithKeyboard(ctx, adminID, text, kb)
	}
}

// handleInfraPaid resolves the "✅ Отметить оплаченным" button on a reminder:
// it advances the server's due date and clears the button.
func (s *Service) handleInfraPaid(ctx context.Context, cb *tg.CallbackQuery) bool {
	id, err := strconv.ParseInt(strings.TrimPrefix(cb.Data, "adm:infrapaid:"), 10, 64)
	if err != nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Не удалось распознать сервер."))
		return true
	}
	if !s.isAdmin(cb.From.ID) {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Недостаточно прав."))
		return true
	}
	if err := s.AdminMarkInfraServerPaid(ctx, cb.From.ID, id); err != nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Не удалось обновить сервер."))
		return true
	}
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("✅ Платёж отмечен, дата продлена."))
	if cb.Message != nil {
		_ = s.bot.EditMessageReplyMarkup(ctx, cb.Message.Chat.ID, cb.Message.MessageID, nil)
	}
	return true
}
