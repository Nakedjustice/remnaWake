package webapp

import (
	"strings"
	"testing"
)

// TestActionCenterInfraInlinePanel pins the inline expansion of Action Center
// infrastructure items: clicking an item opens a panel under it on the admin
// hub (no navigation), rows are filtered by due_status and sorted with the
// Infrastructure screen's urgency order, and "Paid" posts through the existing
// endpoint with an in-place refresh.
func TestActionCenterInfraInlinePanel(t *testing.T) {
	body, err := staticFS.ReadFile("static/index.html")
	if err != nil {
		t.Fatalf("read embedded frontend: %v", err)
	}
	src := strings.ReplaceAll(string(body), "\r\n", "\n")
	for _, want := range []string{
		// Infra items expand inline instead of navigating to the infra screen.
		"case 'infra':\n      toggleInfraActionItem(item);",
		"function toggleInfraActionItem(item)",
		"function renderInfraActionDetail()",
		"panel.appendChild(renderInfraActionDetail())",
		// The panel lists exactly the servers matching its item's filter,
		// sorted by urgency like the Infrastructure screen.
		"s.due_status === infraActionOpen.dueStatus",
		"infraDueRank(a) - infraDueRank(b) || infraDueMs(a) - infraDueMs(b)",
		// Rows reuse the existing paid endpoint and loading handling, then
		// refresh servers + Action Center in place.
		"infraPost('/api/admin/server/paid', { id: srv.id }, btn, refreshInfraActionPanel)",
		"async function infraPost(path, payload, button, onDone)",
		"(onDone || showAdminServers)()",
		"async function refreshInfraActionPanel()",
		"/api/admin/servers",
		"/api/admin/action-center",
		// RU source strings and their EN dictionary entries.
		"'Нажмите, чтобы развернуть список серверов ниже.': 'Tap to expand the server list below.',",
		"'Загружаю списки серверов…': 'Loading server list…',",
		"'Список серверов не загрузился. {error}': \"Couldn't load the server list. {error}\",",
		"'Показаны данные, загруженные ранее.': 'Showing previously loaded data.',",
		"'Нет серверов по этому условию.': 'No servers match this filter.',",
		"t('✅ Оплачен')",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("mini app inline infra panel is missing %q", want)
		}
	}
	if strings.Contains(src, "Откройте инфраструктуру") {
		t.Fatal("infra action items must no longer point at the standalone infrastructure screen")
	}
}
