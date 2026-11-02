package authz

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/STECH-Super-App/go-common/pkg/auth"
	commonerr "github.com/STECH-Super-App/go-common/pkg/errors"
)

// HeaderTenantReadonlyMemberships carries the caller's read-only memberships —
// seats in SUSPENDED tenants — as "<tenant_id>=<ROLE>=<unix>,…". The gateway
// builds it from the token's tns_ro and only chat-service reads it (history of a
// suspended company stays readable, nobody may send). It is separate from
// X-Tenant-Memberships on purpose: every other service authorizes writes from
// that header assuming each entry is an ACTIVE membership, so a suspended seat
// must never appear there.
const HeaderTenantReadonlyMemberships = "X-Tenant-Readonly-Memberships"

// ReasonReadonlyMembershipsInvalid is the typed 400 for a malformed
// X-Tenant-Readonly-Memberships header.
const ReasonReadonlyMembershipsInvalid = "COMMON_TENANT_READONLY_MEMBERSHIPS_INVALID"

// ReadonlyMembership is a caller's read-only seat in one tenant: the long-form
// role and when the membership began.
type ReadonlyMembership struct {
	TenantID string
	Role     string
	Since    time.Time
}

func invalidReadonlyMemberships() error {
	return commonerr.New(http.StatusBadRequest).
		Reason(ReasonReadonlyMembershipsInvalid).
		Message("Invalid tenant read-only memberships header").
		Build()
}

// FormatReadonlyMemberships renders the X-Tenant-Readonly-Memberships value from
// the token's tns_ro: "<tenant_id>=<ROLE>=<unix>,…" in claim order, the role
// expanded to its long form by ExpandTenantRole — the same expansion as
// X-Tenant-Memberships. For the output to always satisfy
// ParseReadonlyMemberships, an entry is omitted when its id is not a UUID, its
// role is not a known role after expansion, its At is not a positive in-range
// Unix time, or its id repeats an earlier entry (the first wins); output is
// capped at MaxMembershipHeaderEntries. Omitting is fail-closed: a seat that is
// not forwarded grants nothing. A claim list with nothing to forward yields "".
func FormatReadonlyMemberships(tenants []auth.TenantClaim) string {
	parts := make([]string, 0, len(tenants))
	seen := make(map[string]struct{}, len(tenants))
	for _, t := range tenants {
		if len(parts) == MaxMembershipHeaderEntries {
			break
		}
		id, ok := membershipTenantID(t.ID)
		role := ExpandTenantRole(t.R)
		if !ok || !IsTenantRole(role) || t.At <= 0 || t.At > maxUnixSeconds {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		parts = append(parts, id+"="+role+"="+strconv.FormatInt(t.At, 10))
	}
	return strings.Join(parts, ",")
}

// ParseReadonlyMemberships decodes X-Tenant-Readonly-Memberships. An empty header
// yields nil and no error. Parsing is strict, like ParseMembershipSince: every
// entry must be "<uuid>=<ADMIN|MANAGER|OPERATOR>=<non-negative unix seconds>"
// (long-form roles only), the tenant id may not repeat, entries may not be
// empty, and at most MaxMembershipHeaderEntries are accepted. Any violation
// rejects the whole header with a typed 400
// COMMON_TENANT_READONLY_MEMBERSHIPS_INVALID — a seat is never half-trusted.
// Order is preserved and tenant ids are returned lower-case.
func ParseReadonlyMemberships(header string) ([]ReadonlyMembership, error) {
	entries, ok := splitMembershipEntries(header)
	if !ok {
		return nil, invalidReadonlyMemberships()
	}
	if len(entries) == 0 {
		return nil, nil
	}
	out := make([]ReadonlyMembership, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for _, raw := range entries {
		fields := strings.Split(raw, "=")
		if len(fields) != 3 {
			return nil, invalidReadonlyMemberships()
		}
		id, ok := membershipTenantID(fields[0])
		if !ok || !IsTenantRole(fields[1]) {
			return nil, invalidReadonlyMemberships()
		}
		since, ok := membershipUnix(fields[2])
		if !ok {
			return nil, invalidReadonlyMemberships()
		}
		if _, dup := seen[id]; dup {
			return nil, invalidReadonlyMemberships()
		}
		seen[id] = struct{}{}
		out = append(out, ReadonlyMembership{TenantID: id, Role: fields[1], Since: since})
	}
	return out, nil
}
