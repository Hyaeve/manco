package source

import (
	"encoding/json"
	"testing"
	"time"
)

func TestAccountReloginDays(t *testing.T) {
	tests := []struct {
		name     string
		extra    map[string]any
		fallback int
		want     int
	}{
		{name: "fallback", fallback: DefaultReloginDays, want: DefaultReloginDays},
		{name: "float", extra: map[string]any{"reloginDays": float64(14)}, fallback: DefaultReloginDays, want: 14},
		{name: "integer", extra: map[string]any{"reloginDays": 21}, fallback: DefaultReloginDays, want: 21},
		{name: "json number", extra: map[string]any{"reloginDays": json.Number("30")}, fallback: DefaultReloginDays, want: 30},
		{name: "string", extra: map[string]any{"reloginDays": "45"}, fallback: DefaultReloginDays, want: 45},
		{name: "ignored zero", extra: map[string]any{"reloginDays": 0}, fallback: DefaultReloginDays, want: DefaultReloginDays},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := AccountReloginDays(Account{Extra: test.extra}, test.fallback); got != test.want {
				t.Fatalf("AccountReloginDays() = %d, want %d", got, test.want)
			}
		})
	}
}

func TestAccountLoginTimestampRoundTrip(t *testing.T) {
	now := time.Date(2026, time.October, 14, 9, 30, 0, 0, time.UTC)
	account := Account{}
	SetAccountReloginDays(&account, 7)
	MarkAccountLoggedIn(&account, now)

	if got := AccountReloginDays(account, 0); got != 7 {
		t.Fatalf("AccountReloginDays() = %d, want 7", got)
	}
	if got := AccountLastLogin(account); !got.Equal(now) {
		t.Fatalf("AccountLastLogin() = %s, want %s", got, now)
	}
}
