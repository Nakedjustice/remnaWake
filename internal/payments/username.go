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

// isValidNewProfileUsername is the boolean form used by Telegram creation
// conversations. Creation cores use normalizeProfileUsername so the normalized
// value is also the value sent to Remnawave.
func isValidNewProfileUsername(raw string) bool {
	_, err := normalizeProfileUsername(raw)
	return err == nil
}

// isValidProfileLookupUsername preserves the pre-existing lookup rules used by
// /register. Registration links an existing Remnawave profile rather than
// creating one, so imported or legacy Unicode usernames must remain linkable.
func isValidProfileLookupUsername(username string) bool {
	if len(username) < 3 || len(username) > 32 {
		return false
	}
	for _, r := range username {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
			return false
		}
	}
	return true
}
