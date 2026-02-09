package auth

// ContextKey is a custom type for context keys to avoid collisions.
type ContextKey string

// Context keys for user authentication data.
const (
	ContextKeyUserID   ContextKey = "user_id"
	ContextKeyUserRole ContextKey = "user_role"
	ContextKeyUserName ContextKey = "user_name"
	ContextKeyUserType ContextKey = "user_type"
	ContextKeyTenantID ContextKey = "tenant_id"
)
