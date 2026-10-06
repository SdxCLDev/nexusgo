package auth

import "context"

type ctxKey int

const claimsKey ctxKey = iota

// ContextWithClaims adjunta los claims del token verificado al contexto de la solicitud.
func ContextWithClaims(ctx context.Context, claims *Claims) context.Context {
	return context.WithValue(ctx, claimsKey, claims)
}

// ClaimsFromContext recupera los claims puestos por el middleware de autenticación.
func ClaimsFromContext(ctx context.Context) (*Claims, bool) {
	claims, ok := ctx.Value(claimsKey).(*Claims)
	return claims, ok
}
