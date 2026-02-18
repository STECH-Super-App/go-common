package auth

// ContextKey is a custom type for context keys to avoid collisions.
type ContextKey string

// Context keys for user authentication data.
const (
	ContextKeyUserID    ContextKey = "user_id"
	ContextKeyUserRoles ContextKey = "user_roles"
	ContextKeyUserName  ContextKey = "user_name"
	ContextKeyUserType  ContextKey = "user_type"
	ContextKeyTenantID  ContextKey = "tenant_id"
	ContextKeyScope     ContextKey = "scope"
	ContextKeyPhone     ContextKey = "phone"
)
