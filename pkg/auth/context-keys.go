package auth

// Context keys for user authentication data.
type ContextKey string

const (
	ContextKeyUserID   ContextKey = "user_id"
	ContextKeyUserRole ContextKey = "user_role"
	ContextKeyUserName ContextKey = "user_name"
	ContextKeyUserType ContextKey = "user_type"
	ContextKeyTenantID ContextKey = "tenant_id"
)