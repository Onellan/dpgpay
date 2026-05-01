package middleware

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"dpg-pay/internal/models"
)

const SessionCookieName = "dpgpay_session"

type contextKey string

const adminUserKey contextKey = "admin_user"

func AuthRequired(store *models.Store) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie(SessionCookieName)
			if err != nil || cookie.Value == "" {
				http.Redirect(w, r, "/admin/login", http.StatusFound)
				return
			}

			adminUser, expiresAt, err := store.GetSession(r.Context(), cookie.Value)
			if err != nil {
				if err == sql.ErrNoRows {
					http.Redirect(w, r, "/admin/login", http.StatusFound)
					return
				}
				http.Error(w, "session lookup failed", http.StatusInternalServerError)
				return
			}
			if time.Now().After(expiresAt) {
				_ = store.DeleteSession(r.Context(), cookie.Value)
				http.Redirect(w, r, "/admin/login", http.StatusFound)
				return
			}

			ctx := context.WithValue(r.Context(), adminUserKey, adminUser)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func AdminUserFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(adminUserKey).(string); ok {
		return v
	}
	return ""
}
