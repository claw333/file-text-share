package main

import "testing"

func TestValidatePassword(t *testing.T) {
	tests := []struct {
		name     string
		password string
		valid    bool
	}{
		{name: "valid", password: "Prototype#2026", valid: true},
		{name: "too short", password: "Short#1A", valid: false},
		{name: "missing uppercase", password: "prototype#2026", valid: false},
		{name: "missing lowercase", password: "PROTOTYPE#2026", valid: false},
		{name: "missing digit", password: "Prototype#Password", valid: false},
		{name: "missing special", password: "Prototype2026", valid: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validatePassword(test.password)
			if (err == nil) != test.valid {
				t.Fatalf("validatePassword() error = %v, valid want %v", err, test.valid)
			}
		})
	}
}

func TestHashAndVerifyPassword(t *testing.T) {
	hash, err := hashPassword("Prototype#2026")
	if err != nil {
		t.Fatal(err)
	}
	if !verifyPassword(hash, "Prototype#2026") {
		t.Fatal("expected password to verify")
	}
	if verifyPassword(hash, "WrongPassword#2026") {
		t.Fatal("wrong password verified")
	}
}

func TestSimplifyUserAgent(t *testing.T) {
	userAgent := "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 Version/18.0 Mobile/15E148 Safari/604.1"
	if got, want := simplifyUserAgent(userAgent), "iPhone · Safari · iOS"; got != want {
		t.Fatalf("simplifyUserAgent() = %q, want %q", got, want)
	}
}
