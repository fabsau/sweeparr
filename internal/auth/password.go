package auth

import (
	"fmt"
	"strings"
	"unicode"
)

var commonWeakPasswords = map[string]bool{
	"password": true, "password123": true, "12345678": true, "123456789": true,
	"1234567890": true, "qwertyuiop": true, "admin123": true, "administrator": true,
	"letmein": true, "welcome1": true, "changeme": true, "sweeparr": true,
	"plexscanner": true, "saunaflix": true, "iloveyou": true, "abc12345": true,
}

// ValidatePasswordStrength enforces a strong password policy locally without external services.
func ValidatePasswordStrength(pw, username string) error {
	if len(pw) < 10 {
		return fmt.Errorf("password must be at least 10 characters long")
	}
	if len(pw) > 128 {
		return fmt.Errorf("password must be at most 128 characters long")
	}
	low := strings.ToLower(strings.TrimSpace(pw))
	if commonWeakPasswords[low] {
		return fmt.Errorf("password is too common; choose a stronger password")
	}
	if u := strings.ToLower(strings.TrimSpace(username)); len(u) >= 3 && strings.Contains(low, u) {
		return fmt.Errorf("password must not contain your username")
	}

	var hasLower, hasUpper, hasDigit, hasSymbol bool
	uniq := map[rune]bool{}
	for _, r := range pw {
		uniq[r] = true
		switch {
		case unicode.IsLower(r):
			hasLower = true
		case unicode.IsUpper(r):
			hasUpper = true
		case unicode.IsDigit(r):
			hasDigit = true
		case unicode.IsPunct(r) || unicode.IsSymbol(r) || unicode.IsSpace(r):
			hasSymbol = true
		}
	}
	if len(uniq) < 5 {
		return fmt.Errorf("password is too repetitive; use at least 5 distinct characters")
	}
	classes := 0
	for _, ok := range []bool{hasLower, hasUpper, hasDigit, hasSymbol} {
		if ok {
			classes++
		}
	}
	if len(pw) < 14 && classes < 3 {
		return fmt.Errorf("password must include at least 3 of: lowercase, uppercase, digit, symbol (or be 14+ characters)")
	}
	return nil
}
