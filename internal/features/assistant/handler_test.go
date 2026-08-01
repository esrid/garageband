package assistant_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/esrid/garageband/internal/features/assistant"
)

func TestAssistantHTTPConfirmationFlow(t *testing.T) {
	fixture := newAssistantFixture(t)
	handler := assistantHandler(fixture, assistant.Principal{
		UserID: fixture.ownerID, TenantID: fixture.tenantID,
	})
	response := assistantRequest(handler, http.MethodGet, "/assistant", nil)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Mode démonstration local") {
		t.Fatalf("assistant index = %d %q", response.Code, response.Body.String())
	}
	response = assistantRequest(handler, http.MethodPost, "/assistant/messages", url.Values{
		assistant.FieldLocation: {fixture.locationA}, assistant.FieldMessage: {""},
	})
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "Écrivez une demande") {
		t.Fatalf("empty assistant message = %d %q", response.Code, response.Body.String())
	}

	response = assistantRequest(handler, http.MethodPost, "/assistant/messages", url.Values{
		assistant.FieldLocation: {fixture.locationA},
		assistant.FieldMessage:  {"Mets l'e-mail du site à http@garage.fr"},
	})
	if response.Code != http.StatusSeeOther {
		t.Fatalf("send assistant message = %d %q", response.Code, response.Body.String())
	}
	redirect, err := url.Parse(response.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	conversationID := redirect.Query().Get("conversation")
	if conversationID == "" {
		t.Fatalf("send redirect = %q", response.Header().Get("Location"))
	}
	response = assistantRequest(handler, http.MethodGet, "/assistant?conversation="+conversationID, nil)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Confirmation requise") ||
		!strings.Contains(response.Body.String(), "http@garage.fr") || !strings.Contains(response.Body.String(), "Confirmer l’action") {
		t.Fatalf("assistant proposal = %d %q", response.Code, response.Body.String())
	}
	workspace, err := fixture.store.Workspace(t.Context(), fixture.tenantID, fixture.ownerID, conversationID)
	if err != nil {
		t.Fatal(err)
	}
	executionID := workspace.Executions[0].ID
	response = assistantRequest(
		handler, http.MethodPost,
		"/assistant/"+conversationID+"/tools/"+executionID+"/confirm", nil,
	)
	if response.Code != http.StatusSeeOther || !strings.Contains(response.Header().Get("Location"), "saved=confirmed") {
		t.Fatalf("confirm assistant action = %d location %q body %q", response.Code, response.Header().Get("Location"), response.Body.String())
	}
	assertLocationEmail(t, fixture.fixtures, fixture.locationA, "http@garage.fr")
}

func TestAssistantHTTPConversationIsPrivate(t *testing.T) {
	fixture := newAssistantFixture(t)
	conversationID, err := fixture.service.Send(
		t.Context(), fixture.tenantID, fixture.ownerID, "", fixture.locationA, "Bonjour",
	)
	if err != nil {
		t.Fatal(err)
	}
	handler := assistantHandler(fixture, assistant.Principal{
		UserID: fixture.memberID, TenantID: fixture.tenantID,
	})
	response := assistantRequest(handler, http.MethodGet, "/assistant?conversation="+conversationID, nil)
	if response.Code != http.StatusNotFound {
		t.Fatalf("other employee conversation = %d body %q", response.Code, response.Body.String())
	}
}

func assistantHandler(fixture assistantFixture, principal assistant.Principal) http.Handler {
	mux := http.NewServeMux()
	assistant.Register(
		mux, fixture.store, fixture.service,
		func(next http.Handler) http.Handler { return next },
		func(context.Context) (assistant.Principal, bool) { return principal, true },
	)
	return mux
}

func assistantRequest(handler http.Handler, method string, target string, values url.Values) *httptest.ResponseRecorder {
	body := ""
	if values != nil {
		body = values.Encode()
	}
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	if values != nil {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
