package uiutil

import (
	"testing"

	"fyne.io/fyne/v2"
)

func TestIsValidSingleFaceFontRejectsCollectionsAndJunk(t *testing.T) {
	if isValidSingleFaceFont([]byte("ttcf\x00\x01\x00\x00\x00\x00\x00\x02")) {
		t.Fatalf("expected TTC font collection to be rejected")
	}
	if isValidSingleFaceFont([]byte("not a font at all")) {
		t.Fatalf("expected invalid font bytes to be rejected")
	}
	if isValidSingleFaceFont(nil) {
		t.Fatalf("expected nil bytes to be rejected")
	}
}

func TestNewAppThemeFontSafe(t *testing.T) {
	th := NewAppTheme()
	res := th.Font(fyne.TextStyle{})
	if res == nil {
		t.Fatalf("expected non-nil font resource (either JP TTF or default fallback)")
	}
	if !isValidSingleFaceFont(res.Content()) {
		t.Fatalf("resolved theme font failed single-face TTF validation")
	}
}
