package cloud

import (
	"errors"
	"fmt"
	"strings"
)

// Problem is an error the control plane answered (RFC 9457,
// openapi.yaml Problem). errors.Is matches it against the Err values
// below by its code.
type Problem struct {
	Status            int    `json:"status"`
	Type              string `json:"type"`
	Title             string `json:"title"`
	Detail            string `json:"detail,omitempty"`
	Code              string `json:"code"`
	ContractSupported string `json:"contract_supported,omitempty"`
	RetryAfter        int    `json:"retry_after,omitempty"`
}

func (p *Problem) Error() string {
	msg := p.Detail
	if msg == "" {
		msg = p.Title
	}
	if msg == "" {
		msg = fmt.Sprintf("the control plane answered %d", p.Status)
	}
	if p.Code == "contract_unsupported" && p.ContractSupported != "" {
		msg += fmt.Sprintf(" (it serves contract %s; this conch speaks %s — update conch)", p.ContractSupported, Contract)
	}
	return "Conch Cloud: " + strings.TrimSuffix(msg, ".")
}

// Is matches the problem's code to one of the Err values.
func (p *Problem) Is(target error) bool {
	code, ok := codes[target]
	return ok && code == p.Code
}

// Errors by the contract's Problem.code, for errors.Is.
var (
	ErrUnauthorized         = errors.New("unauthorized")
	ErrForbidden            = errors.New("forbidden")
	ErrNotFound             = errors.New("not found")
	ErrInvalidRequest       = errors.New("invalid request")
	ErrAuthorizationPending = errors.New("authorization pending")
	ErrSlowDown             = errors.New("slow down")
	ErrAccessDenied         = errors.New("access denied")
	ErrExpiredToken         = errors.New("expired token")
	ErrPlanLimit            = errors.New("plan limit")
	ErrPaymentRequired      = errors.New("payment required")
	ErrConflict             = errors.New("conflict")
	ErrAgentsWorking        = errors.New("agents working")
	ErrContractUnsupported  = errors.New("contract unsupported")
	ErrRateLimited          = errors.New("rate limited")
	ErrUnavailable          = errors.New("unavailable")
	ErrNoProvider           = errors.New("no provider")
	ErrSuspended            = errors.New("suspended")
)

var codes = map[error]string{
	ErrUnauthorized:         "unauthorized",
	ErrForbidden:            "forbidden",
	ErrNotFound:             "not_found",
	ErrInvalidRequest:       "invalid_request",
	ErrAuthorizationPending: "authorization_pending",
	ErrSlowDown:             "slow_down",
	ErrAccessDenied:         "access_denied",
	ErrExpiredToken:         "expired_token",
	ErrPlanLimit:            "plan_limit",
	ErrPaymentRequired:      "payment_required",
	ErrConflict:             "conflict",
	ErrAgentsWorking:        "agents_working",
	ErrContractUnsupported:  "contract_unsupported",
	ErrRateLimited:          "rate_limited",
	ErrUnavailable:          "unavailable",
	ErrNoProvider:           "no_provider",
	ErrSuspended:            "suspended",
}

// ErrNotSignedIn is a conch with no Conch Cloud account. It is said from
// what is on disk; nothing was asked of the network.
var ErrNotSignedIn = errors.New("not signed in to Conch Cloud (run: conch login)")

// ErrSignedOut is a sign-in the control plane no longer takes — the
// refresh token was revoked, expired or reused, or the device was
// removed — so this conch forgot it.
var ErrSignedOut = errors.New("this device was signed out of Conch Cloud (run: conch login)")
