package webapp

import (
	"strings"
	"testing"
)

func TestPlanChangeConfirmationUsesInlineControls(t *testing.T) {
	body, err := staticFS.ReadFile("static/index.html")
	if err != nil {
		t.Fatalf("read embedded frontend: %v", err)
	}
	src := string(body)
	for _, want := range []string{
		"const planChangePanel = el('div', 'payment-step');",
		"planChangeMessage(res.plan_change, months)",
		"t('✅ Продолжить')",
		"t('Отмена')",
		"'✅ Продолжить': '✅ Continue',",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("mini app plan-change confirmation is missing %q", want)
		}
	}
	if strings.Contains(src, "confirmDialog(planChangeMessage(") {
		t.Fatal("plan changes must not depend on the native confirmation dialog")
	}
}
