package auth

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	commonerr "github.com/STECH-Super-App/go-common/pkg/errors"
	"github.com/STECH-Super-App/go-common/pkg/input"
)

// Chat read context wire constants: header names, size bounds, readiness,
// grant bases, membership roles and tenant statuses.
const (
	HeaderChatReadContext        = "X-Chat-Read-Context"
	HeaderActingTenantID         = "X-Acting-Tenant-Id"
	MaxChatReadContextBytes      = 64*1024 - len("X-Chat-Read-Context: ") - 2
	MaxAuthorizationHeaderBytes  = 64 * 1024
	MaxChatReadMemberships       = 1024
	ChatReadReady                = "READY"
	ChatReadNotReady             = "NOT_READY"
	ChatGrantActual              = "ACTUAL"
	ChatGrantFeatureCutover      = "FEATURE_CUTOVER"
	ReasonChatReadContextInvalid = "COMMON_CHAT_READ_CONTEXT_INVALID"
	ChatRoleAdmin                = "ADMIN"
	ChatRoleManager              = "MANAGER"
	ChatRoleOperator             = "OPERATOR"
	ChatTenantActive             = "ACTIVE"
	ChatTenantSuspended          = "SUSPENDED"
	zeroUUID                     = "00000000-0000-0000-0000-000000000000"
	ReasonActingTenantInvalid    = "COMMON_ACTING_TENANT_INVALID"
)

// ChatReadContext is a signed read-only snapshot, independent of Claims.Tenants.
// Only READY grants access; NOT_READY must carry no memberships.
type ChatReadContext struct {
	Status      string               `json:"s"`
	Memberships []ChatReadMembership `json:"m"`
}

// ChatReadMembership is one tenant seat in a signed read snapshot.
type ChatReadMembership struct {
	TenantID         string    `json:"id"`
	Role             string    `json:"r"`
	TenantStatus     string    `json:"s"`
	GrantID          string    `json:"g"`
	GrantEffectiveAt time.Time `json:"at"`
	GrantBasis       string    `json:"b"`
}

func invalidChatRead() error {
	return commonerr.New(http.StatusBadRequest).Reason(ReasonChatReadContextInvalid).Message("Invalid chat read context").Build()
}

// ValidateChatReadContext validates the entire snapshot. Call before signing a
// claim and again at the trust boundary; no partial membership is usable.
func ValidateChatReadContext(c *ChatReadContext) error {
	if c == nil || len(c.Memberships) > MaxChatReadMemberships {
		return invalidChatRead()
	}
	if c.Status != ChatReadReady && c.Status != ChatReadNotReady {
		return invalidChatRead()
	}
	if c.Status == ChatReadNotReady && len(c.Memberships) != 0 {
		return invalidChatRead()
	}
	seen := make(map[string]struct{}, len(c.Memberships))
	for _, m := range c.Memberships {
		id, e := input.UUID(m.TenantID)
		if e != nil || id == zeroUUID {
			return invalidChatRead()
		}
		grant, e := input.UUID(m.GrantID)
		if e != nil || grant == zeroUUID {
			return invalidChatRead()
		}
		if _, exists := seen[id]; exists {
			return invalidChatRead()
		}
		seen[id] = struct{}{}
		if m.Role != ChatRoleAdmin && m.Role != ChatRoleManager && m.Role != ChatRoleOperator {
			return invalidChatRead()
		}
		if m.TenantStatus != ChatTenantActive && m.TenantStatus != ChatTenantSuspended {
			return invalidChatRead()
		}
		if m.GrantEffectiveAt.Unix() <= 0 || (m.GrantBasis != ChatGrantActual && m.GrantBasis != ChatGrantFeatureCutover) {
			return invalidChatRead()
		}
	}
	b, err := json.Marshal(c)
	if err != nil || base64.RawURLEncoding.EncodedLen(len(b)) > MaxChatReadContextBytes {
		return invalidChatRead()
	}
	return nil
}

// FormatChatReadContext encodes a fully validated claim as unpadded base64url
// JSON for a gateway-set trusted header. No truncation is performed.
func FormatChatReadContext(c *ChatReadContext) (string, error) {
	if err := ValidateChatReadContext(c); err != nil {
		return "", err
	}
	b, err := json.Marshal(c)
	if err != nil {
		return "", invalidChatRead()
	}
	encoded := base64.RawURLEncoding.EncodeToString(b)
	if len(encoded) > MaxChatReadContextBytes {
		return "", invalidChatRead()
	}
	return encoded, nil
}

// UnmarshalJSON applies the same strict validation to signed claims as to the
// forwarded header. An optional null is absent; it never grants access.
func (c *ChatReadContext) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		*c = ChatReadContext{}
		return nil
	}
	decoded, err := decodeChatReadJSON(data)
	if err != nil {
		return err
	}
	*c = *decoded
	return nil
}

// decodeChatReadJSON uses an alias so JSON decoding cannot call UnmarshalJSON
// recursively. Reject duplicates and unknown fields before accepting any grant.
func decodeChatReadJSON(data []byte) (*ChatReadContext, error) {
	if base64.RawURLEncoding.EncodedLen(len(data)) > MaxChatReadContextBytes || rejectDuplicateJSONKeys(data) != nil {
		return nil, invalidChatRead()
	}
	type chatReadContextJSON ChatReadContext
	var raw chatReadContextJSON
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		return nil, invalidChatRead()
	}
	c := ChatReadContext(raw)
	if err := ValidateChatReadContext(&c); err != nil {
		return nil, err
	}
	return &c, nil
}

// ParseChatReadContext rejects missing, malformed, unknown-field, duplicate-key,
// oversized and incomplete snapshots rather than returning a partial grant.
func ParseChatReadContext(header string) (*ChatReadContext, error) {
	if len(header) == 0 || len(header) > MaxChatReadContextBytes {
		return nil, invalidChatRead()
	}
	b, err := base64.RawURLEncoding.Strict().DecodeString(header)
	if err != nil {
		return nil, invalidChatRead()
	}
	return decodeChatReadJSON(b)
}

// ValidateChatAccessTokenSize applies a 64 KiB transport budget to
// the complete HTTP/1 Authorization header line after signing, including name,
// Bearer prefix and CRLF. A deployment may impose a lower bound. Call only when
// minting the new chat-context claim; existing tokens keep their behavior.
func ValidateChatAccessTokenSize(token string) error {
	if len(token) == 0 || len("Authorization: Bearer ")+len(token)+2 > MaxAuthorizationHeaderBytes {
		return invalidChatRead()
	}
	return nil
}

// ParseActingTenantID validates the selector only; it is never membership proof.
func ParseActingTenantID(raw string) (string, error) {
	id, e := input.UUID(raw)
	if e != nil || id == zeroUUID {
		return "", commonerr.New(http.StatusBadRequest).Reason(ReasonActingTenantInvalid).Message("Invalid acting tenant").Build()
	}
	return id, nil
}

// Membership selects an actor from a READY validated snapshot; this is only a
// membership proof, never object contact or write authorization.
func (c *ChatReadContext) Membership(actor string) (*ChatReadMembership, bool) {
	if ValidateChatReadContext(c) != nil || c.Status != ChatReadReady {
		return nil, false
	}
	id, e := input.UUID(actor)
	if e != nil {
		return nil, false
	}
	for _, m := range c.Memberships {
		normalized, _ := input.UUID(m.TenantID)
		if normalized == id {
			seat := m
			return &seat, true
		}
	}
	return nil, false
}

func rejectDuplicateJSONKeys(b []byte) error {
	d := json.NewDecoder(bytes.NewReader(b))
	var scan func() error
	scan = func() error {
		tok, err := d.Token()
		if err != nil {
			return err
		}
		delim, ok := tok.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return err
				}
				s, ok := key.(string)
				// encoding/json matches struct keys case-insensitively; aliases
				// must not overwrite an earlier field with a second value.
				if !ok || len(seen) >= 6 {
					return invalidChatRead()
				}
				for previous := range seen {
					if strings.EqualFold(previous, s) {
						return invalidChatRead()
					}
				}
				seen[s] = true
				if err = scan(); err != nil {
					return err
				}
			}
			_, err = d.Token()
			return err
		case '[':
			for d.More() {
				if err = scan(); err != nil {
					return err
				}
			}
			_, err = d.Token()
			return err
		default:
			return invalidChatRead()
		}
	}
	if err := scan(); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return invalidChatRead()
	}
	return nil
}
