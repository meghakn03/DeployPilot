package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	api "github.com/deploypilot/deploypilot/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := api.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return New(fake.NewClientBuilder().WithScheme(scheme).Build(), Config{Token: "test-token", Namespaces: map[string]bool{"demo": true}, Registries: []string{"registry.example/team/"}}, nil)
}
func TestCreatePersistsRequest(t *testing.T) {
	s := testServer(t)
	body := `{"name":"hello","namespace":"demo","image":"registry.example/team/hello:v1"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/deployments", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	got := &api.DeploymentRequest{}
	if err := s.Client.Get(req.Context(), typesKey("demo", "hello"), got); err != nil {
		t.Fatal(err)
	}
	if got.Spec.Port != 8080 || *got.Spec.Replicas != 1 {
		t.Fatalf("defaults not applied: %+v", got.Spec)
	}
}
func TestCreateRejectsDisallowedRegistry(t *testing.T) {
	s := testServer(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/deployments", strings.NewReader(`{"name":"hello","namespace":"demo","image":"docker.io/other/image:latest"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	var payload Error
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Error.Fields["image"] == "" {
		t.Fatal("expected image validation error")
	}
}
func TestProtectedRoutesRequireToken(t *testing.T) {
	s := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/deployments", nil)
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status %d", rr.Code)
	}
}

func TestRootReturnsAPIIndex(t *testing.T) {
	s := testServer(t)
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "New deployment") {
		t.Fatal("root should serve the DeployPilot frontend")
	}
}
func typesKey(namespace, name string) types.NamespacedName {
	return types.NamespacedName{Namespace: namespace, Name: name}
}
