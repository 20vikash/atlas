package api

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/labstack/echo/v4"

	"github.com/frappe/atlas/metal/internal/token"
)

const (
	testIssuer   = "atlas-1"
	testReceiver = "node-fra-00001"
	testCaller   = "node-fra-00002"
	testKeyID    = "key-1"
)

// newTrustedKeyStore returns a store that trusts the keys.
func newTrustedKeyStore(t *testing.T, trusted ...token.TrustedKeys) *token.KeyStore {
	t.Helper()

	store, err := token.NewKeyStore(filepath.Join(t.TempDir(), "atlas-jwt.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, keys := range trusted {
		if err := store.Replace(keys); err != nil {
			t.Fatal(err)
		}
	}

	return store
}

// atlasSigner signs test tokens.
type atlasSigner struct {
	privateKey ed25519.PrivateKey
	trusted    token.TrustedKeys
}

func newAtlasSigner(t *testing.T) atlasSigner {
	t.Helper()

	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}

	return atlasSigner{privateKey: privateKey, trusted: token.TrustedKeys{
		Issuer:     testIssuer,
		Receiver:   testReceiver,
		PublicKeys: []token.PublicKey{{ID: testKeyID, Key: base64.RawURLEncoding.EncodeToString(publicKey)}},
	}}
}

func (signer atlasSigner) sign(t *testing.T, virtualMachineID string, scopes ...token.Scope) string {
	t.Helper()

	names := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		names = append(names, string(scope))
	}

	claims := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.MapClaims{
		"vm_id": virtualMachineID,
		"scope": names,
		"iss":   testIssuer,
		"sub":   testCaller,
		"aud":   testReceiver,
		"exp":   time.Now().Add(time.Hour).Unix(),
	})
	claims.Header["kid"] = testKeyID

	signed, err := claims.SignedString(signer.privateKey)
	if err != nil {
		t.Fatal(err)
	}

	return signed
}

// newProtectedRouter mounts a JWT route that reports validated claims.
func newProtectedRouter(store TrustedKeyStore, scopes ...token.Scope) http.Handler {
	server := &Server{trustedKeys: store}

	router := echo.New()
	router.HideBanner = true
	router.HTTPErrorHandler = errorHandler
	router.GET("/v1/test", func(c echo.Context) error {
		claims := tokenClaims(c)
		return c.JSON(http.StatusOK, map[string]string{
			"vm_id":  claims.VirtualMachineID,
			"caller": claims.Caller,
		})
	}, server.requireScopes(scopes...))

	return router
}

// call sends one bearer value to the protected route.
func call(router http.Handler, bearer string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, "/v1/test", nil)
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	return recorder
}

func TestProtectedRouteAcceptsACorrectToken(t *testing.T) {
	signer := newAtlasSigner(t)
	router := newProtectedRouter(newTrustedKeyStore(t, signer.trusted), token.ScopeReadVirtualMachine, token.ScopeMigration)

	recorder := call(router, signer.sign(t, "vm-00001", token.ScopeReadVirtualMachine, token.ScopeMigration))
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body)
	}

	var body map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["vm_id"] != "vm-00001" || body["caller"] != testCaller {
		t.Fatalf("expected the validated claims, got %v", body)
	}
}

func TestProtectedRouteRejectsAStaticToken(t *testing.T) {
	signer := newAtlasSigner(t)
	router := newProtectedRouter(newTrustedKeyStore(t, signer.trusted), token.ScopeMigration)

	recorder := call(router, testToken)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", recorder.Code, recorder.Body)
	}
}

func TestProtectedRouteRejectsAMissingToken(t *testing.T) {
	signer := newAtlasSigner(t)
	router := newProtectedRouter(newTrustedKeyStore(t, signer.trusted), token.ScopeMigration)

	if recorder := call(router, ""); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", recorder.Code, recorder.Body)
	}
}

func TestProtectedRouteRejectsAMissingScope(t *testing.T) {
	signer := newAtlasSigner(t)
	router := newProtectedRouter(newTrustedKeyStore(t, signer.trusted), token.ScopeMigration)

	recorder := call(router, signer.sign(t, "vm-00001", token.ScopeReadVirtualMachine))
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", recorder.Code, recorder.Body)
	}

	var body errorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != "forbidden" || body.Error.Retryable {
		t.Fatalf("expected a final forbidden error, got %+v", body.Error)
	}
}

func TestProtectedRouteRejectsEveryTokenWithoutTrustedKeys(t *testing.T) {
	signer := newAtlasSigner(t)
	router := newProtectedRouter(newTrustedKeyStore(t), token.ScopeMigration)

	recorder := call(router, signer.sign(t, "vm-00001", token.ScopeMigration))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", recorder.Code, recorder.Body)
	}
}

func TestControllerRoutesRejectAnAtlasToken(t *testing.T) {
	signer := newAtlasSigner(t)
	server := newTestServer(t)

	request := httptest.NewRequest(http.MethodGet, "/v1/vms", nil)
	request.Header.Set("Authorization", "Bearer "+signer.sign(t, "vm-00001", token.ScopeReadVirtualMachine))
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", recorder.Code, recorder.Body)
	}
}
