package httpx

import (
	"net/http"

	"github.com/hertz/captain/internal/token"
)

// AuthClaims verifies the Bearer auth token and (optionally) the role,
// unifying the duplicated auth() helpers across admin/organizer/participation
// (DESIGN §3.1/P0-12). expectRole "" skips the role check.
func AuthClaims(r *http.Request, sig *token.Signer, expectRole string) (token.Claims, bool) {
	c, err := sig.Verify(BearerToken(r), token.KindAuth)
	if err != nil {
		return token.Claims{}, false
	}
	if expectRole != "" && c.Role != expectRole {
		return token.Claims{}, false
	}
	return c, true
}
