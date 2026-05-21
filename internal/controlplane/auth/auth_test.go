package auth

import "testing"

func TestAuthorizeBearer(t *testing.T) {
	a := New([]string{"secret"})
	if !a.Authorize("Bearer secret") {
		t.Fatal("expected auth")
	}
	if a.Authorize("Bearer wrong") {
		t.Fatal("expected reject")
	}
}
