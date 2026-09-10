package api

import (
	"github.com/labstack/echo/v4"

	"github.com/frappe/atlas/metal/internal/token"
)

// claimsContextKey names the validated claims in the request context.
const claimsContextKey = "atlas_token_claims"

// requireScopes accepts only an Atlas-signed token that carries every named
// scope. The static API token cannot pass it.
func (s *Server) requireScopes(scopes ...token.Scope) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			signedToken, found := bearerToken(c)
			if !found {
				return unauthorized()
			}

			claims, err := token.Verify(s.trustedKeys.Keys(), signedToken, scopes...)
			if err != nil {
				return err
			}
			c.Set(claimsContextKey, claims)

			return next(c)
		}
	}
}

// tokenClaims returns the claims that requireScopes validated. The handler must
// match the returned VM identifier to the VM its own request addresses.
func tokenClaims(c echo.Context) token.Claims {
	claims, _ := c.Get(claimsContextKey).(token.Claims)
	return claims
}
