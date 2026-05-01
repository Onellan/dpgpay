package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"dpg-pay/internal/middleware"

	"github.com/gorilla/csrf"
	"golang.org/x/crypto/bcrypt"
)

type loginPageData struct {
	Error     string
	CSRFField any
}

func (a *App) AdminLoginPage(w http.ResponseWriter, r *http.Request) {
	a.render(w, "admin_login.html", loginPageData{CSRFField: csrf.TemplateField(r)})
}

func (a *App) AdminLoginSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	username := r.FormValue("username")
	password := r.FormValue("password")
	canonicalUsername := strings.TrimSpace(strings.ToLower(username))

	authenticatedUser := ""
	if canonicalUsername == strings.TrimSpace(strings.ToLower(a.AdminUser)) && bcrypt.CompareHashAndPassword([]byte(a.AdminHash), []byte(password)) == nil {
		authenticatedUser = a.AdminUser
	} else {
		op, err := a.Store.GetActiveOperatorByUsername(r.Context(), canonicalUsername)
		if err == nil && bcrypt.CompareHashAndPassword([]byte(op.PasswordHash), []byte(password)) == nil {
			authenticatedUser = op.Username
		}
	}

	if authenticatedUser == "" {
		_ = a.Store.CreateAuditLog(r.Context(), "LOGIN_FAILED", "anonymous", "", `{"path":"/admin/login","reason":"invalid_credentials"}`)
		a.render(w, "admin_login.html", loginPageData{Error: "Invalid credentials", CSRFField: csrf.TemplateField(r)})
		return
	}

	sessionID, err := randomToken(32)
	if err != nil {
		http.Error(w, "failed to create session", http.StatusInternalServerError)
		return
	}
	expires := time.Now().Add(24 * time.Hour)
	if err := a.Store.CreateSession(r.Context(), sessionID, authenticatedUser, expires); err != nil {
		http.Error(w, "failed to persist session", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     middleware.SessionCookieName,
		Value:    sessionID,
		Path:     "/",
		HttpOnly: true,
		Secure:   a.CookieSecure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int((24 * time.Hour).Seconds()),
		Expires:  expires,
	})

	_ = a.Store.CreateAuditLog(r.Context(), "LOGIN_SUCCESS", authenticatedUser, "", `{"path":"/admin/login"}`)
	http.Redirect(w, r, "/", http.StatusFound)
}

func (a *App) AdminLogout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(middleware.SessionCookieName)
	if err == nil && cookie.Value != "" {
		_ = a.Store.DeleteSession(r.Context(), cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     middleware.SessionCookieName,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   a.CookieSecure,
		SameSite: http.SameSiteStrictMode,
	})
	_ = a.Store.CreateAuditLog(r.Context(), "LOGOUT", "admin", "", `{"path":"/admin/logout"}`)
	http.Redirect(w, r, "/admin/login", http.StatusFound)
}

func randomToken(size int) (string, error) {
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
