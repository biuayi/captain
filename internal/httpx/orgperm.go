package httpx

import (
	"context"
	"net/http"

	"github.com/hertz/captain/internal/orgperm"
	"github.com/hertz/captain/internal/token"
)

type orgClaimsKey struct{}

// OrgClaims returns the organizer claims stashed by OrgPerm middleware.
func OrgClaims(ctx context.Context) (token.Claims, bool) {
	c, ok := ctx.Value(orgClaimsKey{}).(token.Claims)
	return c, ok
}

// VersionLoader loads the authoritative organizer.perm_version from PG.
type VersionLoader func(ctx context.Context, orgID string) (int, error)

// OrgPerm builds organizer auth+permission middleware (DESIGN §3.1/§SS-0,
// P0-13). It verifies the organizer JWT, enforces requiredPerm (from the
// JWT.Perm snapshot), and rejects a stale snapshot: the JWT.PermVersion must
// equal the current version (Redis cache → PG fallback). Redis/PG unavailable
// → fail-open (trust the JWT) so an infra blip can't lock every organizer out.
// requiredPerm "" enforces auth only.
func OrgPerm(sig *token.Signer, cache *orgperm.Cache, load VersionLoader, requiredPerm string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c, ok := AuthClaims(r, sig, token.RoleOrganizer)
			if !ok {
				Fail(w, http.StatusUnauthorized, "unauthorized", "需要活动方登录")
				return
			}
			if requiredPerm != "" && !c.Perm[requiredPerm] {
				Fail(w, http.StatusForbidden, "perm_denied", "无此操作权限")
				return
			}
			cur, hit := cache.Get(r.Context(), c.Subject)
			if !hit {
				if v, err := load(r.Context(), c.Subject); err == nil {
					cur, hit = v, true
					cache.Set(r.Context(), c.Subject, v)
				}
			}
			if hit && cur != c.PermVersion {
				Fail(w, http.StatusUnauthorized, "token_stale", "权限已变更，请重新登录")
				return
			}
			ctx := context.WithValue(r.Context(), orgClaimsKey{}, c)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
