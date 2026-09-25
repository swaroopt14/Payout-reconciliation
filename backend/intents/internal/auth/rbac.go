package auth

import (
	"log"
	"net/http"
	"strings"
)

// RolePayoutApprover is the only role that may approve a held payout intent
// (D39). zord-edge never grants it at signup — every signup gets
// CUSTOMER_ADMIN, which is deliberately NOT an approver — and it can only be
// set through edge's controlled admin grant path.
const RolePayoutApprover = "PAYOUT_APPROVER"

// SubjectTypeUser is the SubjectType RequireAuth stamps on a verified
// end-user session (JWT). Any other subject type (API key, service) is not a
// person and can never hold an approval role.
const SubjectTypeUser = "user"

// HasRole reports whether the principal carries role (trimmed,
// case-insensitive).
func (p AuthPrincipal) HasRole(role string) bool {
	role = strings.TrimSpace(role)
	if role == "" {
		return false
	}
	for _, r := range p.Roles {
		if strings.EqualFold(strings.TrimSpace(r), role) {
			return true
		}
	}
	return false
}

// IsUserSession reports whether the principal is a verified human user
// session, as opposed to an API key or a service credential.
func (p AuthPrincipal) IsUserSession() bool {
	return p.SubjectType == SubjectTypeUser && p.AuthMethod == authMethodJWT && strings.TrimSpace(p.SubjectID) != ""
}

// RequireRole must be chained after RequireAuth (normally Protect(RequireRole(...))).
// It admits only verified USER sessions holding role:
//   - no principal on the request            -> 401
//   - principal is not a user (e.g. API key) -> 403 USER_REQUIRED
//   - user lacks role                        -> 403 ROLE_FORBIDDEN
//
// A request carrying an API-key credential header is rejected outright even
// if it also carries a bearer token, so a machine credential can never ride
// along on an approval.
func RequireRole(role string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, ok := FromContext(r.Context())
		if !ok {
			writeAuthError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "no verified principal on request")
			return
		}
		if hasAPIKeyCredential(r) || !principal.IsUserSession() {
			log.Printf("role_denied route=%s required_role=%q subject_type=%q auth_method=%q reason=not_user",
				r.URL.Path, role, principal.SubjectType, principal.AuthMethod)
			writeAuthError(w, http.StatusForbidden, "USER_REQUIRED", "this action requires a signed-in user; API keys are not accepted")
			return
		}
		if !principal.HasRole(role) {
			log.Printf("role_denied route=%s required_role=%q subject=%q roles=%v reason=missing_role",
				r.URL.Path, role, principal.SubjectID, principal.Roles)
			writeAuthError(w, http.StatusForbidden, "ROLE_FORBIDDEN", "principal lacks required role: "+role)
			return
		}
		next(w, r)
	}
}

// authMethodJWT is the AuthMethod RequireAuth stamps on a verified access token.
const authMethodJWT = "jwt_hs256"

// hasAPIKeyCredential detects the API-key credential shapes zord-edge
// accepts: an X-API-Key header, "Authorization: ApiKey ...", or edge's
// legacy tenant key sent as "Authorization: Bearer prefix.secret" (one dot;
// a JWT always has two).
func hasAPIKeyCredential(r *http.Request) bool {
	if strings.TrimSpace(r.Header.Get("X-API-Key")) != "" {
		return true
	}
	h := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(h) > 7 && strings.EqualFold(h[:7], "ApiKey ") {
		return true
	}
	if tok, ok := bearerToken(r); ok && strings.Count(tok, ".") == 1 {
		return true
	}
	return false
}
