package payments

import (
	"strings"
	"unicode"
)

const (
	profileUsernameMinLength = 3
	profileUsernameMaxLength = 36
)

// normalizeProfileUsername applies the exact Remnawave profile-creation
// contract. Leading and trailing whitespace is treated as accidental input and
// removed; the resulting username must contain only ASCII Latin letters,
// digits, underscores or hyphens.
//
// It is the single normalization point for profile creation: each flow calls it
// once at the edge where the name is first accepted (claimTrial, RedeemGift,
// CreateInviteRequest, the Telegram conversation handlers) and the creation
// cores below them receive an already-normalized value.
func normalizeProfileUsername(raw string) (string, error) {
	username := strings.TrimSpace(raw)
	if len(username) < profileUsernameMinLength || len(username) > profileUsernameMaxLength {
		return "", ErrInvalidUsername
	}
	for i := 0; i < len(username); i++ {
		c := username[i]
		if (c >= 'a' && c <= 'z') ||
			(c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') ||
			c == '_' || c == '-' {
			continue
		}
		return "", ErrInvalidUsername
	}
	return username, nil
}

// isValidProfileLookupUsername gates the /register profile lookup. Registration
// links an existing Remnawave profile rather than creating one, so imported or
// legacy Unicode usernames must remain linkable. It must also stay a superset of
// normalizeProfileUsername: a hyphenated or 33-36 character name this bot just
// created would otherwise be impossible to link back by name.
func isValidProfileLookupUsername(username string) bool {
	if len(username) < profileUsernameMinLength || len(username) > profileUsernameMaxLength {
		return false
	}
	for _, r := range username {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-' {
			return false
		}
	}
	return true
}
