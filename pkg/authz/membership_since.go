package authz

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/STECH-Super-App/go-common/pkg/auth"
	commonerr "github.com/STECH-Super-App/go-common/pkg/errors"
	"github.com/STECH-Super-App/go-common/pkg/input"
)

// HeaderTenantMembershipSince carries when each of the caller's ACTIVE
// memberships began, as "<tenant_id>=<unix>,<tenant_id>=<unix>". The gateway
// builds it from the token's tns[].at (see auth.TenantClaim) and only chat-service
// reads it (Messenger's unread baseline). It is deliberately a separate header:
// X-Tenant-Memberships stays "<tenant_id>=ROLE" byte for byte, because sale's PHP
// parser drops, and ParseTenantMemberships would read into the role, any entry
// with extra text. Unlike that header, this one is parsed strictly.
const HeaderTenantMembershipSince = "X-Tenant-Membership-Since"

// ReasonMembershipSinceInvalid is the typed 400 for a malformed
// X-Tenant-Membership-Since header.
const ReasonMembershipSinceInvalid = "COMMON_TENANT_MEMBERSHIP_SINCE_INVALID"

// MaxMembershipHeaderEntries bounds the entries of either chat-only header.
// It is a transport sanity bound well above the 16-membership cap per person,
// not the cap itself: the cap is a tenant-service rule enforced where tokens
// are minted.
const MaxMembershipHeaderEntries = 64

// maxUnixSeconds is 9999-12-31T23:59:59Z, the last second time formats with a
// four-digit year; anything later is not a membership start.
const maxUnixSeconds = 253402300799

const nilUUID = "00000000-0000-0000-0000-000000000000"

func invalidMembershipSince() error {
	return commonerr.New(http.StatusBadRequest).
		Reason(ReasonMembershipSinceInvalid).
		Message("Invalid tenant membership start header").
		Build()
}

// FormatMembershipSince renders the X-Tenant-Membership-Since value from the
// token's active memberships: "<tenant_id>=<unix>,…" in claim order. Entries
// without a start time (At == 0, e.g. from an old token) are omitted — an
// absent entry means "no baseline", never a zero baseline. For the output to
// always satisfy ParseMembershipSince, an entry is also omitted when its id is
// not a UUID, its At is out of range, or its id repeats an earlier entry (the
// first wins); output is capped at MaxMembershipHeaderEntries. A claim list with
// nothing to forward yields "".
func FormatMembershipSince(tenants []auth.TenantClaim) string {
	parts := make([]string, 0, len(tenants))
	seen := make(map[string]struct{}, len(tenants))
	for _, t := range tenants {
		if len(parts) == MaxMembershipHeaderEntries {
			break
		}
		id, ok := membershipTenantID(t.ID)
		if !ok || t.At <= 0 || t.At > maxUnixSeconds {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		parts = append(parts, id+"="+strconv.FormatInt(t.At, 10))
	}
	return strings.Join(parts, ",")
}

// ParseMembershipSince decodes X-Tenant-Membership-Since into tenant id (lower
// case) → membership start. An empty header yields an empty map and no error.
// Parsing is strict, because a wrong baseline is a wrong unread count: every
// entry must be "<uuid>=<non-negative unix seconds>", the tenant id may not
// repeat, entries may not be empty, and at most MaxMembershipHeaderEntries are
// accepted. Any violation rejects the whole header with a typed 400
// COMMON_TENANT_MEMBERSHIP_SINCE_INVALID — no partial result is returned.
func ParseMembershipSince(header string) (map[string]time.Time, error) {
	entries, ok := splitMembershipEntries(header)
	if !ok {
		return nil, invalidMembershipSince()
	}
	out := make(map[string]time.Time, len(entries))
	for _, raw := range entries {
		fields := strings.Split(raw, "=")
		if len(fields) != 2 {
			return nil, invalidMembershipSince()
		}
		id, ok := membershipTenantID(fields[0])
		if !ok {
			return nil, invalidMembershipSince()
		}
		since, ok := membershipUnix(fields[1])
		if !ok {
			return nil, invalidMembershipSince()
		}
		if _, dup := out[id]; dup {
			return nil, invalidMembershipSince()
		}
		out[id] = since
	}
	return out, nil
}

// splitMembershipEntries trims the header and splits it into comma-separated
// entries. An empty header is zero entries. ok is false when the header holds an
// empty entry (",,", a leading or trailing comma) or more than
// MaxMembershipHeaderEntries entries; the count is checked before the split so
// an oversized header costs no allocation.
func splitMembershipEntries(header string) (entries []string, ok bool) {
	header = strings.TrimSpace(header)
	if header == "" {
		return nil, true
	}
	if strings.Count(header, ",")+1 > MaxMembershipHeaderEntries {
		return nil, false
	}
	entries = strings.Split(header, ",")
	for _, e := range entries {
		if e == "" {
			return nil, false
		}
	}
	return entries, true
}

// membershipTenantID validates a tenant id the way every fleet input is
// validated (pkg/input) and returns its lower-case form. The nil UUID is not a
// tenant.
func membershipTenantID(raw string) (string, bool) {
	id, failure := input.UUID(raw)
	if failure != nil || id == nilUUID {
		return "", false
	}
	return id, true
}

// membershipUnix parses plain-decimal Unix seconds in [0, maxUnixSeconds].
func membershipUnix(raw string) (time.Time, bool) {
	sec, failure := input.ParseInt64(raw, 0, maxUnixSeconds)
	if failure != nil {
		return time.Time{}, false
	}
	return time.Unix(sec, 0).UTC(), true
}
