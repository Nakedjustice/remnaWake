// Package i18n localizes the bot's user-facing texts. The Russian source
// string doubles as the translation key (gettext style): T returns it
// unchanged for the default Russian language and looks it up in the target
// dictionary otherwise, falling back to Russian for untranslated entries.
//
// The language is fixed per bot instance via SetLang at startup (BOT_LANG).
package i18n

import "strings"

// Lang identifies a bot interface language.
type Lang string

const (
	RU Lang = "ru"
	EN Lang = "en"
)

// current is set once at startup (before any goroutines send messages) and
// only read afterwards.
var current = RU

// SetLang selects the language used by T. Call once at startup.
func SetLang(l Lang) { current = l }

// Parse normalizes a language code from configuration; ok is false for
// unsupported values (the caller should reject them).
func Parse(s string) (Lang, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "ru":
		return RU, true
	case "en":
		return EN, true
	}
	return RU, false
}

// T translates the Russian source text msg into the current language.
func T(msg string) string {
	if current == RU {
		return msg
	}
	if tr, ok := en[msg]; ok {
		return tr
	}
	return msg
}

// PluralDays returns the word "day" grammatically agreed with n in the
// current language (день / дня / дней for Russian).
func PluralDays(n int) string {
	if current == EN {
		if n == 1 {
			return "day"
		}
		return "days"
	}
	last := n % 100
	if last >= 11 && last <= 14 {
		return "дней"
	}
	switch n % 10 {
	case 1:
		return "день"
	case 2, 3, 4:
		return "дня"
	default:
		return "дней"
	}
}
