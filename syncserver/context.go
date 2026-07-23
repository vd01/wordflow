package syncserver

import "context"

type contextKey string

const tokenKey contextKey = "sync_token"

// contextWithToken stores the token in the context
func contextWithToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, tokenKey, token)
}

// tokenFromContext retrieves the token from the context
func tokenFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(tokenKey).(string); ok {
		return v
	}
	return ""
}
